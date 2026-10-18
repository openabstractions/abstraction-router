package router

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

// ollamaWithShow serves tags, ps and /api/show capabilities per model.
func ollamaWithShow(t *testing.T, capabilities map[string][]string) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tags", func(w http.ResponseWriter, _ *http.Request) {
		var models []map[string]string
		for name := range capabilities {
			models = append(models, map[string]string{"name": name})
		}
		json.NewEncoder(w).Encode(map[string]any{"models": models})
	})
	mux.HandleFunc("/api/ps", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{"models":[]}`)) })
	mux.HandleFunc("/api/show", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Model string }
		if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&body) != nil {
			http.Error(w, "bad", 400)
			return
		}
		c, ok := capabilities[body.Model]
		if !ok {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"capabilities": c})
	})
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s.URL
}

// LM Studio's embedding models serve embed only, and its llm models chat.
func TestLMStudioEmbeddingModelsServeEmbedOnly(t *testing.T) {
	lmstudio := serve(t, map[string]string{
		"/api/v0/models": `{"data":[{"id":"text-embedding-nomic-embed-text-v1.5","type":"embeddings","state":"not-loaded"},{"id":"qwen/qwen3-8b","type":"llm","state":"loaded"}]}`,
	})
	r := New(LMStudio(lmstudio))
	r.Survey()
	if d, _ := r.Route(Request{Model: "text-embedding-nomic-embed-text-v1.5", Profile: ProfileEmbed}); d.Verdict != WouldLoad || d.Host != "lmstudio" {
		t.Fatalf("embed pick %+v", d)
	}
	if d, _ := r.Route(Request{Model: "text-embedding-nomic-embed-text-v1.5"}); d.Verdict != NoHost || d.Host != "" {
		t.Fatalf("chat pick of an embedding model %+v", d)
	}
	if d, _ := r.Route(Request{Model: "qwen/qwen3-8b", Profile: ProfileChat}); d.Verdict != Resident {
		t.Fatalf("chat pick %+v", d)
	}
	if d, _ := r.Route(Request{Model: "qwen/qwen3-8b", Profile: ProfileEmbed}); d.Verdict != NoHost {
		t.Fatalf("embed pick of a chat model %+v", d)
	}
	families, _ := r.Models(false)
	for _, f := range families {
		for _, a := range f.Names {
			want := []string{ProfileChat}
			if a.Name == "text-embedding-nomic-embed-text-v1.5" {
				want = []string{ProfileEmbed}
			}
			if !slices.Equal(a.Profiles, want) {
				t.Fatalf("alias %+v, want profiles %v", a, want)
			}
		}
	}
}

// Ollama's capabilities map to profiles: completion to chat, embedding to
// embed, image to image; tools, vision and thinking add none.
func TestOllamaCapabilitiesMapToProfiles(t *testing.T) {
	for _, c := range []struct {
		capabilities []string
		want         []string
	}{
		{[]string{"completion", "tools", "vision", "thinking"}, []string{ProfileChat}},
		{[]string{"embedding"}, []string{ProfileEmbed}},
		{[]string{"image"}, []string{ProfileImage}},
		{[]string{"completion", "insert", "audio"}, []string{ProfileChat}},
		{[]string{}, nil},
	} {
		if got := ollamaProfiles(c.capabilities); !slices.Equal(got, c.want) {
			t.Fatalf("%v mapped to %v, want %v", c.capabilities, got, c.want)
		}
	}
	base := ollamaWithShow(t, map[string][]string{"nomic-embed-text:latest": {"embedding"}, "qwen3:8b": {"completion", "tools", "thinking"}})
	r := New(Ollama(base))
	r.Survey()
	if d, _ := r.Route(Request{Model: "nomic-embed-text:latest", Profile: ProfileEmbed}); d.Verdict != WouldLoad || d.Host != "ollama" {
		t.Fatalf("embed pick %+v", d)
	}
	if d, _ := r.Route(Request{Model: "nomic-embed-text:latest", Profile: ProfileChat}); d.Verdict != NoHost {
		t.Fatalf("chat pick of an embedding model %+v", d)
	}
	if d, _ := r.Route(Request{Model: "qwen3:8b"}); d.Verdict != WouldLoad || d.Host != "ollama" {
		t.Fatalf("chat pick %+v", d)
	}
}

// A speech pick with no host serving speech returns no-host, while a host
// that declares speech serves it.
func TestASpeechPickWithNoHostReturnsNoHost(t *testing.T) {
	lmstudio := serve(t, map[string]string{"/api/v0/models": `{"data":[{"id":"qwen/qwen3-8b","type":"llm","state":"loaded"}]}`})
	ollama := ollamaWithShow(t, map[string][]string{"qwen3:8b": {"completion"}})
	anthropic := NewHosted("anthropic", "https://api.anthropic.invalid", WireAnthropicMessages, "")
	r := New(LMStudio(lmstudio), Ollama(ollama), anthropic)
	r.Survey()
	if d, _ := r.Route(Request{Model: "kokoro-82m", Profile: ProfileSpeech}); d.Verdict != NoHost || d.Host != "" || d.Why != "no host serves the speech profile" {
		t.Fatalf("speech pick %+v", d)
	}
	speech := NewHosted("voices", "https://voices.invalid/v1", WireOpenAICompatible, "")
	speech.Profiles = []string{ProfileSpeech}
	r.SetHosts(LMStudio(lmstudio), Ollama(ollama), anthropic, speech)
	r.Survey()
	if d, _ := r.Route(Request{Model: "kokoro-82m", Profile: ProfileSpeech}); d.Verdict != NotOnDisk {
		t.Fatalf("a speech pick with a speech host but no such model %+v", d)
	}
	states, _, _, _, _ := r.Residency(false)
	for _, s := range states {
		want := map[string][]string{"lmstudio": {ProfileChat, ProfileEmbed, ProfileTranscription, ProfileSpeech, ProfileImage}, "ollama": {ProfileChat, ProfileEmbed, ProfileTranscription, ProfileSpeech, ProfileImage}, "anthropic": {ProfileChat}, "voices": {ProfileSpeech}}[s.Host]
		if !slices.Equal(s.Profiles, want) {
			t.Fatalf("host %s profiles %v, want %v", s.Host, s.Profiles, want)
		}
	}
}

// The embeddings address sits beside an OpenAI-compatible chat path, and a
// host whose chat path is not one has none.
func TestEmbedURLSitsBesideTheChatPath(t *testing.T) {
	for chat, want := range map[string]string{
		"/v1/chat/completions":     "http://h/v1/embeddings",
		"/api/v1/chat/completions": "http://h/api/v1/embeddings",
		"/v1/messages":             "",
		"":                         "",
	} {
		if got := (&Host{Base: "http://h", Chat: chat}).EmbedURL(); got != want {
			t.Fatalf("chat %q: embed %q, want %q", chat, got, want)
		}
	}
}

// ollamaWithShowContext serves tags, ps and /api/show with both capabilities
// and a model_info context length per model, so a survey reads both from one
// call.
func ollamaWithShowContext(t *testing.T, arch string, contextLength map[string]int64) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tags", func(w http.ResponseWriter, _ *http.Request) {
		var models []map[string]string
		for name := range contextLength {
			models = append(models, map[string]string{"name": name})
		}
		json.NewEncoder(w).Encode(map[string]any{"models": models})
	})
	mux.HandleFunc("/api/ps", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{"models":[]}`)) })
	mux.HandleFunc("/api/show", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Model string }
		if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&body) != nil {
			http.Error(w, "bad", 400)
			return
		}
		n, ok := contextLength[body.Model]
		if !ok {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"capabilities": []string{"completion"},
			"model_info":   map[string]any{"general.architecture": arch, arch + ".context_length": n},
		})
	})
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s.URL
}

