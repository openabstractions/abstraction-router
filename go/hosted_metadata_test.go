package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// DeepSeek's GET /models reports each model's context window and output
// media (api-docs.deepseek.com, read 2026-11-08). OpenCode planned on the
// 32768-token default before the listing was read, and a DeepSeek server
// offered embeddings, speech and images it does not have (R10, 2026-11-08).
func TestHostedListingReportsContextAndMedia(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"object":"list","data":[
{"id":"deepseek-v4-pro","object":"model","owned_by":"deepseek","context_window":1048576,"max_output_tokens":393216,"input_modalities":["text"],"output_modalities":["text"]},
{"id":"router-model","object":"model","context_length":200000},
{"id":"bare-model","object":"model"}]}`))
	}))
	defer upstream.Close()
	r := New(NewHosted("cloud", upstream.URL, WireOpenAICompatible, "cloudcred"))
	r.UseCredentials(func(context.Context, string, string, string) (map[string]string, error) { return nil, nil })
	r.Survey()
	contexts := map[string]Offer{}
	for _, profile := range []string{ProfileChat, ProfileEmbed} {
		offers, _, err := r.Offer(Request{Op: OpOffer, Profile: profile, Placement: PlacementHosted})
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range offers {
			for _, name := range o.Names {
				contexts[profile+" "+name] = o
			}
		}
	}
	if o := contexts["chat deepseek-v4-pro"]; o.Context != 1048576 || o.ContextSource != ContextMaximum {
		t.Fatalf("reported context_window: %+v", o)
	}
	if o := contexts["chat router-model"]; o.Context != 200000 || o.ContextSource != ContextMaximum {
		t.Fatalf("reported context_length: %+v", o)
	}
	if o := contexts["chat bare-model"]; o.ContextSource != ContextDefault {
		t.Fatalf("a model without metadata keeps the default: %+v", o)
	}
	if _, ok := contexts["embed deepseek-v4-pro"]; ok {
		t.Fatal("a text-only model was offered for embeddings")
	}
	if _, ok := contexts["embed bare-model"]; !ok {
		t.Fatal("a model reporting no media keeps its host's profiles")
	}
}

// A provider whose listing names no context falls back to the catalogue's
// documented window for its preset.
func TestHostedContextFallsBackToTheCatalogue(t *testing.T) {
	h := NewHosted("deepseek", "https://api.deepseek.com/v1", WireOpenAICompatible, "oa-panel-deepseek")
	contexts, profiles := hostedMetadata(h, []string{"deepseek-flash"}, &listedMetadata{contexts: map[string]modelContext{}, profiles: map[string][]string{"deepseek-flash": {ProfileChat, ProfileImage}}})
	if contexts["deepseek-flash"].maximum != 1048576 {
		t.Fatalf("catalogue context %+v", contexts)
	}
	if !slices.Equal(profiles["deepseek-flash"], []string{ProfileChat, ProfileImage}) {
		t.Fatalf("reported profiles within the host's %v", profiles)
	}
	other := NewHosted("other", "https://example.invalid/v1", WireOpenAICompatible, "other")
	if contexts, _ := hostedMetadata(other, []string{"m"}, &listedMetadata{contexts: map[string]modelContext{}, profiles: map[string][]string{}}); len(contexts) != 0 {
		t.Fatalf("an unknown provider has no catalogue context: %+v", contexts)
	}
}
