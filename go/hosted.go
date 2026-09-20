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
		resident: func(*Host) ([]string, error) { return nil, nil }}
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
	resp, err := hostedClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("listing %s: unreachable", address)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("listing %s: %s", address, resp.Status)
	}
	var models []struct {
		ID string `json:"model_id"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&models) != nil {
		return nil, fmt.Errorf("listing %s: unreadable", address)
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := hostedClient.Do(req)
	if err != nil {
		// The URL error names the address only; headers never enter an error.
		return nil, fmt.Errorf("listing %s: unreachable", address)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("listing %s: %s", address, resp.Status)
	}
	var d struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&d); err != nil {
		return nil, fmt.Errorf("listing %s: unreadable", address)
	}
	out := make([]string, 0, len(d.Data))
	for _, m := range d.Data {
		if m.ID != "" {
			out = append(out, m.ID)
		}
	}
	return out, nil
}

// readHosted lists one hosted host, applying its credential for this request.
func (r *Router) readHosted(h *Host) (installed []string, why string) {
	if h.listing == nil {
		return nil, "unsupported_wire:" + h.Wire
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var headers map[string]string
	// A remote runtime applies its own credential; none is applied here.
	if h.Credential != "" && h.remote == nil {
		apply := r.applier()
		if apply == nil {
			return nil, "credential:unavailable:" + h.Credential
		}
		var err error
		if headers, err = apply(ctx, CredentialConsumer, h.Credential, Target(h.Base)); err != nil {
			return nil, CredentialReason(h.Credential, err)
		}
	}
	installed, err := h.listing(ctx, h, headers)
	clear(headers)
	if err != nil {
		return nil, err.Error()
	}
	return installed, ""
}
