package router

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/openabstractions/abstraction-identity/listen"
	wire "github.com/openabstractions/abstraction-router/go/abstraction/router"
)

type offerAlias struct {
	Name                   string   `json:"name"`
	Host                   string   `json:"host"`
	Servable               bool     `json:"servable"`
	Hosted                 bool     `json:"hosted"`
	WindowAtLoad           bool     `json:"windowAtLoad"`
	Profiles               []string `json:"profiles"`
	ContextLength          int64    `json:"contextLength"`
	EffectiveContextLength int64    `json:"effectiveContextLength"`
}

type offerFamily struct {
	Family string       `json:"family"`
	Names  []offerAlias `json:"names"`
}

type offerWant struct {
	Family        string   `json:"family"`
	Names         []string `json:"names"`
	Servers       []string `json:"servers"`
	Context       int64    `json:"context"`
	ContextSource string   `json:"contextSource"`
	Input         int64    `json:"input"`
	Unknown       []string `json:"unknown"`
}

type offerCase struct {
	Name     string        `json:"name"`
	Servers  []string      `json:"servers"`
	Families []offerFamily `json:"families"`
	Remotes  map[string]struct {
		Offers        []offerWant `json:"offers"`
		UnknownMethod bool        `json:"unknownMethod"`
	} `json:"remotes"`
	Requests []struct {
		Profile   string      `json:"profile"`
		Placement string      `json:"placement"`
		Reserve   int64       `json:"reserve"`
		Offers    []offerWant `json:"offers"`
	} `json:"requests"`
}

// fakeRemote is a remote runtime's router@1 answering from fixed values.
type fakeRemote struct {
	models        wire.ModelsSnapshot
	offers        []wire.Offer
	unknownMethod bool
	outcome       wire.OfferOutcome
	asked         []wire.OfferRequest
}

func (f *fakeRemote) Models(bool) (wire.ModelsSnapshot, error) { return f.models, nil }
func (f *fakeRemote) Hosts(bool) (wire.HostsSnapshot, error)   { return wire.HostsSnapshot{}, nil }
func (f *fakeRemote) Offer(request wire.OfferRequest) (wire.OfferSnapshot, error) {
	f.asked = append(f.asked, request)
	if f.unknownMethod {
		return wire.OfferSnapshot{}, &wire.ServiceError{Code: wire.ServiceErrorCodeUnknownMethod}
	}
	outcome := f.outcome
	if outcome == 0 {
		outcome = wire.OfferOutcomeOffered
	}
	return wire.OfferSnapshot{Outcome: outcome, Offers: f.offers}, nil
}

func (w offerWant) wire() wire.Offer {
	return wire.Offer{Family: w.Family, Names: w.Names, Servers: w.Servers, Context: w.Context,
		ContextSource: wire.ContextSource(w.ContextSource), Input: w.Input, Unknown: w.Unknown}
}

func (w offerWant) offer() Offer {
	return Offer{Family: w.Family, Names: w.Names, Servers: w.Servers, Context: w.Context,
		ContextSource: w.ContextSource, Input: w.Input, Unknown: w.Unknown}
}

// offerRouter is a Router holding c's families as its latest survey, with a
// server per alias host.
func offerRouter(t *testing.T, c offerCase) (*Router, map[string]*fakeRemote) {
	t.Helper()
	order := append([]string(nil), c.Servers...)
	facts := map[string]offerAlias{}
	for _, f := range c.Families {
		for _, a := range f.Names {
			if !has(order, a.Host) {
				order = append(order, a.Host)
			}
			facts[a.Host] = a
		}
	}
	remotes := map[string]*fakeRemote{}
	var hosts []*Host
	for _, name := range order {
		if remote, ok := c.Remotes[name]; ok {
			fake := &fakeRemote{unknownMethod: remote.UnknownMethod}
			for _, o := range remote.Offers {
				fake.offers = append(fake.offers, o.wire())
			}
			remotes[name] = fake
			hosts = append(hosts, remoteHost(name, name+".test:443", "", func(context.Context) remoteRouter { return fake }))
			continue
		}
		fact := facts[name]
		h := &Host{Name: name, Base: "http://" + name + ".test", Chat: "/v1/chat/completions", windowAtLoad: fact.WindowAtLoad}
		if fact.Hosted {
			h.Hosted, h.Wire = true, WireOpenAICompatible
		}
		hosts = append(hosts, h)
	}
	profiles := map[string]map[string][]string{}
	var families []Family
	for _, f := range c.Families {
		family := Family{Family: f.Family}
		for _, a := range f.Names {
			family.Names = append(family.Names, Alias{Host: a.Host, Name: a.Name, Servable: a.Servable, Hosted: a.Hosted, Profiles: a.Profiles,
				ContextLength: a.ContextLength, EffectiveContextLength: a.EffectiveContextLength})
			if len(a.Profiles) > 0 {
				if profiles[a.Host] == nil {
					profiles[a.Host] = map[string][]string{}
				}
				profiles[a.Host][a.Name] = a.Profiles
			}
		}
		families = append(families, family)
	}
	r := New(hosts...)
	r.snap = snapshot{at: time.Now(), families: families, profiles: profiles}
	return r, remotes
}

