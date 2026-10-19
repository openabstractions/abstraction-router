package router

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// aliasWindows reads each alias's effective context length from a fresh
// survey of r.
func aliasWindows(t *testing.T, r *Router) map[string]int64 {
	t.Helper()
	families, _ := r.Models(true)
	out := map[string]int64{}
	for _, f := range families {
		for _, a := range f.Names {
			out[a.Name] = a.EffectiveContextLength
		}
	}
	return out
}

// Ollama fixes a model's window at load: a running model's window is its
// /api/ps context_length, a stopped one's is its Modelfile num_ctx, and one
// with neither is unknown until LoadedWindow loads it (ROUTE-D8).
func TestOllamaReportsTheWindowItLoadsWith(t *testing.T) {
	var loaded atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tags", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"models":[{"name":"running:8b"},{"name":"pinned:8b"},{"name":"cold:8b"}]}`))
	})
	mux.HandleFunc("/api/ps", func(w http.ResponseWriter, _ *http.Request) {
		cold := ""
		if loaded.Load() {
			cold = `,{"name":"cold:8b","model":"cold:8b","context_length":4096}`
		}
		fmt.Fprintf(w, `{"models":[{"name":"running:8b","model":"running:8b","context_length":8192}%s]}`, cold)
	})
	mux.HandleFunc("/api/show", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		parameters := "temperature 0.6"
		if string(body) == `{"model":"pinned:8b"}` {
			parameters = "num_ctx                        16384\nstop \"<|im_end|>\""
		}
		fmt.Fprintf(w, `{"capabilities":["completion"],"model_info":{"general.architecture":"qwen3","qwen3.context_length":40960},"parameters":%q}`, parameters)
	})
	mux.HandleFunc("/api/generate", func(w http.ResponseWriter, r *http.Request) {
		if body, _ := io.ReadAll(r.Body); string(body) != `{"model":"cold:8b"}` {
			t.Errorf("load body %s", body)
		}
		loaded.Store(true)
		w.Write([]byte(`{"model":"cold:8b","done":true,"done_reason":"load"}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	host := Ollama(server.URL)
	r := New(host)
	windows := aliasWindows(t, r)
	if windows["running:8b"] != 8192 || windows["pinned:8b"] != 16384 || windows["cold:8b"] != 0 {
		t.Fatalf("windows %v", windows)
	}
	offers, _, err := r.Offer(Request{Op: OpOffer, Profile: ProfileChat, Placement: PlacementLocal})
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]string{}
	for _, o := range offers {
		for _, name := range o.Names {
			sources[name] = o.ContextSource
		}
	}
	if sources["cold:8b"] != ContextUnknown || sources["running:8b"] != ContextEffective {
		t.Fatalf("offer context sources %v: Ollama's maximum stood in for its load window", sources)
	}
	if got := LoadedWindow(context.Background(), host, "cold:8b"); got != 4096 || !loaded.Load() {
		t.Fatalf("LoadedWindow(cold) = %d, loaded %v", got, loaded.Load())
	}
}

// LM Studio reports the context length a loaded model was loaded with; a
// model it has not loaded has an unknown window.
func TestLMStudioReportsItsLoadedContextLength(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v0/models" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"data":[{"id":"qwen3-8b","type":"llm","state":"loaded","max_context_length":32768,"loaded_context_length":4096},` +
			`{"id":"gemma-3-4b","type":"llm","state":"not-loaded","max_context_length":131072}]}`))
	}))
	defer server.Close()
	host := LMStudio(server.URL)
	windows := aliasWindows(t, New(host))
	if windows["qwen3-8b"] != 4096 || windows["gemma-3-4b"] != 0 {
		t.Fatalf("windows %v", windows)
	}
	if got := LoadedWindow(context.Background(), host, "qwen3-8b"); got != 4096 {
		t.Fatalf("LoadedWindow = %d", got)
	}
}