// LM Studio's max_context_length, Ollama's model_info context length and
// Lemonade's max_context_window each reach the survey's Alias.ContextLength
// without loading a model: LM Studio and Lemonade read it from the same
// listing already used for installed/profiles, and Ollama's /api/show is a
// metadata read, never a load.
func TestHostsReportContextLength(t *testing.T) {
	lmstudio := serve(t, map[string]string{
		"/api/v0/models": `{"data":[{"id":"qwen/qwen3-8b","type":"llm","state":"loaded","max_context_length":40960}]}`,
	})
	lemonade := serve(t, map[string]string{
		"/api/v1/models": `{"data":[{"id":"Qwen3-8B-GGUF","checkpoint":"unsloth/Qwen3-8B-GGUF:Q4_K_XL.gguf","downloaded":true,"max_context_window":131072,"context_length":40960}]}`,
		"/api/v1/health": `{"all_models_loaded":[{"model_name":"Qwen3-8B-GGUF","recipe_options":{"ctx_size":4096}}]}`,
	})
	ollama := ollamaWithShowContext(t, "qwen3", map[string]int64{"qwen3:8b": 32768})
	r := New(LMStudio(lmstudio), Lemonade(lemonade), Ollama(ollama))
	r.Survey()
	families, _ := r.Models(false)
	want := map[string]int64{
		"qwen/qwen3-8b":                      40960,
		"Qwen3-8B-GGUF":                      131072,
		"unsloth/Qwen3-8B-GGUF:Q4_K_XL.gguf": 131072,
		"qwen3:8b":                           32768,
	}
	wantEffective := map[string]int64{"Qwen3-8B-GGUF": 4096, "unsloth/Qwen3-8B-GGUF:Q4_K_XL.gguf": 4096}
	seen := map[string]bool{}
	for _, f := range families {
		for _, a := range f.Names {
			if n, ok := want[a.Name]; ok {
				seen[a.Name] = true
				if a.ContextLength != n {
					t.Fatalf("alias %s: context length %d, want %d", a.Name, a.ContextLength, n)
				}
				if effective, ok := wantEffective[a.Name]; ok && a.EffectiveContextLength != effective {
					t.Fatalf("alias %s: effective context length %d, want %d", a.Name, a.EffectiveContextLength, effective)
				}
			}
		}
	}
	for name := range want {
		if !seen[name] {
			t.Fatalf("alias %s was not surveyed at all", name)
		}
	}
}

