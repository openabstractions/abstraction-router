package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	wire "github.com/openabstractions/abstraction-router/go/abstraction/router"
)

func listingHost(name string, models func() []string) *Host {
	return &Host{Name: name, Base: "http://" + name + ".invalid", Chat: "/chat",
		installed: func(context.Context, *Host) ([]string, error) { return models(), nil },
		resident:  func(context.Context, *Host) ([]string, error) { return nil, nil },
	}
}

func aliasNames(r *Router) []string {
	families, _ := r.Models(false)
	var names []string
	for _, family := range families {
		for _, alias := range family.Names {
			names = append(names, alias.Host+"/"+alias.Name)
		}
	}
	slices.Sort(names)
	return names
}

// A host change while the clock's survey still reads the old hosts starts a
// survey of its own; the older clock survey, finishing last, never replaces
// the newer snapshot (ROUTE-D12).
func TestAnOlderSurveyFinishingLastKeepsTheNewerSnapshot(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls atomic.Int32
	old := &Host{Name: "old", Base: "http://old.invalid", Chat: "/chat",
		installed: func(context.Context, *Host) ([]string, error) {
			if calls.Add(1) == 1 {
				started <- struct{}{}
				<-release
			}
			return []string{"old-model"}, nil
		},
		resident: func(context.Context, *Host) ([]string, error) { return nil, nil },
	}
	r := New(old)
	clock := make(chan error, 1)
	go func() {
		_, err := r.joinSurvey(context.Background(), false)
		clock <- err
	}()
	<-started
	r.SetHosts(listingHost("new", func() []string { return []string{"new-model"} }))
	refreshed := make(chan error, 1)
	go func() { refreshed <- r.SurveyContext(context.Background()) }()
	select {
	case err := <-refreshed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("the refresh joined the survey of the old hosts")
	}
	if got := aliasNames(r); !slices.Equal(got, []string{"new/new-model"}) {
		t.Fatalf("after the refresh: %v", got)
	}
	close(release)
	if err := <-clock; err != nil {
		t.Fatal(err)
	}
	if got := aliasNames(r); !slices.Equal(got, []string{"new/new-model"}) {
		t.Fatalf("the older clock survey replaced the newer snapshot: %v", got)
	}
}

// The clock reads a local engine at every tick, a hosted listing that answered
// at most every HostedListingEvery, and a paired runtime at every tick while
// it answers. A hosted listing or a paired runtime that failed is read again
// on a growing backoff; a host change or a stored credential resets it.
func TestTheClockCadenceFollowsTheKindOfHost(t *testing.T) {
	now := time.Now()
	local := listingHost("local", nil)
	hosted := &Host{Name: "cloud", Hosted: true}
	paired := remoteHost("mac", "mac.invalid:443", "", nil)
	var c cadence
	for _, h := range []*Host{local, hosted, paired} {
		if _, kept := c.kept(h, now); kept {
			t.Fatalf("%s was kept before any read", h.Name)
		}
	}
	up := hostRead{state: HostState{Up: true}}
	down := hostRead{state: HostState{Up: false, Why: "dial failed"}}
	c.record(local, up, 1, 0, now)
	c.record(hosted, up, 1, 0, now)
	c.record(paired, up, 1, 0, now)
	if _, kept := c.kept(local, now.Add(time.Second)); kept {
		t.Fatal("a local engine was kept at the next tick")
	}
	if _, kept := c.kept(hosted, now.Add(time.Minute)); !kept {
		t.Fatal("a hosted listing was read again within its interval")
	}
	if _, kept := c.kept(hosted, now.Add(HostedListingEvery)); kept {
		t.Fatal("a hosted listing was not read after its interval")
	}
	if _, kept := c.kept(paired, now.Add(time.Minute)); kept {
		t.Fatal("an answering paired runtime was not read at the next tick")
	}
	for _, h := range []*Host{paired, hosted} {
		at, last := now, now
		for failures, wait := range []time.Duration{2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 15 * time.Minute, 15 * time.Minute} {
			c.record(h, down, uint64(failures+2), 0, at)
			last = at
			if _, kept := c.kept(h, at.Add(wait-time.Second)); !kept {
				t.Fatalf("%s: after %d failures it was read before %v", h.Name, failures+1, wait)
			}
			if _, kept := c.kept(h, at.Add(wait)); kept {
				t.Fatalf("%s: after %d failures it was not read at %v", h.Name, failures+1, wait)
			}
			at = at.Add(wait)
		}
		c.record(h, up, 1, 0, at)
		if _, kept := c.kept(h, last.Add(time.Minute)); !kept {
			t.Fatalf("%s: an older read replaced a newer one", h.Name)
		}
	}
	c.record(paired, up, 9, 0, now)
	if _, kept := c.kept(paired, now.Add(time.Minute)); kept {
		t.Fatal("an answering paired runtime did not reset its backoff")
	}
	c.record(paired, down, 10, 0, now)
	c.reset(1)
	if _, kept := c.kept(paired, now.Add(time.Second)); kept {
		t.Fatal("a host change did not reset the backoff")
	}
	c.record(paired, down, 11, 0, now)
	if _, kept := c.kept(paired, now.Add(time.Second)); kept {
		t.Fatal("a survey started under the earlier host list recorded a read")
	}
}

