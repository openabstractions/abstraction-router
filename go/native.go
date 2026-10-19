package router

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	facade "github.com/openabstractions/abstraction-facade/go-core/go/abstraction/facade"
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
	// loaded is the context each resident model was loaded with, as the
	// provider's last description stated it.
	mu     sync.Mutex
	loaded map[string]int64
}

func (l *nativeLink) setLoaded(contexts map[string]int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.loaded = contexts
}

func (l *nativeLink) loadedContext(name string) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.loaded[name]
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
// prove a model is resident: the survey reads residency from the provider's
// own endpoint description (ResidentCapability), and a provider that states
// none leaves it unreported. expected authenticates the provider process on
// every transport connection.
func NewNative(name, endpoint string, expected listen.ServerExpectation, models, profiles []string, options ...NativeOption) (*Host, error) {
	if strings.TrimSpace(name) == "" || strings.TrimSpace(endpoint) == "" {
		return nil, errors.New("router: native host needs name and endpoint")
	}
	if len(models) == 0 || slices.Contains(models, "") {
		return nil, errors.New("router: native host needs an explicit model allowlist")
	}
	transport := listen.FrameClient{Endpoint: endpoint, Server: &expected, Timeout: 35 * time.Second, MaxFrame: nativeFrameBytes, Sessions: true}
	h := &Host{Name: name, Base: endpoint, Chat: "abstraction.inference/chat@1", Wire: WireNative,
		Profiles: slices.Clone(profiles), native: &nativeLink{transport: transport}}
	for _, option := range options {
		if option.apply != nil {
			option.apply(h)
		}
	}
	listed, byProfile := DeclaredModels(models, h.HostProfiles())
	h.ScopeModels(byProfile)
	h.installed = func(context.Context, *Host) ([]string, error) { return slices.Clone(listed), nil }
	h.resident = func(ctx context.Context, h *Host) ([]string, error) {
		names, contexts, err := describeResidents(ctx, transport, h.Chat)
		h.native.setLoaded(contexts)
		if err != nil {
			return nil, err
		}
		// A provider states residency only for models it declared
		// (ROUTE-D14); any other name is left out.
		declared := []string{}
		for _, name := range names {
			if slices.Contains(listed, name) {
				declared = append(declared, name)
			}
		}
		return declared, nil
	}
	// The provider states each model's capabilities on its chat@3 service
	// (CapabilityFacts); a provider without that service states none.
	h.ReportCapabilities(func(ctx context.Context, names []string) map[string]*ModelCapabilities {
		return describeCapabilities(ctx, transport)
	})
	return h, nil
}

// NativeOption configures a native host before NewNative publishes it.
type NativeOption struct{ apply func(*Host) }

// WithSurveyReady supplies the native provider owner's live readiness for
// passive endpoint surveys. A false result keeps its declared models routable
// and skips transport probes. A nil callback keeps the default behavior.
func WithSurveyReady(ready func() bool) NativeOption {
	return NativeOption{apply: func(h *Host) { h.surveyReady = ready }}
}

// describeResidents reads a provider's stated residency; tests replace it.
var describeResidents = describedResidents

// ResidentCapability is the display fact a native provider's endpoint
// description states on its chat service for the models it holds loaded
// now: the count under this key and each name under "resident.<n>", n from
// 1. A provider without the key reports no residency.
const ResidentCapability = "resident"

// residentLimit bounds the names one description states; the endpoint's
// capability map holds 32 entries.
const residentLimit = 16

// residentReadBudget bounds the survey's description call to one provider.
const residentReadBudget = 3 * time.Second

// ResidentCapabilities is the capability map stating names as resident, at
// most residentLimit of them in order; a name longer than a display value
// is left out.
func ResidentCapabilities(names []string) map[string]string {
	out := map[string]string{}
	count := 0
	for _, name := range names {
		if count == residentLimit {
			break
		}
		if name == "" || len(name) > 256 {
			continue
		}
		count++
		out[ResidentCapability+"."+strconv.Itoa(count)] = name
	}
	out[ResidentCapability] = strconv.Itoa(count)
	return out
}

// ResidentContextCapability follows a resident's key: "resident.<n>.context"
// states the per-conversation context that resident model was loaded with.
const ResidentContextCapability = "context"