func TestOllamaRefreshesSameNameMetadataOncePerSurvey(t *testing.T) {
	contextLength := int64(8192)
	showCalls := 0
	failShow := false
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tags", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"models":[{"name":"qwen3:8b"}]}`))
	})
	mux.HandleFunc("/api/ps", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"models":[]}`))
	})
	mux.HandleFunc("/api/show", func(w http.ResponseWriter, r *http.Request) {
		showCalls++
		if failShow {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"capabilities": []string{"completion"},
			"model_info": map[string]any{
				"general.architecture": "qwen3",
				"qwen3.context_length": contextLength,
			},
		})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	r := New(Ollama(server.URL))
	readAlias := func() *Alias {
		families, _ := r.Models(false)
		for _, family := range families {
			for i := range family.Names {
				if family.Names[i].Name == "qwen3:8b" {
					return &family.Names[i]
				}
			}
		}
		t.Fatal("qwen3:8b alias missing")
		return nil
	}

	r.Survey()
	if got := readAlias(); got.ContextLength != 8192 || !slices.Contains(got.Profiles, ProfileChat) {
		t.Fatalf("initial survey metadata: %+v", got)
	}
	if showCalls != 1 {
		t.Fatalf("initial survey made %d /api/show calls, want 1", showCalls)
	}

	contextLength = 16384
	r.Survey()
	if got := readAlias(); got.ContextLength != 16384 || !slices.Contains(got.Profiles, ProfileChat) {
		t.Fatalf("same-name metadata was not refreshed: %+v", got)
	}
	if showCalls != 2 {
		t.Fatalf("two surveys made %d /api/show calls, want exactly 2", showCalls)
	}

	failShow = true
	r.Survey()
	if got := readAlias(); got.ContextLength != 0 {
		t.Fatalf("failed fresh /api/show must not retain a stale context observation: %+v", got)
	}
	if showCalls != 3 {
		t.Fatalf("three surveys made %d /api/show calls, want exactly 3", showCalls)
	}
}

func TestOllamaMetadataRefreshHonorsSurveyCancellation(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	defer close(release)
	var blockShow atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tags", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"models":[{"name":"qwen3:8b"}]}`))
	})
	mux.HandleFunc("/api/ps", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"models":[]}`))
	})
	mux.HandleFunc("/api/show", func(w http.ResponseWriter, r *http.Request) {
		if blockShow.Load() {
			started <- struct{}{}
			select {
			case <-r.Context().Done():
			case <-release:
			}
			return
		}
		w.Write([]byte(`{"capabilities":["completion"],"model_info":{"general.architecture":"qwen3","qwen3.context_length":8192}}`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	r := New(Ollama(server.URL))
	r.Survey()

	blockShow.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.SurveyContext(ctx) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("metadata request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled survey returned %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("metadata request ignored survey cancellation")
	}
	families, _ := r.Models(false)
	found := false
	for _, family := range families {
		for _, alias := range family.Names {
			if alias.Name == "qwen3:8b" {
				found = true
				if alias.ContextLength != 8192 || !slices.Contains(alias.Profiles, ProfileChat) {
					t.Fatalf("cancelled survey replaced the previous metadata snapshot: %+v", alias)
				}
			}
		}
	}
	if !found {
		t.Fatal("cancelled survey removed the previous qwen3:8b alias")
	}
}

func TestLemonadeEffectiveContextUnknownOrContradictoryStaysUnknown(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"data":[{"id":"missing","downloaded":true,"max_context_window":8192},{"id":"zero","downloaded":true,"max_context_window":8192},{"id":"negative","downloaded":true,"max_context_window":8192},{"id":"malformed","downloaded":true,"max_context_window":8192},{"id":"contradictory","downloaded":true,"max_context_window":4096},{"id":"unknown-max","downloaded":true,"max_context_window":0}]}`))
	})
	mux.HandleFunc("/api/v1/health", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{"all_models_loaded":[]}`)) })
	mux.HandleFunc("/api/v1/models/zero/options", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{"resolved_ctx_size":0}`)) })
	mux.HandleFunc("/api/v1/models/negative/options", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{"resolved_ctx_size":-1}`)) })
	mux.HandleFunc("/api/v1/models/malformed/options", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{"resolved_ctx_size":"4096"}`)) })
	mux.HandleFunc("/api/v1/models/contradictory/options", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{"resolved_ctx_size":8192}`)) })
	mux.HandleFunc("/api/v1/models/unknown-max/options", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{"resolved_ctx_size":2048}`)) })
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	r := New(Lemonade(server.URL))
	r.Survey()
	families, _ := r.Models(false)
	for _, family := range families {
		for _, alias := range family.Names {
			if alias.Name == "unknown-max" {
				if alias.ContextLength != 0 || alias.EffectiveContextLength != 2048 {
					t.Fatalf("positive effective observation may stand without theoretical maximum: %+v", alias)
				}
				continue
			}
			if alias.EffectiveContextLength != 0 || alias.ContextLength != 8192 && alias.Name != "contradictory" {
				t.Fatalf("%s: invalid/absent effective observation must stay unknown, got %+v", alias.Name, alias)
			}
			if alias.Name == "contradictory" && (alias.ContextLength != 4096 || alias.EffectiveContextLength != 0) {
				t.Fatalf("contradictory effective observation must be dropped, theoretical maximum retained: %+v", alias)
			}
		}
	}
}

