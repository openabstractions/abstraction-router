package router

import (
	"slices"
	"testing"

	"github.com/openabstractions/abstraction-identity/listen"
	content "github.com/openabstractions/abstraction-storage/go/abstraction/storage/content"
)

// recipeHost is a declared native host whose recipes name every file by hash,
// as a shipped declaration does. The engine source label is the provider's
// binding identity, as the runtime projects it.
func recipeHost(t *testing.T, models map[string][]string) (*Host, map[string][]HeldObject) {
	t.Helper()
	names := make([]string, 0, len(models))
	for model := range models {
		names = append(names, model)
	}
	slices.Sort(names)
	host, err := NewNative("lemonade-image", "fixture", listen.ServerExpectation{}, names, []string{ProfileImage})
	if err != nil {
		t.Fatal(err)
	}
	held := map[string][]HeldObject{}
	for _, model := range names {
		for _, role := range models[model] {
			digest := "sha256:" + model + "-" + role
			host.EngineObjects = append(host.EngineObjects, ModelObject{Digest: digest, Role: role,
				Engines: []ModelEngine{{Host: host.Name, Model: model, Engine: "sdcpp", Source: "provider-binding-v1:fixture", Role: role}}})
			held[model] = append(held[model], HeldObject{Store: "lemonade", Names: []string{model}, Digest: digest, Evidence: content.EvidenceHashed})
		}
	}
	return host, held
}

// offered is every name the image offer projection lists for host.
func offered(t *testing.T, routes *Router, host string) []string {
	t.Helper()
	routes.Survey()
	offers, _, err := routes.Offer(Request{Profile: ProfileImage, Placement: PlacementLocal})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, offer := range offers {
		if slices.Contains(offer.Servers, host) {
			out = append(out, offer.Names...)
		}
	}
	slices.Sort(out)
	return out
}

// A declared host whose hash-named recipes are none installed has no engine
// here: it is listed unavailable and offers nothing. Each recipe whose every
// file storage holds with hashed evidence is offered, and only those.
func TestNativeRecipeRequiresEveryHeldHashedDigestWithoutRestart(t *testing.T) {
	const klein, turbo = "user.LocalApprovedKlein", "user.Z-Image-Turbo"
	host, held := recipeHost(t, map[string][]string{klein: {"main", "text_encoder", "vae"}, turbo: {"main", "text_encoder", "vae"}})
	routes := New(host)
	state := func() HostState {
		t.Helper()
		states, _, _, _, _ := routes.Residency(false)
		if len(states) != 1 {
			t.Fatalf("states = %+v", states)
		}
		return states[0]
	}
	if got := offered(t, routes, host.Name); len(got) != 0 {
		t.Fatalf("a host with nothing installed offered %v", got)
	}
	if s := state(); s.Up || s.Servable || s.Installed != 0 || s.Why != "engine:not_installed:lemonade-image" || s.AvailabilityCause != AvailabilityConfiguration {
		t.Fatalf("uninstalled host state = %+v", s)
	}
	if decision, _ := routes.Route(Request{Model: klein, Profile: ProfileImage}); decision.Verdict != NotOnDisk {
		t.Fatalf("route to an uninstalled model = %s: %s", decision.Verdict, decision.Why)
	}
	// Two of three files are not an installed model.
	routes.SetHeld(held[klein][:2])
	if got := offered(t, routes, host.Name); len(got) != 0 {
		t.Fatalf("a partial recipe offered %v", got)
	}
	// One installed model: only it is offered and routed.
	routes.SetHeld(held[klein])
	if got := offered(t, routes, host.Name); !slices.Equal(got, []string{klein}) {
		t.Fatalf("offered %v, want %s alone", got, klein)
	}
	if s := state(); !s.Up || !s.Servable || s.Installed != 1 || s.Why == "engine:not_installed:lemonade-image" {
		t.Fatalf("installed host state = %+v", s)
	}
	if decision, _ := routes.Route(Request{Model: klein, Profile: ProfileImage}); decision.Verdict != WouldLoad {
		t.Fatalf("route to the installed model = %s: %s", decision.Verdict, decision.Why)
	}
	if decision, _ := routes.Route(Request{Model: turbo, Profile: ProfileImage}); decision.Verdict != NotOnDisk {
		t.Fatalf("route to the other model = %s: %s", decision.Verdict, decision.Why)
	}
	// Named, foreign or mismatched evidence withdraws the model.
	for _, evidence := range []content.Evidence{content.EvidenceNamed, content.EvidenceForeign} {
		changed := slices.Clone(held[klein])
		changed[2].Evidence = evidence
		routes.SetHeld(changed)
		if got := offered(t, routes, host.Name); len(got) != 0 {
			t.Fatalf("%s evidence offered %v", evidence, got)
		}
	}
	mismatch := slices.Clone(held[klein])
	mismatch[2].Digest = "sha256:wrong"
	routes.SetHeld(mismatch)
	if got := offered(t, routes, host.Name); len(got) != 0 || state().Up {
		t.Fatalf("a hash mismatch kept %v offered", got)
	}
	routes.SetHeld(append(slices.Clone(held[klein]), held[turbo]...))
	if got := offered(t, routes, host.Name); !slices.Equal(got, []string{klein, turbo}) {
		t.Fatalf("offered %v, want both", got)
	}
}

// A recipe that names inventory objects keeps its owner's account of
// installation: the model host's chat models stay offered with no held
// hashed evidence.
func TestInventoryObjectRecipesAreUnchanged(t *testing.T) {
	const model = "qwen3-8b"
	host, err := NewNative("modelhost", "fixture", listen.ServerExpectation{}, []string{model}, []string{ProfileChat})
	if err != nil {
		t.Fatal(err)
	}
	host.EngineObjects = []ModelObject{{Store: "ollama", Object: "blob-1", Role: "main",
		Engines: []ModelEngine{{Host: host.Name, Model: model, Engine: "llama.cpp", Source: "provider-binding-v1:fixture", Role: "main"}}}}
	routes := New(host)
	routes.Survey()
	offers, _, err := routes.Offer(Request{Profile: ProfileChat, Placement: PlacementLocal})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, offer := range offers {
		found = found || slices.Contains(offer.Names, model)
	}
	states, _, _, _, _ := routes.Residency(false)
	if !found || len(states) != 1 || !states[0].Up || states[0].Installed != 1 {
		t.Fatalf("model host offers %+v, states %+v", offers, states)
	}
}
