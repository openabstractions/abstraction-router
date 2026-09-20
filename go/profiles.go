package router

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync"
)

// Profiles a host serves, the seed of abstraction.inference host_profiles. A
// later profile is <owner>/<name>@<n>.
const (
	ProfileChat          = "chat"
	ProfileEmbed         = "embed"
	ProfileTranscription = "transcription"
	ProfileSpeech        = "speech"
	ProfileImage         = "image"
	ProfileLive          = "live"
)

// SeedProfiles is every seeded profile, in catalogue order.
var SeedProfiles = []string{ProfileChat, ProfileEmbed, ProfileTranscription, ProfileSpeech, ProfileImage, ProfileLive}

// DefaultProfiles is what a host serves when it declares none: every seeded
// profile on the OpenAI-compatible wire, which the local runtimes speak, chat
// on any other wire, and nothing for a host with no chat endpoint.
func DefaultProfiles(h *Host) []string {
	switch {
	case h.Wire == WireOpenAIRealtime:
		return []string{ProfileLive}
	case h.Chat == "":
		return nil
	case !h.Hosted || h.Wire == WireOpenAICompatible:
		return []string{ProfileChat, ProfileEmbed, ProfileTranscription, ProfileSpeech, ProfileImage}
	case h.Wire == WireDeepgramPrerecorded:
		return []string{ProfileTranscription}
	case h.Wire == WireElevenLabsStream:
		return []string{ProfileSpeech}
	case h.Wire == WireStabilityV2Beta || h.Wire == WireFalQueue || h.Wire == WireReplicatePredictions:
		return []string{ProfileImage}
	}
	return []string{ProfileChat}
}

// HostProfiles is what h serves: its declared profiles, or the default.
func (h *Host) HostProfiles() []string {
	if len(h.Profiles) > 0 {
		return slices.Clone(h.Profiles)
	}
	return DefaultProfiles(h)
}

// lmStudioProfiles maps LM Studio's /api/v0/models type to profiles
// (lmstudio.ai/docs/developer/rest/endpoints: llm, vlm, embeddings).
func lmStudioProfiles(kind string) []string {
	switch kind {
	case "llm", "vlm":
		return []string{ProfileChat}
	case "embeddings":
		return []string{ProfileEmbed}
	}
	return nil
}

// ollamaProfiles maps Ollama's /api/show capabilities to profiles
// (github.com/ollama/ollama types/model/capability.go): completion serves
// chat, embedding serves embed and image serves image. tools, insert, vision,
// thinking and audio describe what a chat model accepts.
func ollamaProfiles(capabilities []string) []string {
	var out []string
	for _, c := range capabilities {
		var p string
		switch c {
		case "completion":
			p = ProfileChat
		case "embedding":
			p = ProfileEmbed
		case "image":
			p = ProfileImage
		}
		if p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// ollamaShow reads each model's capabilities once and keeps them while the
// model stays listed.
type ollamaShow struct {
	mu    sync.Mutex
	known map[string][]string
}

func (s *ollamaShow) profiles(h *Host, names []string) map[string][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.known == nil {
		s.known = map[string][]string{}
	}
	out := map[string][]string{}
	for _, name := range names {
		if p, ok := s.known[name]; ok {
			out[name] = p
			continue
		}
		var d struct {
			Capabilities []string `json:"capabilities"`
		}
		if err := post(h.Base+"/api/show", map[string]string{"model": name}, &d); err != nil || d.Capabilities == nil {
			continue
		}
		s.known[name] = ollamaProfiles(d.Capabilities)
		out[name] = s.known[name]
	}
	for name := range s.known {
		if _, listed := out[name]; !listed {
			delete(s.known, name)
		}
	}
	return out
}

func post(url string, body, into any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := client.Post(url, "application/json", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(into)
}

// serves reports whether host serves name for profile: the model's own
// profiles when the host reported them, otherwise the host's.
func (s snapshot) serves(h *Host, name, profile string) bool {
	if p, ok := s.profiles[h.Name][name]; ok {
		return slices.Contains(p, profile)
	}
	return slices.Contains(h.HostProfiles(), profile)
}
