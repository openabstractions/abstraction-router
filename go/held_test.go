package router

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// hostWithModels is one openai-compatible host serving a fixed model list.
func hostWithModels(t *testing.T, ids ...string) string {
	t.Helper()
	body := `{"data":[`
	for i, id := range ids {
		if i > 0 {
			body += ","
		}
		body += `{"id":"` + id + `","type":"llm","state":"not-loaded"}`
	}
	body += `]}`
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v0/models", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(body)) })
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s.URL
}

func familyOf(families []Family, name string) (Family, bool) {
	for _, f := range families {
		if f.Family == name {
			return f, true
		}
	}
	return Family{}, false
}

// A store holding what a host serves names that host's family and its exact
// alias; a store holding what no host serves becomes a family of its own,
// unservable and never resident.
func TestHeldStoresNameServedAndUnservedFamilies(t *testing.T) {
	r := New(LMStudio(hostWithModels(t, "cygnal/smollm2-135m-instruct-gguf/smollm2-135m-instruct-q2_k.gguf")))
	r.SetHeld([]HeldObject{
		{Store: "lmstudio", Names: []string{"cygnal/smollm2-135m-instruct-gguf/smollm2-135m-instruct-q2_k.gguf"}},
		{Store: "huggingface", Names: []string{"models--unsloth--SmolLM2-135M-Instruct-GGUF/SmolLM2-135M-Instruct-Q2_K.gguf"}},
		{Store: "comfyui-amd", Names: []string{"diffusion_models/wan2.2_ti2v_5B_fp16.safetensors"}},
	})
	r.Survey()
	families, _ := r.Models(false)

	smol, ok := familyOf(families, "smollm2-135m")
	if !ok {
		t.Fatalf("families %+v", families)
	}
	if !slices.Equal(smol.HeldIn, []string{"huggingface", "lmstudio"}) {
		t.Fatalf("family held_in %v", smol.HeldIn)
	}
	if len(smol.Names) != 1 || smol.Names[0].Host == "" || !slices.Equal(smol.Names[0].HeldIn, []string{"lmstudio"}) {
		t.Fatalf("alias %+v", smol.Names)
	}

	wan, ok := familyOf(families, "wan2.2-5b-ti2v")
	if !ok {
		t.Fatalf("a held model no host serves is missing: %+v", families)
	}
	if !slices.Equal(wan.HeldIn, []string{"comfyui-amd"}) {
		t.Fatalf("unserved family held_in %v", wan.HeldIn)
	}
	if len(wan.Names) != 1 || wan.Names[0].Host != "" || wan.Names[0].Servable || wan.Names[0].Resident {
		t.Fatalf("unserved alias %+v", wan.Names[0])
	}
	if !slices.Equal(wan.Names[0].HeldIn, []string{"comfyui-amd"}) {
		t.Fatalf("unserved alias held_in %v", wan.Names[0].HeldIn)
	}
}

// An object no store index names reaches a family through the digest another
// store published for the same content.
func TestAnUnnamedObjectJoinsItsFamilyByDigest(t *testing.T) {
	const digest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	r := New(LMStudio(hostWithModels(t, "qwen/qwen3.6-35b-a3b")))
	r.SetHeld([]HeldObject{
		{Store: "lmstudio", Names: []string{"qwen/qwen3.6-35b-a3b"}, Digest: digest},
		{Store: "ollama", Digest: digest},
		{Store: "ollama", Digest: "sha256:2222222222222222222222222222222222222222222222222222222222222222"},
	})
	r.Survey()
	families, _ := r.Models(false)
	qwen, ok := familyOf(families, "qwen3.6-35b-a3b")
	if !ok {
		t.Fatalf("families %+v", families)
	}
	if !slices.Equal(qwen.HeldIn, []string{"lmstudio", "ollama"}) {
		t.Fatalf("held_in %v", qwen.HeldIn)
	}
	// The unmatched digest names no family, so it adds nothing.
	if len(families) != 1 {
		t.Fatalf("an unmatched object invented a family: %+v", families)
	}
}

