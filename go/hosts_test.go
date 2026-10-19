package router

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestNewLocalPreservesProtocolConstructors(t *testing.T) {
	constructors := map[string]func(string) *Host{"lemonade": Lemonade, "lmstudio": LMStudio, "ollama": Ollama, "docker-model-runner": DockerModelRunner, "foundry-local": FoundryLocal, "whispercpp": WhisperCPP, "piper": Piper, "comfyui": func(base string) *Host { return ComfyUI(base) }, "swarmui": SwarmUI}
	for kind, constructor := range constructors {
		want := constructor("http://127.0.0.1:1234/")
		want.Profiles = nil // previous local declaration constructor behavior
		got, err := NewLocal(kind, "http://127.0.0.1:1234/", nil)
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != want.Name || got.Base != want.Base || got.Chat != want.Chat || got.Wire != want.Wire || got.Hosted != want.Hosted || got.Credential != want.Credential || !slices.Equal(got.Profiles, want.Profiles) || got.windowAtLoad != want.windowAtLoad || (got.resident == nil) != (want.resident == nil) {
			t.Fatalf("%s constructor changed", kind)
		}
	}
	if _, err := NewLocal("unsupported", "http://127.0.0.1:1234", nil); err == nil {
		t.Fatal("unsupported kind accepted")
	}
}

func TestComfyUIOffersCheckpointNamesAndReadyAliases(t *testing.T) {
	base := serve(t, map[string]string{
		"/models":                  `["checkpoints","diffusion_models","text_encoders","vae"]`,
		"/models/checkpoints":      `["nested/complete.safetensors","plain.ckpt"]`,
		"/models/diffusion_models": `["part.safetensors"]`,
		"/models/text_encoders":    `["encoder.safetensors"]`,
		"/models/vae":              `["vae.safetensors"]`,
	})
	ready := []string{"admitted-recipe"}
	key := struct{}{}
	ctx := context.WithValue(context.Background(), key, "survey")
	calls := 0
	h := ComfyUI(base, ComfyRecipeAliases(func(got context.Context) ([]string, error) {
		calls++
		if got.Value(key) != "survey" {
			t.Fatal("callback lost listing context")
		}
		return ready, nil
	}))
	got, err := h.installed(ctx, h)
	if err != nil || !slices.Equal(got, []string{"admitted-recipe", "nested/complete.safetensors", "plain.ckpt"}) {
		t.Fatalf("installed %v, %v", got, err)
	}
	ready = nil
	got, err = h.installed(ctx, h)
	if err != nil || calls != 2 || !slices.Equal(got, []string{"nested/complete.safetensors", "plain.ckpt"}) {
		t.Fatalf("readiness refresh %v, %v, calls %d", got, err, calls)
	}
	legacy := ComfyUI(base)
	got, err = legacy.installed(ctx, legacy)
	if err != nil || !slices.Equal(got, []string{"nested/complete.safetensors", "plain.ckpt"}) {
		t.Fatalf("default checkpoint loader names %v, %v", got, err)
	}
}

func TestComfyUIReadyAliasFailureAndAmbiguityRefuse(t *testing.T) {
	base := serve(t, map[string]string{"/models": `["checkpoints"]`, "/models/checkpoints": `["same.ckpt"]`})
	for _, aliases := range [][]string{{""}, {"same.ckpt"}, {"duplicate", "duplicate"}, {"SAME.CKPT"}, {"duplicate", "DUPlICATE"}} {
		h := ComfyUI(base, ComfyRecipeAliases(func(context.Context) ([]string, error) { return aliases, nil }))
		if got, err := h.installed(context.Background(), h); err == nil || got != nil {
			t.Fatalf("ambiguous aliases %v yielded %v, %v", aliases, got, err)
		}
	}
	want := errors.New("readiness unavailable")
	h := ComfyUI(base, ComfyRecipeAliases(func(context.Context) ([]string, error) { return nil, want }))
	if got, err := h.installed(context.Background(), h); !errors.Is(err, want) || got != nil {
		t.Fatalf("readiness failure yielded %v, %v", got, err)
	}
}

func TestComfyUIRecipeAliasRoutesWithoutCheckpointOrComponentOffer(t *testing.T) {
	base := serve(t, map[string]string{
		"/models":       `["diffusion_models","text_encoders","vae"]`,
		"/system_stats": `{"devices":[]}`,
	})
	r := New(ComfyUI(base, ComfyRecipeAliases(func(context.Context) ([]string, error) {
		return []string{"admitted-recipe"}, nil
	})))
	r.Survey()
	d, _ := r.Route(Request{Model: "admitted-recipe", Profile: ProfileImage})
	if d.Verdict != WouldLoad || d.Host != "comfyui" {
		t.Fatalf("admitted recipe route %+v", d)
	}
}

// A Lemonade host with no /api/v1/health route still has its installed
// list: the two reads are independent, so the models read does not get
// discarded for a residency read that fails. Resident stays unset and Why
// names the missing read, rather than the whole host reading as down.
func TestLemonadeInstalledStandsWithoutHealth(t *testing.T) {
	lemonade := serve(t, map[string]string{
		"/api/v1/models": `{"data":[{"id":"Qwen3.6-35B-A3B-GGUF","checkpoint":"unsloth/Qwen3.6-35B-A3B-GGUF:Qwen3.6-35B-A3B-UD-Q4_K_XL.gguf","downloaded":true}]}`,
	})
	r := New(Lemonade(lemonade))
	r.Survey()
	hosts, _, _, _, _ := r.Residency(false)
	if len(hosts) != 1 {
		t.Fatalf("hosts %+v", hosts)
	}
	h := hosts[0]
	if !h.Up {
		t.Fatalf("a host whose installed list was read should read up: %+v", h)
	}
	if h.Installed != 2 {
		t.Fatalf("installed %d, want 2 (the id and its checkpoint)", h.Installed)
	}
	if h.Resident != nil {
		t.Fatalf("resident should be unknown, not read: %+v", h.Resident)
	}
	if !strings.HasPrefix(h.Why, "health:") {
		t.Fatalf("why %q, want a health: reason naming the missing residency read", h.Why)
	}

	fams, _ := r.Models(false)
	var found bool
	for _, f := range fams {
		for _, n := range f.Names {
			if n.Name == "Qwen3.6-35B-A3B-GGUF" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("the installed alias should still be catalogued: %+v", fams)
	}
}
