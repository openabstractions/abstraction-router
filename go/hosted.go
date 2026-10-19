package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/openabstractions/abstraction-router/go/hostedcatalogue"
)

// Wire kinds a hosted host speaks, the seed of router.thrift wire_kinds.
const (
	WireOpenAICompatible     = "openai-compatible"
	WireOpenAIRealtime       = "openai-realtime"
	WireAnthropicMessages    = "anthropic-messages"
	WireDeepgramPrerecorded  = "deepgram-prerecorded"
	WireElevenLabsStream     = "elevenlabs-stream"
	WireStabilityV2Beta      = "stability-v2beta"
	WireFalQueue             = "fal-queue"
	WireReplicatePredictions = "replicate-predictions"
)

// CredentialConsumer is the consumer contract name the router gives the
// credentials applier when it reads a hosted host's listing.
const CredentialConsumer = "abstraction.router/router@1"

// AnthropicVersion is the API version header the anthropic-messages wire sends.
const AnthropicVersion = "2023-06-01"

// Applier applies a named credential to one outgoing request the service sends
// to target, a lowercase host name. It returns the request headers for that one
// request, or an error. A *CredentialRefusal carries the applier's outcome
// word; any other error reads as unavailable. The router sends the headers once
// and keeps them out of every snapshot, audit entry and error.
type Applier func(ctx context.Context, consumer, name, target string) (map[string]string, error)

// CredentialRefusal is an applier outcome other than applied.
type CredentialRefusal struct{ Outcome, Name string }

func (e *CredentialRefusal) Error() string { return "credential:" + e.Outcome + ":" + e.Name }

// CredentialReason is the why word of a failed apply: credential:<outcome>:<name>.
func CredentialReason(name string, err error) string {
	var refusal *CredentialRefusal
	if errors.As(err, &refusal) {
		return refusal.Error()
	}
	return "credential:unavailable:" + name
}

// UseCredentials gives the router the applier its hosted listings use. Without
// one a hosted host that names a credential is listed up false with why
// credential:unavailable:<name>.
func (r *Router) UseCredentials(apply Applier) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.apply = apply
}

func (r *Router) applier() Applier {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.apply
}

var hostedClient = &http.Client{Timeout: 10 * time.Second}

// A hosted listing's credential was applied for its declared target. Refuse a
// redirect before net/http can copy custom credential headers to another host.
func hostedRequestClient(headers map[string]string) *http.Client {
	if len(headers) == 0 {
		return hostedClient
	}
	copy := *hostedClient
	copy.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("hosted credential redirect refused")
	}
	return &copy
}