// The family is what the store's descriptor says it is. The host's alias here
// parses to "smollm2-135m"; the store declares the model to be "smollm2", and
// the router reports the store's word, marked as coming from a descriptor.
func TestAFamilyComesFromTheDescriptorNotTheAlias(t *testing.T) {
	const alias = "cygnal/smollm2-135m-instruct-gguf/smollm2-135m-instruct-q2_k.gguf"
	r := New(LMStudio(hostWithModels(t, alias)))
	r.SetHeld([]HeldObject{{
		Store: "lmstudio", Names: []string{alias},
		Descriptor: map[string]string{"family": "smollm2", "quant": "q2_k", "format": "gguf",
			"architecture": "llama", "size_label": "135M", "source": "lmstudio"},
	}})
	r.Survey()
	families, _ := r.Models(false)
	if len(families) != 1 {
		t.Fatalf("families %+v", families)
	}
	f := families[0]
	if f.Family != "smollm2" || f.FamilySource != FromDescriptor {
		t.Fatalf("family %q from %q", f.Family, f.FamilySource)
	}
	if len(f.Names) != 1 || f.Names[0].Name != alias || f.Names[0].Host == "" {
		t.Fatalf("names %+v", f.Names)
	}
	// A request naming what the host serves reaches the family the descriptor
	// gave it, so Pick answers the same as before the rename.
	d, _ := r.Route(Request{Model: alias})
	if d.Family != "smollm2" || d.Verdict != WouldLoad {
		t.Fatalf("decision %+v", d)
	}
}

// A store's longer name reaches a host's shorter alias by the store's own
// naming: Ollama's inventory calls the tag library/tiny:1b and its server
// calls it tiny:1b. The same suffix rule that resolves the family also
// resolves the alias's own held_in: they are one matching function, so the
// alias itself is found in the store that names it, not just the family.
func TestAStoresPathReachesAHostsShorterAlias(t *testing.T) {
	r := New(LMStudio(hostWithModels(t, "tiny:1b")))
	r.SetHeld([]HeldObject{{
		Store: "ollama", Names: []string{"library/tiny:1b"},
		Descriptor: map[string]string{"family": "tinyllama", "format": "gguf", "source": "ollama"},
	}})
	r.Survey()
	families, _ := r.Models(false)
	f, ok := familyOf(families, "tinyllama")
	if !ok {
		t.Fatalf("families %+v", families)
	}
	if f.FamilySource != FromDescriptor || len(f.Names) != 1 || f.Names[0].Host == "" {
		t.Fatalf("family %+v", f)
	}
	if !slices.Equal(f.Names[0].HeldIn, []string{"ollama"}) {
		t.Fatalf("alias held_in %v, want the suffix match to reach ollama's library/tiny:1b", f.Names[0].HeldIn)
	}
}

// ComfyUI's server lists a model under exactly the path its own store uses
// (folder/file, tree.Observe's name): an exact match needs no suffix, and
// the alias's held_in reaches it through the same key as the family would.
func TestAComfyUIAliasMatchesItsStoreNameExactly(t *testing.T) {
	comfy := serve(t, map[string]string{
		"/models":                  `["diffusion_models"]`,
		"/models/diffusion_models": `["wan2.2_ti2v_5B_fp16.safetensors"]`,
	})
	r := New(ComfyUI(comfy))
	r.SetHeld([]HeldObject{{
		Store: "comfyui", Names: []string{"diffusion_models/wan2.2_ti2v_5B_fp16.safetensors"},
		Descriptor: map[string]string{"family": "wan2.2-5b-ti2v", "format": "safetensors", "source": "comfyui"},
	}})
	r.Survey()
	families, _ := r.Models(false)
	f, ok := familyOf(families, "wan2.2-5b-ti2v")
	if !ok {
		t.Fatalf("families %+v", families)
	}
	if len(f.Names) != 1 || f.Names[0].Name != "diffusion_models/wan2.2_ti2v_5B_fp16.safetensors" {
		t.Fatalf("names %+v", f.Names)
	}
	if !slices.Equal(f.Names[0].HeldIn, []string{"comfyui"}) {
		t.Fatalf("alias held_in %v", f.Names[0].HeldIn)
	}
}

// Lemonade's own checkpoint field is the Hugging Face store's exact name,
// owner/repo:file, so it matches without a suffix; Lemonade's shorter id
// names no path the store shares, so it stays an alias of its own rather
// than a guess at which held object it means.
func TestALemonadeCheckpointMatchesItsHuggingFaceStoreNameExactly(t *testing.T) {
	lemonade := serve(t, map[string]string{
		"/api/v1/models": `{"data":[{"id":"Qwen3.6-35B-A3B-GGUF","checkpoint":"unsloth/Qwen3.6-35B-A3B-GGUF:Qwen3.6-35B-A3B-UD-Q4_K_XL.gguf","downloaded":true}]}`,
		"/api/v1/health": `{"all_models_loaded":[]}`,
	})
	r := New(Lemonade(lemonade))
	r.SetHeld([]HeldObject{{
		Store: "huggingface", Names: []string{"unsloth/Qwen3.6-35B-A3B-GGUF:Qwen3.6-35B-A3B-UD-Q4_K_XL.gguf"},
		Descriptor: map[string]string{"family": "qwen3.6-35b-a3b", "format": "gguf", "source": "huggingface"},
	}})
	r.Survey()
	families, _ := r.Models(false)
	f, ok := familyOf(families, "qwen3.6-35b-a3b")
	if !ok {
		t.Fatalf("families %+v", families)
	}
	var checkpoint, id Alias
	for _, a := range f.Names {
		switch a.Name {
		case "unsloth/Qwen3.6-35B-A3B-GGUF:Qwen3.6-35B-A3B-UD-Q4_K_XL.gguf":
			checkpoint = a
		case "Qwen3.6-35B-A3B-GGUF":
			id = a
		}
	}
	if !slices.Equal(checkpoint.HeldIn, []string{"huggingface"}) {
		t.Fatalf("checkpoint held_in %v", checkpoint.HeldIn)
	}
	if id.Name == "" {
		t.Fatalf("lemonade's id alias is missing: %+v", f.Names)
	}
	if len(id.HeldIn) != 0 {
		t.Fatalf("lemonade's bare id shares no path with the store name, so it should not guess a held_in: %v", id.HeldIn)
	}
}

