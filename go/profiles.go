package router

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
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
	// ProfileVideo is a native provider's abstraction.inference/video@1,
	// which the runtime's job executor reaches for video jobs (inference
	// CONTRACT.md INF-G5). No host serves it by default.
	ProfileVideo = "video"
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

// ollamaModelMetadata reads each model's capabilities and context length once
// per survey from /api/show. The result is intentionally survey-scoped so
// changes to same-named models are visible on the next fresh inventory read.
func ollamaModelMetadata(ctx context.Context, h *Host, names []string) map[string]hostModelMetadata {
	out := make(map[string]hostModelMetadata, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		if err := ctx.Err(); err != nil {
			return out
		}
		var d struct {
			Capabilities []string       `json:"capabilities"`
			ModelInfo    map[string]any `json:"model_info"`
		}
		if err := postContext(ctx, h.Base+"/api/show", map[string]string{"model": name}, &d); err != nil || d.Capabilities == nil {
			continue
		}
		metadata := hostModelMetadata{profiles: ollamaProfiles(d.Capabilities)}
		if n := ollamaContextLength(d.ModelInfo); n > 0 {
			metadata.context.maximum = n
		}
		out[name] = metadata
	}
	return out
}

func post(url string, body, into any) error {
	return postContext(context.Background(), url, body, into)
}

func postContext(ctx context.Context, url string, body, into any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
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