// Lemonade Server 11.6.0's downloaded model records carry recipe and
// max_context_window, while its healthy-but-idle health response carries no
// per-model ctx_size. If the optional per-model options route is unavailable,
// the router still lists every downloaded alias but Offer must withhold it
// as unknown: max_context_window is not an effective request window for a
// server that fixes that window at load (ROUTE-D8).
func TestLemonade116DownloadedModelsStayListedButNotOfferedWithoutEffectiveContext(t *testing.T) {
	models := `{"object":"list","data":[` +
		`{"id":"Qwen3-0.6B-GGUF","checkpoint":"unsloth/Qwen3-0.6B-GGUF:Q4_0","downloaded":true,"recipe":"llamacpp","max_context_window":40960,"labels":["reasoning","tool-calling"]},` +
		`{"id":"Qwen3.6-35B-A3B-MTP-GGUF","checkpoint":"unsloth/Qwen3.6-35B-A3B-MTP-GGUF:Qwen3.6-35B-A3B-UD-Q4_K_XL.gguf","downloaded":true,"recipe":"llamacpp","max_context_window":262144},` +
		`{"id":"qwen3.6-moe-35b-a3b-FLM","checkpoint":"qwen3.6-moe:35b-a3b","downloaded":true,"recipe":"flm","max_context_window":262144}]}`
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/models", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(models)) })
	mux.HandleFunc("/api/v1/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"all_models_loaded":[],"model_loaded":null,"status":"ok","websocket_port":9000}`))
	})
	// This fixture represents the installed server's unsupported options route.
	mux.HandleFunc("/api/v1/models/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	r := New(Lemonade(server.URL))
	r.Survey()
	hosts, _, _, _, _ := r.Residency(false)
	if len(hosts) != 1 || !hosts[0].Up || hosts[0].Installed != 6 || len(hosts[0].Resident) != 0 {
		t.Fatalf("healthy Lemonade inventory should list three downloaded IDs and checkpoints while idle: %+v", hosts)
	}

	wantMaximum := map[string]int64{
		"Qwen3-0.6B-GGUF":          40960,
		"Qwen3.6-35B-A3B-MTP-GGUF": 262144,
		"qwen3.6-moe-35b-a3b-FLM":  262144,
	}
	families, _ := r.Models(false)
	listed := map[string]Alias{}
	for _, family := range families {
		for _, alias := range family.Names {
			listed[alias.Name] = alias
		}
	}
	for name, maximum := range wantMaximum {
		alias, ok := listed[name]
		if !ok || alias.ContextLength != maximum || alias.EffectiveContextLength != 0 {
			t.Fatalf("downloaded alias %q should stay visible with only its reported maximum: %+v, present=%t", name, alias, ok)
		}
	}

	offers, _, err := r.Offer(Request{Profile: ProfileChat, Placement: PlacementLocal, Reserve: 4096})
	if err != nil {
		t.Fatal(err)
	}
	for name := range wantMaximum {
		var found *Offer
		for i := range offers {
			if slices.Contains(offers[i].Names, name) {
				found = &offers[i]
				break
			}
		}
		if found == nil || found.ContextSource != ContextUnknown || found.Context != 0 || found.Input != 0 || !slices.Contains(found.Unknown, "lemonade/"+name) {
			t.Fatalf("unresolved Lemonade alias %q must remain a typed unknown offer, got %+v", name, found)
		}
	}
}