// Two stores that name two different objects with the same file name after
// their own leading path names neither through the shared suffix: the key
// is contested, exactly as a contested family is, so a person who happens to
// have two same-named files in two stores is told nothing rather than shown
// a guess.
func TestATwoStoresSharingAFileNameContestTheSuffix(t *testing.T) {
	p := index([]HeldObject{
		{Store: "comfyui", Names: []string{"checkpoints/model.safetensors"}},
		{Store: "stabilitymatrix", Names: []string{"other/model.safetensors"}},
	})
	if got := stores(p.byName["model.safetensors"]); got != nil {
		t.Fatalf("contested suffix key held_in %v, want none", got)
	}
	// Each store's own full name is unambiguous and still resolves.
	if got := stores(p.byName["checkpoints/model.safetensors"]); !slices.Equal(got, []string{"comfyui"}) {
		t.Fatalf("comfyui's own name %v", got)
	}
	if got := stores(p.byName["other/model.safetensors"]); !slices.Equal(got, []string{"stabilitymatrix"}) {
		t.Fatalf("stabilitymatrix's own name %v", got)
	}
}

// Two stores that describe one name as two models name nothing: the router
// keeps the host's alias rather than picking a winner.
func TestAContestedNameFallsBackToTheAlias(t *testing.T) {
	r := New(LMStudio(hostWithModels(t, "acme/tiny.gguf")))
	r.SetHeld([]HeldObject{
		{Store: "lmstudio", Names: []string{"acme/tiny.gguf"}, Descriptor: map[string]string{"family": "one"}},
		{Store: "comfyui", Names: []string{"acme/tiny.gguf"}, Descriptor: map[string]string{"family": "two"}},
	})
	r.Survey()
	families, _ := r.Models(false)
	f, ok := familyOf(families, "tiny")
	if !ok {
		t.Fatalf("the contested name did not fall back to its alias: %+v", families)
	}
	if f.FamilySource != FromAlias || f.Names[0].Host == "" {
		t.Fatalf("family %+v", f)
	}
}

// A host whose models no descriptor names keeps its aliases as families and
// says so, which is what every host on a machine with no storage inventory
// does.
func TestWithoutDescriptorsEveryFamilyIsAnAlias(t *testing.T) {
	r := New(LMStudio(hostWithModels(t, "qwen/qwen3.6-35b-a3b")))
	r.Survey()
	families, _ := r.Models(false)
	f, ok := familyOf(families, "qwen3.6-35b-a3b")
	if !ok {
		t.Fatalf("families %+v", families)
	}
	if f.FamilySource != FromAlias {
		t.Fatalf("family_source %q without an inventory", f.FamilySource)
	}
	// A store that holds the model and describes nothing leaves it an alias.
	r.SetHeld([]HeldObject{{Store: "lmstudio", Names: []string{"qwen/qwen3.6-35b-a3b"}}})
	r.Survey()
	families, _ = r.Models(false)
	f, _ = familyOf(families, "qwen3.6-35b-a3b")
	if f.FamilySource != FromAlias || len(f.HeldIn) != 1 {
		t.Fatalf("family %+v", f)
	}
}

// A store's own model no host serves is a family of the descriptor's naming.
func TestAHeldModelNoHostServesTakesItsDescriptorsFamily(t *testing.T) {
	r := New(LMStudio(hostWithModels(t)))
	r.SetHeld([]HeldObject{{
		Store: "comfyui-amd", Names: []string{"diffusion_models/wan2.2_ti2v_5B_fp16.safetensors"},
		Descriptor: map[string]string{"family": "wan2.2-ti2v-5b", "format": "safetensors", "source": "comfyui"},
	}})
	r.Survey()
	families, _ := r.Models(false)
	f, ok := familyOf(families, "wan2.2-ti2v-5b")
	if !ok {
		t.Fatalf("families %+v", families)
	}
	if f.FamilySource != FromDescriptor || f.Names[0].Host != "" || f.Names[0].Servable {
		t.Fatalf("family %+v", f)
	}
}

