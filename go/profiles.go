package router

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
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

// ollamaContextLength reads a model's context window in tokens from
// /api/show's model_info (github.com/ollama/ollama server/routes.go): the key
// named "<general.architecture>.context_length" when architecture is known,
// otherwise the first key found ending ".context_length" so an unrecognised
// architecture still reports its length. 0 when model_info names none.
func ollamaContextLength(modelInfo map[string]any) int64 {
	if arch, ok := modelInfo["general.architecture"].(string); ok && arch != "" {
		if n, ok := modelInfo[arch+".context_length"].(float64); ok && n > 0 {
			return int64(n)
		}
	}
	var keys []string
	for k := range modelInfo {
		if strings.HasSuffix(k, ".context_length") {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		if n, ok := modelInfo[k].(float64); ok && n > 0 {
			return int64(n)
		}
	}
	return 0
}

// ollamaShow reads each model's capabilities and context length once, from
// one /api/show call that loads nothing (it reads stored metadata, not model
// weights), and keeps them while the model stays listed.
type ollamaShow struct {
	mu      sync.Mutex
	known   map[string][]string
	context map[string]int64
}

// ensure populates known and context for every name not already cached,
// then drops any cached name no longer in names. Locked by the caller.
func (s *ollamaShow) ensure(h *Host, names []string) {
	if s.known == nil {
		s.known = map[string][]string{}
	}
	if s.context == nil {
		s.context = map[string]int64{}
	}
	for _, name := range names {
		if _, ok := s.known[name]; ok {
			continue
		}
		var d struct {
			Capabilities []string       `json:"capabilities"`
			ModelInfo    map[string]any `json:"model_info"`
		}
		if err := post(h.Base+"/api/show", map[string]string{"model": name}, &d); err != nil || d.Capabilities == nil {
			continue
		}
		s.known[name] = ollamaProfiles(d.Capabilities)
		if n := ollamaContextLength(d.ModelInfo); n > 0 {
			s.context[name] = n
		}
	}
	for name := range s.known {
		if !has(names, name) {
			delete(s.known, name)
			delete(s.context, name)
		}
	}
}

func (s *ollamaShow) profiles(h *Host, names []string) map[string][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensure(h, names)
	out := map[string][]string{}
	for _, name := range names {
		if p, ok := s.known[name]; ok {
			out[name] = p
		}
	}
	return out
}

// contextLength is modelContextLength for the Ollama host: it shares
// ensure's cache with profiles, so a survey that already read profiles for
// these names sends no further /api/show call.
func (s *ollamaShow) contextLength(h *Host, names []string) map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensure(h, names)
	out := map[string]int64{}
	for _, name := range names {
		if n, ok := s.context[name]; ok {
			out[name] = n
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