// ResidentContextCapabilities is ResidentCapabilities with, beside each
// stated name whose context is known, that context, while the map stays
// within the endpoint's 32 entries.
func ResidentContextCapabilities(names []string, contexts map[string]int64) map[string]string {
	out := ResidentCapabilities(names)
	count, _ := strconv.Atoi(out[ResidentCapability])
	for i := 1; i <= count && len(out) < 32; i++ {
		key := ResidentCapability + "." + strconv.Itoa(i)
		if tokens := contexts[out[key]]; tokens > 0 {
			out[key+"."+ResidentContextCapability] = strconv.FormatInt(tokens, 10)
		}
	}
	return out
}

// ParseResidentContextCapabilities reads the contexts
// ResidentContextCapabilities stated, by resident name; a missing or
// malformed value states none.
func ParseResidentContextCapabilities(capabilities map[string]string) map[string]int64 {
	count, err := strconv.Atoi(capabilities[ResidentCapability])
	if err != nil || count < 0 || count > residentLimit {
		return nil
	}
	out := map[string]int64{}
	for i := 1; i <= count; i++ {
		key := ResidentCapability + "." + strconv.Itoa(i)
		name := capabilities[key]
		tokens, err := strconv.ParseInt(capabilities[key+"."+ResidentContextCapability], 10, 64)
		if name != "" && err == nil && tokens > 0 {
			out[name] = tokens
		}
	}
	return out
}

// ParseResidentCapabilities reads the names ResidentCapabilities stated;
// ok is false when capabilities state no residency.
func ParseResidentCapabilities(capabilities map[string]string) (names []string, ok bool) {
	count, err := strconv.Atoi(capabilities[ResidentCapability])
	if err != nil || count < 0 || count > residentLimit {
		return nil, false
	}
	names = []string{}
	for i := 1; i <= count; i++ {
		if name := capabilities[ResidentCapability+"."+strconv.Itoa(i)]; name != "" && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names, true
}

// describedResidents asks the provider for its endpoint description and
// reads the residency its chat service states, with each resident's loaded
// context where it states one.
func describedResidents(ctx context.Context, transport listen.FrameClient, chat string) ([]string, map[string]int64, error) {
	ctx, cancel := context.WithTimeout(ctx, residentReadBudget)
	defer cancel()
	description, err := facade.NewEndpointClient(transport.WithContext(ctx)).Describe()
	if err != nil {
		return nil, nil, fmt.Errorf("native residency unreported: %w", err)
	}
	for _, service := range description.Services {
		if service.Contract != chat {
			continue
		}
		if names, ok := ParseResidentCapabilities(service.Capabilities); ok {
			return names, ParseResidentContextCapabilities(service.Capabilities), nil
		}
	}
	return nil, nil, errors.New("native residency unreported")
}

// ChatV3Contract is the chat@3 service whose description states a native
// provider's model capabilities.
const ChatV3Contract = "abstraction.inference/chat@3"

// describeCapabilities reads a provider's stated model capabilities; tests
// replace it.
var describeCapabilities = describedCapabilities

func describedCapabilities(ctx context.Context, transport listen.FrameClient) map[string]*ModelCapabilities {
	ctx, cancel := context.WithTimeout(ctx, residentReadBudget)
	defer cancel()
	description, err := facade.NewEndpointClient(transport.WithContext(ctx)).Describe()
	if err != nil {
		return nil
	}
	for _, service := range description.Services {
		if service.Contract == ChatV3Contract {
			return ParseCapabilityFacts(service.Capabilities)
		}
	}
	return nil
}

// ReportContexts sets how the survey reads the context one conversation gets
// on each of h's names: the request limit its provider applies to the next
// load, reported as the alias's effective context (ROUTE-D8). A name read
// as zero or absent reports none. The survey reads it with the installed
// names, also while residency is unreported. Call it before the host is
// shared with a router.
func (h *Host) ReportContexts(read func(ctx context.Context, names []string) map[string]int64) {
	if read == nil {
		h.modelContexts = nil
		return
	}
	h.modelContexts = func(ctx context.Context, h *Host, names []string) map[string]modelContext {
		out := map[string]modelContext{}
		for name, tokens := range read(ctx, names) {
			if tokens > 0 && has(names, name) {
				out[name] = modelContext{effective: tokens}
			}
		}
		// A loaded model's context is the one its provider loaded it
		// with, which can be smaller than the plan when memory was held by
		// another engine; that is what a request may use.
		if h.native != nil {
			for _, name := range names {
				if tokens := h.native.loadedContext(name); tokens > 0 {
					out[name] = modelContext{effective: tokens}
				}
			}
		}
		return out
	}
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