// A projector's own descriptor names its base family; the router folds it
// into that family's Components rather than listing it as a family of its
// own (abstraction.model/descriptor@1 MODEL-C3).
func TestComponentFoldsIntoItsBaseFamily(t *testing.T) {
	r := New(LMStudio(hostWithModels(t)))
	r.SetHeld([]HeldObject{
		{Store: "comfyui", Names: []string{"qwen3.6-vl-dev.gguf"},
			Descriptor: map[string]string{"family": "qwen3.6-vl-dev", "format": "gguf", "source": "comfyui"}},
		{Store: "comfyui", Names: []string{"mmproj-qwen3.6-vl-dev.gguf"},
			Descriptor: map[string]string{"family": "qwen3.6-vl-dev-mmproj", "base": "qwen3.6-vl-dev",
				"role": "projector", "format": "gguf", "source": "comfyui"}},
	})
	r.Survey()
	families, _ := r.Models(false)

	if _, ok := familyOf(families, "qwen3.6-vl-dev-mmproj"); ok {
		t.Fatalf("a component appeared as a family of its own: %+v", families)
	}
	f, ok := familyOf(families, "qwen3.6-vl-dev")
	if !ok {
		t.Fatalf("families %+v", families)
	}
	if !slices.Equal(f.HeldIn, []string{"comfyui"}) {
		t.Fatalf("held_in %v", f.HeldIn)
	}
	if len(f.Components) != 1 || f.Components[0] != (Component{Store: "comfyui", Role: "projector", Name: "mmproj-qwen3.6-vl-dev.gguf"}) {
		t.Fatalf("components %+v", f.Components)
	}
}

// A VAE whose own name derives no base still names the family it belongs to
// when the family_source stays honest about it: family_source reads alias,
// as it does for any name no descriptor could confirm, because a component
// is not a family a store declared (MODEL-C3).
func TestComponentFallsBackToItsOwnFamilyWhenBaseCannotBeDerived(t *testing.T) {
	r := New(LMStudio(hostWithModels(t)))
	r.SetHeld([]HeldObject{{
		Store: "comfyui", Names: []string{"vae-ft-mse-840000-ema-pruned.safetensors"},
		Descriptor: map[string]string{"family": "vae-ft-mse-840000-ema-pruned", "role": "vae",
			"format": "safetensors", "source": "comfyui"},
	}})
	r.Survey()
	families, _ := r.Models(false)
	f, ok := familyOf(families, "vae-ft-mse-840000-ema-pruned")
	if !ok {
		t.Fatalf("families %+v", families)
	}
	if f.FamilySource != FromAlias {
		t.Fatalf("family_source %q, want alias for a component whose base could not be derived", f.FamilySource)
	}
	if len(f.Components) != 1 || f.Components[0] != (Component{Store: "comfyui", Role: "vae", Name: "vae-ft-mse-840000-ema-pruned.safetensors"}) {
		t.Fatalf("components %+v", f.Components)
	}
}

// A plain weights object carries no role, or role weights, and is unaffected
// by component folding: existing behaviour is unchanged.
func TestAPlainWeightsRoleIsNotAComponent(t *testing.T) {
	r := New(LMStudio(hostWithModels(t)))
	r.SetHeld([]HeldObject{{
		Store: "comfyui", Names: []string{"wan2.2.safetensors"},
		Descriptor: map[string]string{"family": "wan2.2", "role": "weights", "format": "safetensors", "source": "comfyui"},
	}})
	r.Survey()
	families, _ := r.Models(false)
	f, ok := familyOf(families, "wan2.2")
	if !ok {
		t.Fatalf("families %+v", families)
	}
	if f.FamilySource != FromDescriptor || len(f.Components) != 0 {
		t.Fatalf("family %+v", f)
	}
}

// A router told nothing keeps the catalogue its hosts alone produce, and an
// unservable name never reaches a routing decision.
func TestProvenanceIsAdditiveAndNeverRouted(t *testing.T) {
	r := New(LMStudio(hostWithModels(t, "qwen/qwen3.6-35b-a3b")))
	r.Survey()
	before, _ := r.Models(false)
	for _, f := range before {
		if f.HeldIn != nil {
			t.Fatalf("held_in without an inventory: %+v", f)
		}
	}
	r.SetHeld([]HeldObject{{Store: "comfyui-amd", Names: []string{"diffusion_models/wan2.2_ti2v_5B_fp16.safetensors"}}})
	r.Survey()
	d, _ := r.Route(Request{Model: "wan2.2_ti2v_5B_fp16.safetensors"})
	if d.Verdict != NotOnDisk || len(d.InstalledOn) != 0 {
		t.Fatalf("decision %+v", d)
	}
}
