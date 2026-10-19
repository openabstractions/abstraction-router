package router

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	wire "github.com/openabstractions/abstraction-router/go/abstraction/router"
)

// countingRemote is a paired runtime's router@1 whose offers change and which
// counts the Offer calls it answers.
type countingRemote struct {
	mu     sync.Mutex
	offers []wire.Offer
	asks   int
}

func (f *countingRemote) Models(bool) (wire.ModelsSnapshot, error) { return wire.ModelsSnapshot{}, nil }
func (f *countingRemote) Hosts(bool) (wire.HostsSnapshot, error)   { return wire.HostsSnapshot{}, nil }
func (f *countingRemote) Offer(wire.OfferRequest) (wire.OfferSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asks++
	return wire.OfferSnapshot{Outcome: wire.OfferOutcomeOffered, Offers: append([]wire.Offer(nil), f.offers...)}, nil
}
func (f *countingRemote) set(families ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.offers = nil
	for _, family := range families {
		f.offers = append(f.offers, wire.Offer{Family: family, Names: []string{family}, Servers: []string{"ollama"},
			Context: 32768, ContextSource: wire.ContextSourceMaximum, Input: 28672})
	}
}
func (f *countingRemote) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.asks
}

func observeRouter(t *testing.T, families ...string) (*Router, *countingRemote) {
	t.Helper()
	remote := &countingRemote{}
	remote.set(families...)
	r := New(remoteHost("mac", "mac.test:443", "", func(context.Context) remoteRouter { return remote }))
	if err := r.SurveyContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	return r, remote
}

var hostedOffer = Request{Op: OpObserveOffer, Placement: PlacementHosted, Reserve: 4096}

func TestObserveOfferAnswersAtOnceThenWaitsForASurveyThatChangesIt(t *testing.T) {
	r, remote := observeRouter(t, "llama")
	first, err := r.ObserveOffer(context.Background(), hostedOffer, "", MaxOfferWait)
	if err != nil {
		t.Fatal(err)
	}
	if first.Cursor == "" || len(first.Offers) != 1 || first.Offers[0].Servers[0] != "mac/ollama" {
		t.Fatalf("first reading = %+v", first)
	}
	start := time.Now()
	same, err := r.ObserveOffer(context.Background(), hostedOffer, first.Cursor, 150*time.Millisecond)
	if err != nil || same.Cursor != first.Cursor {
		t.Fatalf("unchanged wait = %+v, %v", same, err)
	}
	if waited := time.Since(start); waited < 100*time.Millisecond {
		t.Fatalf("an unchanged reading answered after %v, before its wait", waited)
	}
	// A survey that finds nothing new keeps the wait going.
	done := make(chan OfferReading, 1)
	go func() {
		reading, err := r.ObserveOffer(context.Background(), hostedOffer, first.Cursor, 10*time.Second)
		if err != nil {
			t.Error(err)
		}
		done <- reading
	}()
	time.Sleep(50 * time.Millisecond)
	if err := r.SurveyContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case reading := <-done:
		t.Fatalf("an unchanged survey answered the wait: %+v", reading)
	case <-time.After(100 * time.Millisecond):
	}
	remote.set("llama", "gemma")
	if err := r.SurveyContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case reading := <-done:
		if reading.Cursor == first.Cursor || len(reading.Offers) != 2 {
			t.Fatalf("changed reading = %+v", reading)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a survey that changed the offers did not answer the wait")
	}
}

func TestObserveOfferAsksAPairedRuntimeOncePerSurveyForEveryWaiter(t *testing.T) {
	r, remote := observeRouter(t, "llama")
	first, err := r.ObserveOffer(context.Background(), hostedOffer, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	before := remote.count()
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.ObserveOffer(context.Background(), hostedOffer, first.Cursor, 300*time.Millisecond); err != nil {
				t.Error(err)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	if err := r.SurveyContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if asked := remote.count() - before; asked != 1 {
		t.Fatalf("five waiters across one survey asked the paired runtime %d times, want 1", asked)
	}
}

func TestObserveOfferAnswersWithinTheCallersDeadline(t *testing.T) {
	r, _ := observeRouter(t, "llama")
	first, err := r.ObserveOffer(context.Background(), hostedOffer, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), observeReplyMargin+200*time.Millisecond)
	defer cancel()
	start := time.Now()
	reading, err := r.ObserveOffer(ctx, hostedOffer, first.Cursor, MaxOfferWait)
	if err != nil || reading.Cursor != first.Cursor {
		t.Fatalf("deadline-bounded wait = %+v, %v", reading, err)
	}
	if waited := time.Since(start); waited > observeReplyMargin {
		t.Fatalf("waited %v into the reply margin", waited)
	}
}

func TestObserveOfferRefusesAnInvalidRequestOrWait(t *testing.T) {
	r, _ := observeRouter(t, "llama")
	for _, c := range []struct {
		req  Request
		wait time.Duration
	}{
		{Request{Placement: "elsewhere"}, 0},
		{Request{Placement: PlacementLocal, Reserve: -1}, 0},
		{Request{Placement: PlacementLocal}, -time.Millisecond},
		{Request{Placement: PlacementLocal}, MaxOfferWait + time.Millisecond},
	} {
		if _, err := r.ObserveOffer(context.Background(), c.req, "", c.wait); !errors.Is(err, ErrInvalidOffer) {
			t.Fatalf("%+v wait %v: err = %v, want ErrInvalidOffer", c.req, c.wait, err)
		}
	}
	answer := r.AnswerContext(context.Background(), Request{Op: OpObserveOffer, Placement: "elsewhere"}, boundSeen())
	if answer.Code != CodeInvalidRequest {
		t.Fatalf("answer = %+v, want invalid_request", answer)
	}
}