// TestOfferCases holds Offer to every case of testdata/offer.json (ROUTE-D8,
// ROUTE-S4).
func TestOfferCases(t *testing.T) {
	raw, err := os.ReadFile("testdata/offer.json")
	if err != nil {
		t.Fatal(err)
	}
	var table struct {
		Cases []offerCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	if len(table.Cases) == 0 {
		t.Fatal("no offer cases")
	}
	for _, c := range table.Cases {
		for _, request := range c.Requests {
			t.Run(c.Name+"/"+request.Profile+"/"+request.Placement, func(t *testing.T) {
				r, remotes := offerRouter(t, c)
				req := Request{Op: OpOffer, Profile: request.Profile, Placement: request.Placement, Reserve: request.Reserve}
				got, _, err := r.OfferContext(context.Background(), req)
				if err != nil {
					t.Fatal(err)
				}
				var want []Offer
				for _, o := range request.Offers {
					want = append(want, o.offer())
				}
				if len(got) != len(want) || (len(want) > 0 && !reflect.DeepEqual(got, want)) {
					gotJSON, _ := json.MarshalIndent(got, "", " ")
					wantJSON, _ := json.MarshalIndent(want, "", " ")
					t.Fatalf("offers\n got %s\nwant %s", gotJSON, wantJSON)
				}
				for name, fake := range remotes {
					if request.Placement == PlacementLocal {
						if len(fake.asked) != 0 {
							t.Fatalf("remote %s asked for a local offer: %+v", name, fake.asked)
						}
						continue
					}
					wantAsked := wire.OfferRequest{Profile: request.Profile, Placement: wire.PlacementAny, Reserve: request.Reserve}
					if len(fake.asked) != 1 || fake.asked[0] != wantAsked {
						t.Fatalf("remote %s asked %+v, want one %+v", name, fake.asked, wantAsked)
					}
				}
			})
		}
	}
}

// TestOfferRefusesAnUnknownPlacementOrNegativeReserve: the legacy line
// request carries placement as a word, and a word outside the enum or a
// negative reserve is a malformed request.
func TestOfferRefusesAnUnknownPlacementOrNegativeReserve(t *testing.T) {
	r := New()
	for _, req := range []Request{
		{Op: OpOffer, Placement: "elsewhere"},
		{Op: OpOffer, Placement: ""},
		{Op: OpOffer, Placement: PlacementLocal, Reserve: -1},
	} {
		out := r.Answer(req, boundSeen())
		if out.Code != CodeInvalidRequest || out.Offers != nil {
			t.Fatalf("%+v answered %+v", req, out)
		}
	}
	out := r.Answer(Request{Op: OpOffer, Placement: PlacementAny}, boundSeen())
	if out.Code != "" {
		t.Fatalf("an empty router refused an offer: %+v", out)
	}
	unknown := r.Answer(Request{Op: "bogus"}, boundSeen())
	if unknown.Code != CodeUnknownOperation || !strings.Contains(unknown.Error, OpOffer) {
		t.Fatalf("unknown op answered %+v", unknown)
	}
}

// TestOfferFreshSurveysFirst: fresh reads the servers before folding.
func TestOfferFreshSurveysFirst(t *testing.T) {
	fake := &fakeRemote{models: wire.ModelsSnapshot{Models: []wire.Family{{Family: "qwen", Names: []wire.Alias{
		{Host: "lemonade", Name: "qwen", Servable: true, ContextLength: 40960, EffectiveContextLength: 8192},
	}}}}, unknownMethod: true}
	r := New(remoteHost("far", "far.test:443", "", func(context.Context) remoteRouter { return fake }))
	stale, _, err := r.OfferContext(context.Background(), Request{Op: OpOffer, Placement: PlacementAny})
	if err != nil || len(stale) != 0 {
		t.Fatalf("before any survey: %+v %v", stale, err)
	}
	fresh, _, err := r.OfferContext(context.Background(), Request{Op: OpOffer, Placement: PlacementAny, Reserve: 4096, Fresh: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []Offer{{Family: "qwen", Names: []string{"qwen"}, Servers: []string{"far"}, Context: 8192, ContextSource: ContextEffective, Input: 4096}}
	if !reflect.DeepEqual(fresh, want) {
		t.Fatalf("fresh offers %+v, want %+v", fresh, want)
	}
}

// TestRemoteAliasesCarryContext: a remote runtime's Models aliases keep the
// context its own router reported (ROUTE-S4), the smallest bound per name.
func TestRemoteAliasesCarryContext(t *testing.T) {
	fake := &fakeRemote{models: wire.ModelsSnapshot{Models: []wire.Family{
		{Family: "qwen", Names: []wire.Alias{
			{Host: "lemonade", Name: "qwen", Servable: true, ContextLength: 40960, EffectiveContextLength: 16384},
			{Host: "ollama", Name: "qwen", Servable: true, ContextLength: 32768},
			{Host: "lmstudio", Name: "qwen", Servable: true, ContextLength: 131072},
		}},
		{Family: "bare", Names: []wire.Alias{{Host: "ollama", Name: "bare", Servable: true}}},
	}}}
	r := New(remoteHost("far", "far.test:443", "", func(context.Context) remoteRouter { return fake }))
	if err := r.SurveyContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	families, _ := r.Models(false)
	got := map[string]Alias{}
	for _, f := range families {
		for _, a := range f.Names {
			got[a.Name] = a
		}
	}
	if a := got["qwen"]; a.Host != "far" || a.ContextLength != 40960 || a.EffectiveContextLength != 16384 {
		t.Fatalf("qwen alias %+v", a)
	}
	if a := got["bare"]; a.Host != "far" || a.ContextLength != 0 || a.EffectiveContextLength != 0 {
		t.Fatalf("bare alias %+v", a)
	}
}

// TestOfferRemoteErrorCountsNothing: a remote that fails Offer for a reason
// other than unknown_method is left out, as a server whose listing fails is.
func TestOfferRemoteErrorCountsNothing(t *testing.T) {
	failing := &failingRemote{fakeRemote: fakeRemote{models: wire.ModelsSnapshot{Models: []wire.Family{{Family: "qwen", Names: []wire.Alias{
		{Host: "ollama", Name: "qwen", Servable: true, ContextLength: 65536},
	}}}}}}
	r := New(remoteHost("far", "far.test:443", "", func(context.Context) remoteRouter { return failing }))
	if err := r.SurveyContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	offers, _, err := r.OfferContext(context.Background(), Request{Op: OpOffer, Placement: PlacementHosted})
	if err != nil {
		t.Fatal(err)
	}
	if len(offers) != 0 {
		t.Fatalf("a failing remote was counted: %+v", offers)
	}
}

func TestOfferRemoteTypedRefusalCountsNothing(t *testing.T) {
	for _, outcome := range []wire.OfferOutcome{wire.OfferOutcomeNotPermitted, wire.OfferOutcomeForbidden, wire.OfferOutcomeUnavailable, wire.OfferOutcomeInvalid} {
		t.Run(outcome.String(), func(t *testing.T) {
			// Even a malformed refusal carrying models must never enter the fold.
			fake := &fakeRemote{outcome: outcome, offers: []wire.Offer{{Family: "refused", Names: []string{"refused"}, Servers: []string{"remote"}}},
				models: wire.ModelsSnapshot{Models: []wire.Family{{Family: "listed", Names: []wire.Alias{{Host: "remote", Name: "listed", Servable: true}}}}}}
			r := New(remoteHost("far", "far.test:443", "", func(context.Context) remoteRouter { return fake }))
			if err := r.SurveyContext(context.Background()); err != nil {
				t.Fatal(err)
			}
			offers, _, err := r.OfferContext(context.Background(), Request{Op: OpOffer, Placement: PlacementHosted})
			if err != nil || len(offers) != 0 {
				t.Fatalf("refused remote counted: %+v %v", offers, err)
			}
		})
	}
}

// TestOfferRemoteUnknownSourceIsUnknown: a remote offer whose source is
// unknown with no alias named, or a source word this router has never heard,
// makes the local family's context unknown.
func TestOfferRemoteUnknownSourceIsUnknown(t *testing.T) {
	for _, source := range []wire.ContextSource{wire.ContextSourceUnknown, wire.ContextSource("measured-later")} {
		fake := &fakeRemote{offers: []wire.Offer{{Family: "qwen", Names: []string{"qwen"}, Servers: []string{"lemonade"},
			Context: 65536, ContextSource: source, Input: 61440}}}
		r := New(remoteHost("far", "far.test:443", "", func(context.Context) remoteRouter { return fake }))
		offers, _, err := r.OfferContext(context.Background(), Request{Op: OpOffer, Placement: PlacementAny, Reserve: 4096})
		if err != nil {
			t.Fatal(err)
		}
		want := []Offer{{Family: "qwen", Names: []string{"qwen"}, Servers: []string{"far/lemonade"}, ContextSource: ContextUnknown}}
		if !reflect.DeepEqual(offers, want) {
			t.Fatalf("source %q: offers %+v, want %+v", source, offers, want)
		}
	}
}

func boundSeen() listen.Seen { return listen.Seen{Bound: true, Path: "offer.test", User: "test"} }

type failingRemote struct{ fakeRemote }

func (f *failingRemote) Offer(wire.OfferRequest) (wire.OfferSnapshot, error) {
	return wire.OfferSnapshot{}, errors.New("transport closed")
}
