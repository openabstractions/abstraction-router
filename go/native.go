package router

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/openabstractions/abstraction-identity/listen"
)

// WireNative is abstraction.inference served by another local process over
// the shared authenticated native transport.
const WireNative = "oa-native@1"

// WireImageEngine identifies an in-process image adapter supplied explicitly
// to an inference provider. It is an internal Go host kind, not a registry
// transport or an application-facing wire protocol.
const WireImageEngine = "oa-image-engine"

const nativeFrameBytes = 1 << 20

type nativeLink struct {
	transport listen.FrameClient
}

// DeclaredEntry reads one declaration model entry against profiles, the
// profiles the declaration serves (ROUTE-D9). An entry "<profile>:<model>"
// with a nonempty model and a profile in profiles is model for that profile
// alone; any other entry is the whole entry as a model for every profile,
// with profile "". Every reader of a declaration's entries parses them here.
func DeclaredEntry(entry string, profiles []string) (model, profile string) {
	if prefix, rest, scoped := strings.Cut(entry, ":"); scoped && rest != "" && slices.Contains(profiles, prefix) {
		return rest, prefix
	}
	return entry, ""
}

// DeclaredModels reads a declaration's model entries (ROUTE-D9). An entry
// "<profile>:<model>" whose profile is one of profiles serves model for that
// profile alone; any other entry, a colon inside a model name included, is a
// model served for every profile in profiles. names holds each distinct
// model once, in entry order; byProfile maps each of profiles to the models
// served for it, in entry order.
func DeclaredModels(entries, profiles []string) (names []string, byProfile map[string][]string) {
	byProfile = make(map[string][]string, len(profiles))
	add := func(profile, model string) {
		if !slices.Contains(byProfile[profile], model) {
			byProfile[profile] = append(byProfile[profile], model)
		}
	}
	for _, entry := range entries {
		model, profile := DeclaredEntry(entry, profiles)
		if profile != "" {
			add(profile, model)
		} else {
			for _, p := range profiles {
				add(p, model)
			}
		}
		if !slices.Contains(names, model) {
			names = append(names, model)
		}
	}
	return names, byProfile
}

// NewNative constructs a local-execution inference host from trusted
// declaration state. The model allowlist is explicit because chat@1 has no
// model inventory operation; models are the declaration's entries, each
// served for the profiles DeclaredModels reads from it. A declaration does not
// prove a model is resident; residency remains unreported until a provider
// status route supplies proof. expected authenticates the provider process on
// every transport connection.
func NewNative(name, endpoint string, expected listen.ServerExpectation, models, profiles []string) (*Host, error) {
	if strings.TrimSpace(name) == "" || strings.TrimSpace(endpoint) == "" {
		return nil, errors.New("router: native host needs name and endpoint")
	}
	if len(models) == 0 || slices.Contains(models, "") {
		return nil, errors.New("router: native host needs an explicit model allowlist")
	}
	transport := listen.FrameClient{Endpoint: endpoint, Server: &expected, Timeout: 35 * time.Second, MaxFrame: nativeFrameBytes, Sessions: true}
	h := &Host{Name: name, Base: endpoint, Chat: "abstraction.inference/chat@1", Wire: WireNative,
		Profiles: slices.Clone(profiles), native: &nativeLink{transport: transport}}
	listed, byProfile := DeclaredModels(models, h.HostProfiles())
	h.ScopeModels(byProfile)
	h.installed = func(context.Context, *Host) ([]string, error) { return slices.Clone(listed), nil }
	h.resident = func(context.Context, *Host) ([]string, error) { return nil, errors.New("native residency unreported") }
	return h, nil
}

// NewImageEngine describes models an explicitly composed local image adapter
// can serve. Declared availability does not prove a model is loaded.
func NewImageEngine(name string, models []string) (*Host, error) {
	if strings.TrimSpace(name) == "" || len(models) == 0 || slices.Contains(models, "") {
		return nil, errors.New("router: image engine needs name and explicit models")
	}
	listed := slices.Clone(models)
	h := &Host{Name: name, Chat: "abstraction.inference/image@1", Wire: WireImageEngine, Profiles: []string{ProfileImage}}
	h.installed = func(context.Context, *Host) ([]string, error) { return slices.Clone(listed), nil }
	h.resident = func(context.Context, *Host) ([]string, error) {
		return nil, errors.New("image engine residency unreported")
	}
	return h, nil
}

// ScopeModels names, for each profile h serves, the models h serves for it
// (ROUTE-D9). A model is then served only for the profiles that name it; a
// model no profile names is served for none. Call it before the host is
// shared with a router.
func (h *Host) ScopeModels(byProfile map[string][]string) {
	h.profileModels = make(map[string][]string, len(byProfile))
	for profile, models := range byProfile {
		h.profileModels[profile] = slices.Clone(models)
	}
}

// ServesModel reports whether h serves model for profile by what h itself
// declares: the models ScopeModels named for profile, or, for a host whose
// models carry no profiles of its own declaring, whether h serves profile.
func (h *Host) ServesModel(profile, model string) bool {
	if !has(h.HostProfiles(), profile) {
		return false
	}
	if h.profileModels == nil {
		return true
	}
	return has(h.profileModels[profile], model)
}

// declaredProfiles is, for each of names, the profiles h's declaration names
// it for, in h's profile order; nil when h declares no models per profile.
func (h *Host) declaredProfiles(names []string) map[string][]string {
	if h.profileModels == nil {
		return nil
	}
	out := make(map[string][]string, len(names))
	for _, name := range names {
		profiles := []string{}
		for _, profile := range h.HostProfiles() {
			if has(h.profileModels[profile], name) {
				profiles = append(profiles, profile)
			}
		}
		out[name] = profiles
	}
	return out
}

// NativeTransport is the authenticated transport of a declared native host.
func (h *Host) NativeTransport() (listen.FrameClient, bool) {
	if h.native == nil {
		return listen.FrameClient{}, false
	}
	return h.native.transport, true
}