// Target is the lowercase host name of a base URL, the credential target.
func Target(base string) string {
	u, err := url.Parse(base)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// NewHosted is a provider endpoint reached over the network. base is the wire's
// API root (https://openrouter.ai/api/v1 for openai-compatible,
// https://api.anthropic.com for anthropic-messages) and credential the name the
// service applies, or "" for an endpoint that takes none. A wire this package
// does not know is kept and listed up false with why unsupported_wire:<kind>.
func NewHosted(name, base, wire, credential string) *Host {
	h := &Host{Name: name, Base: strings.TrimRight(base, "/"), Hosted: true, Wire: wire, Credential: credential,
		resident: func(context.Context, *Host) ([]string, error) { return nil, nil }}
	switch wire {
	case WireOpenAICompatible:
		h.Chat = "/chat/completions"
		h.listing = func(ctx context.Context, h *Host, headers map[string]string) ([]string, error) {
			return listIDs(ctx, h.Base+"/models", headers)
		}
	case WireOpenAIRealtime:
		h.Chat = "/realtime"
		h.Profiles = []string{ProfileLive}
		h.listing = func(ctx context.Context, h *Host, headers map[string]string) ([]string, error) {
			return listIDs(ctx, h.Base+"/models", headers)
		}
	case WireAnthropicMessages:
		h.Chat = "/v1/messages"
		h.listing = func(ctx context.Context, h *Host, headers map[string]string) ([]string, error) {
			with := map[string]string{"anthropic-version": AnthropicVersion}
			for k, v := range headers {
				with[k] = v
			}
			return listIDs(ctx, h.Base+"/v1/models?limit=1000", with)
		}
	case WireDeepgramPrerecorded:
		h.Chat = "/v1/listen"
		h.Profiles = []string{ProfileTranscription}
		h.listing = func(context.Context, *Host, map[string]string) ([]string, error) {
			return []string{"nova-3", "nova-2", "enhanced", "base"}, nil
		}
	case WireElevenLabsStream:
		h.Chat, h.Profiles = "/v1/text-to-speech", []string{ProfileSpeech}
		h.listing = func(ctx context.Context, h *Host, headers map[string]string) ([]string, error) {
			return listArrayIDs(ctx, h.Base+"/v1/models", headers)
		}
	case WireStabilityV2Beta:
		h.Chat, h.Profiles = "/v2beta/stable-image", []string{ProfileImage}
		h.listing = func(context.Context, *Host, map[string]string) ([]string, error) {
			return []string{"core", "ultra"}, nil
		}
	case WireFalQueue:
		h.Chat, h.Profiles = "/", []string{ProfileImage}
		h.listing = func(context.Context, *Host, map[string]string) ([]string, error) {
			return []string{"fal-ai/stable-diffusion-v3-medium"}, nil
		}
	case WireReplicatePredictions:
		h.Chat, h.Profiles = "/v1/predictions", []string{ProfileImage}
		h.listing = func(context.Context, *Host, map[string]string) ([]string, error) {
			return []string{"black-forest-labs/flux-schnell"}, nil
		}
	}
	return h
}

func listArrayIDs(ctx context.Context, address string, headers map[string]string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := hostedRequestClient(headers).Do(req)
	if err != nil {
		return nil, failedListing(causeOf(err), fmt.Errorf("listing %s: unreachable", address))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, failedListing(statusCause(resp.StatusCode), fmt.Errorf("listing %s: %s", address, resp.Status))
	}
	var models []struct {
		ID string `json:"model_id"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&models) != nil {
		return nil, failedListing(AvailabilityMalformed, fmt.Errorf("listing %s: unreadable", address))
	}
	out := make([]string, 0, len(models))
	for _, model := range models {
		if model.ID != "" {
			out = append(out, model.ID)
		}
	}
	return out, nil
}

func listIDs(ctx context.Context, address string, headers map[string]string) ([]string, error) {
	return listIDsWithClient(ctx, address, headers, hostedRequestClient(headers))
}

func listIDsWithClient(ctx context.Context, address string, headers map[string]string, client *http.Client) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	defer clear(req.Header)
	resp, err := client.Do(req)
	if err != nil {
		// The URL error names the address only; headers never enter an error.
		return nil, failedListing(causeOf(err), fmt.Errorf("listing %s: unreachable", address))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, failedListing(statusCause(resp.StatusCode), fmt.Errorf("listing %s: %s", address, resp.Status))
	}
	var d struct {
		Data []listedModel `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&d); err != nil || d.Data == nil {
		return nil, failedListing(AvailabilityMalformed, fmt.Errorf("listing %s: unreadable", address))
	}
	seen, _ := ctx.Value(listedMetadataKey{}).(*listedMetadata)
	out := make([]string, 0, len(d.Data))
	for _, m := range d.Data {
		if m.ID != "" {
			out = append(out, m.ID)
			seen.note(m)
		}
	}
	return out, nil
}

// listedModel is one entry of an OpenAI-compatible GET /models listing with
// the optional metadata providers add: the context window (DeepSeek and Groq
// context_window, OpenRouter context_length, vLLM max_model_len) and the
// generated media (DeepSeek output_modalities).
type listedModel struct {
	ID               string          `json:"id"`
	ContextWindow    json.RawMessage `json:"context_window"`
	ContextLength    json.RawMessage `json:"context_length"`
	MaxModelLen      json.RawMessage `json:"max_model_len"`
	OutputModalities []string        `json:"output_modalities"`
	InputModalities  []string        `json:"input_modalities"`
	Architecture     *struct {
		InputModalities  []string `json:"input_modalities"`
		OutputModalities []string `json:"output_modalities"`
	} `json:"architecture"`
	SupportedParameters []string `json:"supported_parameters"`
}

func (m listedModel) context() int64 {
	for _, raw := range []json.RawMessage{m.ContextWindow, m.ContextLength, m.MaxModelLen} {
		if n := positiveJSONInteger(raw); n > 0 {
			return n
		}
	}
	return 0
}

// profiles reads the profiles a model's reported output media serve, or nil
// when the provider reports none: text is chat, image is image and audio is
// speech.
func (m listedModel) profiles() []string {
	if len(m.OutputModalities) == 0 {
		return nil
	}
	out := []string{}
	for _, media := range m.OutputModalities {
		var profile string
		switch media {
		case "text":
			profile = ProfileChat
		case "image":
			profile = ProfileImage
		case "audio":
			profile = ProfileSpeech
		}
		if profile != "" && !has(out, profile) {
			out = append(out, profile)
		}
	}
	return out
}

// listedMetadata collects what one hosted listing reported about its models.
type listedMetadata struct {
	contexts     map[string]modelContext
	profiles     map[string][]string
	capabilities map[string]*ModelCapabilities
}

type listedMetadataKey struct{}

func (l *listedMetadata) note(m listedModel) {
	if l == nil {
		return
	}
	if n := m.context(); n > 0 {
		l.contexts[m.ID] = modelContext{maximum: n}
	}
	if p := m.profiles(); p != nil {
		l.profiles[m.ID] = p
	}
	if c := m.capabilities(); c != nil {
		if l.capabilities == nil {
			l.capabilities = map[string]*ModelCapabilities{}
		}
		l.capabilities[m.ID] = c
	}
}

// hostedMetadata is what a hosted listing reported, completed from the
// hosted catalogue: a provider's documented context window for each model
// whose listing named none (architect ruling D(a), 2026-11-08). Profiles a
// model reports are kept within the host's own.
func hostedMetadata(h *Host, installed []string, listed *listedMetadata) (map[string]modelContext, map[string][]string) {
	contexts, profiles := map[string]modelContext{}, map[string][]string{}
	fallback := catalogueContext(h.Base, h.Wire)
	for _, name := range installed {
		if c, ok := listed.contexts[name]; ok {
			contexts[name] = c
		} else if fallback > 0 {
			contexts[name] = modelContext{maximum: fallback}
		}
		if reported, ok := listed.profiles[name]; ok {
			kept := []string{}
			for _, p := range reported {
				if has(h.HostProfiles(), p) {
					kept = append(kept, p)
				}
			}
			profiles[name] = kept
		}
	}
	return contexts, profiles
}

// catalogueContext is the documented context window of the hosted preset
// whose address and wire base and wire are, or 0.
func catalogueContext(base, wire string) int64 {
	p, _ := hostedcatalogue.PresetFor(base, wire)
	return p.ModelContext
}

// readHosted lists one hosted host, applying its credential for this request,
// with the metadata its listing reported (listedMetadata).
func (r *Router) readHosted(ctx context.Context, h *Host) (installed []string, listed *listedMetadata, why string, cause AvailabilityCause) {
	listed = &listedMetadata{contexts: map[string]modelContext{}, profiles: map[string][]string{}}
	if h.listing == nil {
		return nil, listed, "unsupported_wire:" + h.Wire, AvailabilityConfiguration
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var headers map[string]string
	// A remote runtime applies its own credential; none is applied here.
	if h.Credential != "" && h.remote == nil {
		apply := r.applier()
		if apply == nil {
			return nil, listed, "credential:unavailable:" + h.Credential, AvailabilityCredential
		}
		var err error
		if headers, err = apply(ctx, CredentialConsumer, h.Credential, Target(h.Base)); err != nil {
			return nil, listed, CredentialReason(h.Credential, err), AvailabilityCredential
		}
	}
	installed, err := h.listing(context.WithValue(ctx, listedMetadataKey{}, listed), h, headers)
	clear(headers)
	if err != nil {
		return nil, listed, err.Error(), causeOf(err)
	}
	return installed, listed, "", ""
}
