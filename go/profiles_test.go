package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
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