// One failed hosted listing is read again after the first backoff step, not
// kept for HostedListingEvery: the person's models return within minutes of
// a provider error.
func TestAFailedHostedListingIsRetriedOnItsBackoff(t *testing.T) {
	now := time.Now()
	hosted := &Host{Name: "deepseek", Hosted: true}
	var c cadence
	c.record(hosted, hostRead{state: HostState{Up: false, Why: "credential:unknown:deepseek"}}, 1, 0, now)
	if _, kept := c.kept(hosted, now.Add(time.Minute)); !kept {
		t.Fatal("a failed listing was read again before its backoff")
	}
	if _, kept := c.kept(hosted, now.Add(2*time.Minute)); kept {
		t.Fatal("a failed hosted listing was kept past its first backoff step")
	}
}

// A credential stored while a survey is already reading its listing wins: the
// stored key is read at once by a survey of its own, and the earlier survey's
// refusal, finishing last, neither records a failure nor replaces the
// snapshot. A fresh call after the store joins the new survey, not the old one.
func TestAStoredCredentialWinsOverASurveyAlreadyReadingItsListing(t *testing.T) {
	listing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"data":[{"id":"offer-model"}]}`))
	}))
	defer listing.Close()
	var stored atomic.Bool
	var applies atomic.Int32
	entered, release := make(chan struct{}, 1), make(chan struct{})
	r := New(NewHosted("deepseek", listing.URL+"/api/v1", WireOpenAICompatible, "deepseek"))
	r.UseCredentials(func(_ context.Context, _, name, _ string) (map[string]string, error) {
		if applies.Add(1) == 1 {
			// The clock's survey asked before the key was stored.
			entered <- struct{}{}
			<-release
			return nil, &CredentialRefusal{Outcome: "unknown", Name: name}
		}
		if !stored.Load() {
			return nil, &CredentialRefusal{Outcome: "unknown", Name: name}
		}
		return map[string]string{"Authorization": "Bearer stored"}, nil
	})
	clock := make(chan error, 1)
	go func() { _, err := r.joinSurvey(context.Background(), false); clock <- err }()
	<-entered
	stored.Store(true)
	reread := make(chan error, 1)
	go func() { reread <- r.RereadHosts(context.Background(), "deepseek") }()
	select {
	case err := <-reread:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("the stored credential's survey joined the survey already reading the listing")
	}
	if got := aliasNames(r); !slices.Equal(got, []string{"deepseek/offer-model"}) {
		t.Fatalf("after the store: %v", got)
	}
	if err := r.SurveyContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-clock; err != nil {
		t.Fatal(err)
	}
	if got := aliasNames(r); !slices.Equal(got, []string{"deepseek/offer-model"}) {
		t.Fatalf("the earlier survey's refusal replaced the snapshot: %v", got)
	}
	if r.cadence.backingOff(r.Hosts()[0], time.Now()) {
		t.Fatal("the earlier survey's refusal recorded a failure after the store")
	}
}

// A clock survey keeps a hosted listing it read within the interval: the
// provider is asked once, not at every tick.
func TestTheClockKeepsAHostedListingWithinItsInterval(t *testing.T) {
	var reads atomic.Int32
	local := listingHost("ollama", func() []string { reads.Add(1); return []string{"local-model"} })
	r := New(local)
	r.SetHosts(local, &Host{Name: "cloud", Hosted: true, Base: "https://cloud.invalid"})
	r.cadence.record(&Host{Name: "cloud", Hosted: true}, hostRead{state: HostState{Host: "cloud", Up: true, Hosted: true}, installed: []string{"cloud-model"}}, 0, 1, time.Now())
	if _, err := r.joinSurvey(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if got := aliasNames(r); !slices.Equal(got, []string{"cloud/cloud-model", "ollama/local-model"}) {
		t.Fatalf("clock survey: %v", got)
	}
	if reads.Load() != 1 {
		t.Fatalf("the local engine was read %d times, want 1", reads.Load())
	}
}

// blockingHost lists one model; its first listing waits for release, ignoring
// its context, as a slow server does.
func blockingHost(name string, started chan<- struct{}, release <-chan struct{}) (*Host, *atomic.Int32) {
	var calls atomic.Int32
	return &Host{Name: name, Base: "http://" + name + ".invalid", Chat: "/chat",
		installed: func(context.Context, *Host) ([]string, error) {
			if calls.Add(1) == 1 {
				started <- struct{}{}
				<-release
			}
			return []string{name + "-model"}, nil
		},
		resident: func(context.Context, *Host) ([]string, error) { return nil, nil },
	}, &calls
}

// A fresh call while the clock's survey runs starts a survey of its own: the
// clock may have skipped a hosted listing the fresh call must read.
func TestAFreshCallStartsItsOwnSurveyWhileTheClockRuns(t *testing.T) {
	started, release := make(chan struct{}, 1), make(chan struct{})
	host, calls := blockingHost("slow", started, release)
	r := New(host)
	clock := make(chan error, 1)
	go func() { _, err := r.joinSurvey(context.Background(), false); clock <- err }()
	<-started
	fresh := make(chan error, 1)
	go func() { fresh <- r.SurveyContext(context.Background()) }()
	select {
	case err := <-fresh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("the fresh call waited on the clock's survey")
	}
	if calls.Load() != 2 {
		t.Fatalf("the host was listed %d times, want once per survey", calls.Load())
	}
	close(release)
	if err := <-clock; err != nil {
		t.Fatal(err)
	}
}

// A clock tick during a fresh call's survey joins it: one listing serves both.
func TestATickJoinsAFreshSurveyInFlight(t *testing.T) {
	started, release := make(chan struct{}, 1), make(chan struct{})
	host, calls := blockingHost("slow", started, release)
	r := New(host)
	fresh := make(chan error, 1)
	go func() { fresh <- r.SurveyContext(context.Background()) }()
	<-started
	tick := make(chan error, 1)
	go func() { _, err := r.joinSurvey(context.Background(), false); tick <- err }()
	time.Sleep(50 * time.Millisecond)
	close(release)
	for _, done := range []chan error{fresh, tick} {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("the host was listed %d times, want one shared survey", calls.Load())
	}
}

// Poll returns only once the survey it joined has ended, so the runtime's
// Close, which waits for Poll, never closes the credential holder under a
// survey in flight.
func TestPollReturnsOnlyAfterItsSurveyEnds(t *testing.T) {
	started, release := make(chan struct{}, 1), make(chan struct{})
	host, _ := blockingHost("slow", started, release)
	r := New(host)
	stop := make(chan struct{})
	returned := make(chan struct{})
	go func() { r.Poll(time.Hour, stop); close(returned) }()
	<-started
	close(stop)
	select {
	case <-returned:
		t.Fatal("Poll returned while its survey still read a host")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("Poll did not return after its survey ended")
	}
}

// Offer asks no paired runtime the clock is backing off from, unless the
// caller asks for a fresh survey (ROUTE-D12).
func TestOfferAsksNoPairedRuntimeInBackoff(t *testing.T) {
	r, remote := observeRouter(t, "llama")
	mac := r.Hosts()[0]
	r.cadence.record(mac, hostRead{state: HostState{Up: false, Why: "dial failed"}}, r.surveyGen+1, r.hostsGen, time.Now())
	before := remote.count()
	offers, _, err := r.OfferContext(context.Background(), Request{Op: OpOffer, Placement: PlacementHosted})
	if err != nil || len(offers) != 0 || remote.count() != before {
		t.Fatalf("a backed-off paired runtime was asked: offers=%+v asks=%d->%d err=%v", offers, before, remote.count(), err)
	}
	r.cadence.advance(r.hostsGen, mac.Name)
	if offers, _, err := r.OfferContext(context.Background(), Request{Op: OpOffer, Placement: PlacementHosted}); err != nil || len(offers) != 1 {
		t.Fatalf("an answering paired runtime was not asked: %+v %v", offers, err)
	}
}

// An ObserveOffer wait that ends with no new survey answers an age at least
// as long as the wait: the age is measured when the answer is sent
// (ROUTE-D11).
func TestObserveOfferAgeIsMeasuredWhenTheAnswerIsSent(t *testing.T) {
	r, _ := observeRouter(t, "llama")
	first, err := r.ObserveOffer(context.Background(), hostedOffer, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	wait := 300 * time.Millisecond
	request := hostedOffer
	request.Cursor, request.Wait = first.Cursor, wait
	answer := r.AnswerContext(context.Background(), request, boundSeen())
	if answer.Err() != nil || answer.Cursor != first.Cursor {
		t.Fatalf("unchanged wait = %+v", answer)
	}
	if answer.AgeMS < wait.Milliseconds() {
		t.Fatalf("age %d ms after a %v wait with no survey", answer.AgeMS, wait)
	}
}

// A paired runtime whose Offer fails drops out of the reading: to a watcher it
// reads as a removal, which the next survey can reverse (ROUTE-S4).
func TestAFailedPairedOfferReadsAsARemoval(t *testing.T) {
	failing := &failingOnce{countingRemote: countingRemote{}}
	failing.set("llama")
	r := New(remoteHost("mac", "mac.test:443", "", func(context.Context) remoteRouter { return failing }))
	if err := r.SurveyContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	first, err := r.ObserveOffer(context.Background(), hostedOffer, "", 0)
	if err != nil || len(first.Offers) != 1 {
		t.Fatalf("first reading %+v %v", first, err)
	}
	failing.fail.Store(true)
	if err := r.SurveyContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	gone, err := r.ObserveOffer(context.Background(), hostedOffer, first.Cursor, time.Second)
	if err != nil || gone.Cursor == first.Cursor || len(gone.Offers) != 0 {
		t.Fatalf("a failed paired Offer was not a removal: %+v %v", gone, err)
	}
	failing.fail.Store(false)
	if err := r.SurveyContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	back, err := r.ObserveOffer(context.Background(), hostedOffer, gone.Cursor, time.Second)
	if err != nil || back.Cursor != first.Cursor {
		t.Fatalf("the next survey did not restore the paired runtime: %+v %v", back, err)
	}
}

type failingOnce struct {
	countingRemote
	fail atomic.Bool
}

func (f *failingOnce) Offer(request wire.OfferRequest) (wire.OfferSnapshot, error) {
	if f.fail.Load() {
		return wire.OfferSnapshot{}, context.DeadlineExceeded
	}
	return f.countingRemote.Offer(request)
}
