package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/openabstractions/abstraction-identity/listen"
)

const hostedSecret = "sk-or-FIXTURE-7c1e55a0"

// fakeHosted is a hosted provider that records the headers of every request.
type fakeHosted struct {
	server *httptest.Server
	mu     sync.Mutex
	seen   []http.Header
}

func newFakeHosted(t *testing.T, models ...string) *fakeHosted {
	t.Helper()
	f := &fakeHosted{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.seen = append(f.seen, r.Header.Clone())
		f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+hostedSecret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var d struct {
			Data []map[string]string `json:"data"`
		}
		for _, m := range models {
			d.Data = append(d.Data, map[string]string{"id": m})
		}
		json.NewEncoder(w).Encode(d)
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeHosted) headers() []http.Header {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]http.Header(nil), f.seen...)
}

// fakeApplier stands in for abstraction.credentials applier@1 and records uses.
type fakeApplier struct {
	mu    sync.Mutex
	known map[string]string
	uses  []string
}

func (a *fakeApplier) apply(_ context.Context, consumer, name, target string) (map[string]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.uses = append(a.uses, consumer+" "+name+" "+target)
	secret, ok := a.known[name]
	if !ok {
		return nil, &CredentialRefusal{Outcome: "unknown", Name: name}
	}
	return map[string]string{"Authorization": "Bearer " + secret}, nil
}

// hostedMachine is the fixture machine plus one hosted host listing a family
// Lemonade holds, a family nobody local has, and one LM Studio would load.
func hostedMachine(t *testing.T, credential string) (*Router, *fakeHosted, *fakeApplier) {
	t.Helper()
	local := machine(t)
	f := newFakeHosted(t, "qwen/qwen3.6-35b-a3b", "anthropic/claude-sonnet-5", "google/gemma-4-26b-it")
	applier := &fakeApplier{known: map[string]string{"openrouter": hostedSecret}}
	r := New(append(local.hosts, NewHosted("openrouter", f.server.URL+"/api/v1", WireOpenAICompatible, credential))...)
	r.UseCredentials(applier.apply)
	r.Survey()
	return r, f, applier
}

func TestHostedListingAppliesTheCredentialOncePerRequest(t *testing.T) {
	r, f, applier := hostedMachine(t, "openrouter")
	r.Survey()
	seen := f.headers()
	if len(seen) != 2 {
		t.Fatalf("two surveys sent %d listing requests", len(seen))
	}
	for _, h := range seen {
		if got := h.Values("Authorization"); len(got) != 1 || got[0] != "Bearer "+hostedSecret {
			t.Fatalf("listing request carried Authorization %q", got)
		}
	}
	applier.mu.Lock()
	uses := append([]string(nil), applier.uses...)
	applier.mu.Unlock()
	target := Target(f.server.URL)
	if len(uses) != 2 || uses[0] != CredentialConsumer+" openrouter "+target {
		t.Fatalf("applier uses %q, want one per listing request as %s", uses, CredentialConsumer)
	}
	hosts, _, _, _, _ := r.Residency(false)
	for _, h := range hosts {
		if h.Host == "openrouter" && (!h.Up || !h.Hosted || h.Wire != WireOpenAICompatible || h.Credential != "openrouter" || h.Installed != 3) {
			t.Fatalf("hosted host state %+v", h)
		}
	}
}

func TestPickPrefersResidentOverHosted(t *testing.T) {
	r, _, _ := hostedMachine(t, "openrouter")
	d, _ := r.Route(Request{Model: "qwen/qwen3.6-35b-a3b"})
	if d.Verdict != Resident || d.Host != "lemonade" {
		t.Fatalf("a model Lemonade holds went to %+v", d)
	}
	d, _ = r.Route(Request{Model: "gemma-4-26b-it"})
	if d.Verdict != WouldLoad || d.Host != "lmstudio" {
		t.Fatalf("a model LM Studio has on disk went to %+v", d)
	}
	d, _ = r.Route(Request{Model: "anthropic/claude-sonnet-5"})
	if d.Verdict != Hosted || d.Host != "openrouter" || d.Endpoint != "" || d.Loads != 0 {
		t.Fatalf("a model only the hosted host lists: %+v", d)
	}
}

func TestAnAllowanceNamingOnlyTheHostedHostYieldsHosted(t *testing.T) {
	r, _, _ := hostedMachine(t, "openrouter")
	only := []string{"openrouter"}
	d, _ := r.Route(Request{Model: "qwen/qwen3.6-35b-a3b", Hosts: &only})
	if d.Verdict != Hosted || d.Host != "openrouter" || d.Endpoint != "" {
		t.Fatalf("allowance [openrouter] for a resident family: %+v", d)
	}
	if len(d.Withheld) == 0 {
		t.Fatalf("the resident local host was not named as withheld: %+v", d)
	}
}

func TestSnapshotsCarryNoHeaderValue(t *testing.T) {
	r, _, _ := hostedMachine(t, "openrouter")
	caller := listen.Seen{Bound: true, User: "fixture", Path: "/fixture"}
	for _, op := range []string{OpModels, OpResidency, OpRoute} {
		raw, err := json.Marshal(r.Answer(Request{Op: op, Model: "anthropic/claude-sonnet-5"}, caller))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), hostedSecret) || strings.Contains(string(raw), "Bearer") {
			t.Fatalf("%s answer carries a header value: %s", op, raw)
		}
		if op == OpModels && !strings.Contains(string(raw), `"hosted":true`) {
			t.Fatalf("models answer does not mark hosted aliases: %s", raw)
		}
	}
}

func TestAHostWhoseCredentialIsUnknownIsListedDown(t *testing.T) {
	r, f, _ := hostedMachine(t, "missing")
	if n := len(f.headers()); n != 0 {
		t.Fatalf("a refused credential still sent %d listing requests", n)
	}
	hosts, _, _, _, _ := r.Residency(false)
	for _, h := range hosts {
		if h.Host != "openrouter" {
			continue
		}
		if h.Up || h.Why != "credential:unknown:missing" {
			t.Fatalf("host with an unknown credential: %+v", h)
		}
		return
	}
	t.Fatal("hosted host missing from residency")
}

func TestAnUnknownWireIsListedDown(t *testing.T) {
	r := New(NewHosted("elsewhere", "https://example.invalid", "example/grpc@1", ""))
	r.Survey()
	hosts, _, _, _, _ := r.Residency(false)
	if len(hosts) != 1 || hosts[0].Up || hosts[0].Why != "unsupported_wire:example/grpc@1" {
		t.Fatalf("unknown wire: %+v", hosts)
	}
}
