package router

import (
	"context"
	"slices"
	"sync"
	"testing"
)

type standingKey struct{}

// twoAccounts is a router with two hosted servers, each under its own key,
// both serving the same model and nothing local.
func twoAccounts(t *testing.T) *Router {
	t.Helper()
	first := newFakeHosted(t, "deepseek-chat")
	second := newFakeHosted(t, "deepseek-chat")
	applier := &fakeApplier{known: map[string]string{"work": hostedSecret, "home": hostedSecret}}
	r := New(NewHosted("work", first.server.URL+"/v1", WireOpenAICompatible, "work"),
		NewHosted("home", second.server.URL+"/v1", WireOpenAICompatible, "home"))
	r.UseCredentials(applier.apply)
	r.Survey()
	return r
}

// standings is a HostedStandingReader with fixed answers that records the
// hosts read and the caller each read carried.
type standings struct {
	mu     sync.Mutex
	answer map[string]HostedStanding
	read   []string
}

func (s *standings) reader(ctx context.Context, host string) HostedStanding {
	s.mu.Lock()
	defer s.mu.Unlock()
	caller, _ := ctx.Value(standingKey{}).(string)
	s.read = append(s.read, caller+" "+host)
	return s.answer[host]
}

func routeTwoAccounts(t *testing.T, r *Router, ctx context.Context) *Decision {
	t.Helper()
	allowed := []string{"work", "home"}
	d, _ := r.RouteFor(ctx, Request{Model: "deepseek-chat", Hosts: &allowed})
	if d.Verdict != Hosted {
		t.Fatalf("verdict %s: %s", d.Verdict, d.Why)
	}
	return d
}

// A caller already allowed on the second account is routed there; the first
// account, which would ask, is not taken.
func TestHostedPrefersTheAccountTheCallerMayUse(t *testing.T) {
	r := twoAccounts(t)
	s := &standings{answer: map[string]HostedStanding{"home": HostedPermitted}}
	r.UseHostedStanding(s.reader)
	ctx := context.WithValue(context.Background(), standingKey{}, "opencode")
	if d := routeTwoAccounts(t, r, ctx); d.Host != "home" {
		t.Fatalf("routed to %s, want the allowed account home", d.Host)
	}
	if !slices.Equal(s.read, []string{"opencode work", "opencode home"}) {
		t.Fatalf("standings read %q", s.read)
	}
}

// With neither account decided, the ordinary ranking stands.
func TestHostedUndecidedKeepsTheOrdinaryRanking(t *testing.T) {
	r := twoAccounts(t)
	r.UseHostedStanding((&standings{}).reader)
	if d := routeTwoAccounts(t, r, context.Background()); d.Host != "work" {
		t.Fatalf("routed to %s, want the first hosted server", d.Host)
	}
}

// A refused account comes after an undecided one.
func TestHostedRefusedComesLast(t *testing.T) {
	r := twoAccounts(t)
	r.UseHostedStanding((&standings{answer: map[string]HostedStanding{"work": HostedRefused}}).reader)
	if d := routeTwoAccounts(t, r, context.Background()); d.Host != "home" {
		t.Fatalf("routed to %s, want the undecided account home", d.Host)
	}
}

// One permitted hosted server reads no standing: the common route decides
// nothing extra. Route reads the standings with a context that carries no
// caller.
func TestHostedStandingIsReadOnlyWhenServersCompete(t *testing.T) {
	r := twoAccounts(t)
	s := &standings{answer: map[string]HostedStanding{"home": HostedPermitted}}
	r.UseHostedStanding(s.reader)
	only := []string{"work"}
	if d, _ := r.RouteFor(context.Background(), Request{Model: "deepseek-chat", Hosts: &only}); d.Host != "work" {
		t.Fatalf("routed to %s", d.Host)
	}
	both := []string{"work", "home"}
	if d, _ := r.Route(Request{Model: "deepseek-chat", Hosts: &both}); d.Host != "home" {
		t.Fatalf("Route routed to %s", d.Host)
	}
	if !slices.Equal(s.read, []string{" work", " home"}) {
		t.Fatalf("standings read %q", s.read)
	}
}

// A local server that has the model still comes before every hosted one.
func TestHostedStandingLeavesLocalFirst(t *testing.T) {
	r, _, _ := hostedMachine(t, "openrouter")
	s := &standings{answer: map[string]HostedStanding{"openrouter": HostedPermitted}}
	r.UseHostedStanding(s.reader)
	d, _ := r.RouteFor(context.Background(), Request{Model: "qwen/qwen3.6-35b-a3b"})
	if d.Verdict != Resident || d.Host == "openrouter" {
		t.Fatalf("verdict %s on %s", d.Verdict, d.Host)
	}
	if len(s.read) != 0 {
		t.Fatalf("standings read %q", s.read)
	}
}