func TestLemonadeUsesResolvedOptionsOncePerUnloadedModelAndResidentHealth(t *testing.T) {
	optionCalls := map[string]int{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"data":[{"id":"auto-id","checkpoint":"owner/auto:Q4.gguf","downloaded":true,"max_context_window":40960,"context_length":40960},{"id":"auto-id","checkpoint":"owner/auto-duplicate:Q4.gguf","downloaded":true,"max_context_window":40960,"context_length":40960},{"id":"resident","downloaded":true,"max_context_window":32768,"context_length":32768}]}`))
	})
	mux.HandleFunc("/api/v1/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"all_models_loaded":[{"model_name":"resident","recipe_options":{"ctx_size":16384}}]}`))
	})
	mux.HandleFunc("/api/v1/models/auto-id/options", func(w http.ResponseWriter, _ *http.Request) {
		optionCalls["auto-id"]++
		w.Write([]byte(`{"resolved_ctx_size":4096}`))
	})
	mux.HandleFunc("/api/v1/models/resident/options", func(w http.ResponseWriter, _ *http.Request) {
		optionCalls["resident"]++
		w.Write([]byte(`{"resolved_ctx_size":8192}`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	r := New(Lemonade(server.URL))
	r.Survey()
	families, _ := r.Models(false)
	got := map[string]Alias{}
	for _, family := range families {
		for _, alias := range family.Names {
			got[alias.Name] = alias
		}
	}
	for _, name := range []string{"auto-id", "owner/auto:Q4.gguf", "owner/auto-duplicate:Q4.gguf"} {
		if got[name].ContextLength != 40960 || got[name].EffectiveContextLength != 4096 {
			t.Fatalf("unloaded alias %q should retain theoretical and resolved contexts separately: %+v", name, got[name])
		}
	}
	if got["resident"].ContextLength != 32768 || got["resident"].EffectiveContextLength != 16384 {
		t.Fatalf("resident context should come from health recipe options: %+v", got["resident"])
	}
	if optionCalls["auto-id"] != 1 || optionCalls["resident"] != 0 {
		t.Fatalf("options lookups should be once per unloaded model ID only: %+v", optionCalls)
	}
}

func TestLemonadeMetadataRefreshHonorsSurveyCancellation(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	defer close(release)
	var block atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"data":[{"id":"auto-id","downloaded":true,"max_context_window":40960}]}`))
	})
	mux.HandleFunc("/api/v1/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"all_models_loaded":[]}`))
	})
	mux.HandleFunc("/api/v1/models/auto-id/options", func(w http.ResponseWriter, r *http.Request) {
		if block.Load() {
			started <- struct{}{}
			select {
			case <-r.Context().Done():
			case <-release:
			}
			return
		}
		w.Write([]byte(`{"resolved_ctx_size":8192}`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	r := New(Lemonade(server.URL))
	r.Survey()
	block.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.SurveyContext(ctx) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("Lemonade resolved-context request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled survey returned %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Lemonade resolved-context request ignored survey cancellation")
	}
	families, _ := r.Models(false)
	found := false
	for _, family := range families {
		for _, alias := range family.Names {
			if alias.Name == "auto-id" {
				found = true
				if alias.ContextLength != 40960 || alias.EffectiveContextLength != 8192 {
					t.Fatalf("cancelled survey replaced the previous Lemonade metadata snapshot: %+v", alias)
				}
			}
		}
	}
	if !found {
		t.Fatal("cancelled survey removed the previous Lemonade alias")
	}
}

// A model whose host reports no length carries a zero Alias.ContextLength,
// so a caller can tell "unreported" from any real value.
func TestAModelWithNoReportedLengthCarriesZero(t *testing.T) {
	lmstudio := serve(t, map[string]string{
		"/api/v0/models": `{"data":[{"id":"qwen/qwen3-8b","type":"llm","state":"loaded"}]}`,
	})
	r := New(LMStudio(lmstudio))
	r.Survey()
	families, _ := r.Models(false)
	for _, f := range families {
		for _, a := range f.Names {
			if a.ContextLength != 0 {
				t.Fatalf("alias %+v: want zero context length when the host reports none", a)
			}
		}
	}
}

func TestLiveRequiresAnExplicitRealtimeHost(t *testing.T) {
	for _, h := range []*Host{LMStudio("http://127.0.0.1:1"), NewHosted("ordinary", "http://127.0.0.1:1", WireOpenAICompatible, "")} {
		if slices.Contains(h.HostProfiles(), ProfileLive) {
			t.Fatalf("ordinary host advertises live: %+v", h.HostProfiles())
		}
	}
	h := NewHosted("live", "https://example.invalid/v1", WireOpenAIRealtime, "key")
	if !slices.Equal(h.HostProfiles(), []string{ProfileLive}) {
		t.Fatal(h.HostProfiles())
	}
}
