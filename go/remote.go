package router

import (
	"context"
	"crypto/tls"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/openabstractions/abstraction-identity/listen"
	"github.com/openabstractions/abstraction-identity/remote"
	wire "github.com/openabstractions/abstraction-router/go/abstraction/router"
)

// WireRemote is another runtime reached over the mutual-TLS remote transport
// (research/inference-registration/DECISION.md §5).
const WireRemote = "oa-remote@1"

// remoteFrameBytes bounds one remote frame; a router snapshot of a busy
// runtime fits.
const remoteFrameBytes = 8 << 20

// remoteRouter is the part of a remote runtime's router@1 this runtime reads.
type remoteRouter interface {
	Models(fresh bool) (wire.ModelsSnapshot, error)
	Hosts(fresh bool) (wire.HostsSnapshot, error)
	Offer(request wire.OfferRequest) (wire.OfferSnapshot, error)
}

// remoteLink is the transport to a remote runtime.
type remoteLink struct {
	transport listen.FrameClient
	// client is the remote's router@1 for one operation's ctx.
	client func(ctx context.Context) remoteRouter
	mu     sync.Mutex
	// contexts is each servable remote name's context observation from the
	// last listing, the smallest bound across the remote's own aliases of
	// that name.
	contexts map[string]modelContext
	// profiles is each servable remote name's profiles from the last
	// listing, the union across the remote's own aliases of that name. A name
	// one of whose aliases reports no profiles is absent: it is judged by the
	// profiles this runtime declared for the remote.
	profiles map[string][]string
}

// NewRemote is a remote runtime under explicit trust: address is host:port,
// config carries the trusted roots, server name and this runtime's client
// certificate. credential names the credential the remote applies to the
// requests it performs for this runtime; it is never applied or carried here.
// The remote's router@1 Models and Hosts are read through the transport, and
// its hosts are listed as <name>/<host> in its domain. Its aliases carry the
// context the remote reports for them, and an offer asks the remote's own
// router@1 Offer (ROUTE-S4).
func NewRemote(name, address string, config *tls.Config, credential string) (*Host, error) {
	transport, err := remote.Client(address, config, 30*time.Second, remoteFrameBytes)
	if err != nil {
		return nil, err
	}
	h := remoteHost(name, address, credential, func(ctx context.Context) remoteRouter {
		return wire.NewRouterClient(transport.WithContext(ctx))
	})
	h.remote.transport = transport
	return h, nil
}

// remoteHost is a remote runtime host reading router@1 through client.
func remoteHost(name, address, credential string, client func(context.Context) remoteRouter) *Host {
	h := &Host{Name: name, Base: "tls://" + address, Chat: "abstraction.inference/chat@1", Hosted: true, Wire: WireRemote, Credential: credential, Domain: name,
		remote:   &remoteLink{client: client},
		resident: func(context.Context, *Host) ([]string, error) { return nil, nil }}
	h.listing = func(ctx context.Context, h *Host, _ map[string]string) ([]string, error) {
		models, err := h.remote.client(ctx).Models(false)
		if err != nil {
			return nil, errors.New("remote " + address + ": " + err.Error())
		}
		var out []string
		contexts := map[string]modelContext{}
		profiles := map[string][]string{}
		unreported := map[string]bool{}
		for _, f := range models.Models {
			for _, a := range f.Names {
				if !a.Servable {
					continue
				}
				if !has(out, a.Name) {
					out = append(out, a.Name)
				}
				observed := modelContext{maximum: a.ContextLength, effective: a.EffectiveContextLength}
				if prior, seen := contexts[a.Name]; !seen || smallerBound(observed, prior) {
					contexts[a.Name] = observed
				}
				if len(a.Profiles) == 0 {
					unreported[a.Name] = true
				}
				for _, p := range a.Profiles {
					if !has(profiles[a.Name], p) {
						profiles[a.Name] = append(profiles[a.Name], p)
					}
				}
			}
		}
		for name := range unreported {
			delete(profiles, name)
		}
		h.remote.mu.Lock()
		h.remote.contexts = contexts
		h.remote.profiles = profiles
		h.remote.mu.Unlock()
		return out, nil
	}
	// A remote alias serves the profiles its own router reports for it, among
	// those this runtime declared for the remote; a name the remote reports
	// no profiles for serves every declared profile (ROUTE-D9). Every name
	// gets an entry, so no reported name hides an unreported one.
	h.modelProfiles = func(_ context.Context, h *Host, names []string) map[string][]string {
		h.remote.mu.Lock()
		defer h.remote.mu.Unlock()
		declared := h.HostProfiles()
		out := make(map[string][]string, len(names))
		for _, name := range names {
			reported, ok := h.remote.profiles[name]
			if !ok {
				out[name] = slices.Clone(declared)
				continue
			}
			kept := []string{}
			for _, p := range reported {
				if has(declared, p) {
					kept = append(kept, p)
				}
			}
			out[name] = kept
		}
		return out
	}
	h.modelContexts = func(_ context.Context, h *Host, names []string) map[string]modelContext {
		h.remote.mu.Lock()
		defer h.remote.mu.Unlock()
		out := make(map[string]modelContext, len(names))
		for _, name := range names {
			if c, ok := h.remote.contexts[name]; ok {
				out[name] = c
			}
		}
		return out
	}
	return h
}

// bound is the per-request limit an observation supports: the effective
// length when valid, else the maximum, else 0 for unknown.
func (c modelContext) bound() int64 {
	if effective := validEffective(c.effective, c.maximum); effective > 0 {
		return effective
	}
	return c.maximum
}

// smallerBound reports whether c is a tighter known bound than prior.
func smallerBound(c, prior modelContext) bool {
	b, p := c.bound(), prior.bound()
	return b > 0 && (p == 0 || b < p)
}

// RemoteTransport is the transport of a remote runtime host.
func (h *Host) RemoteTransport() (listen.FrameClient, bool) {
	if h.remote == nil {
		return listen.FrameClient{}, false
	}
	return h.remote.transport, true
}

// remoteHosts reads the remote runtime's hosts, named in its domain.
func (h *Host) remoteHosts(ctx context.Context) ([]HostState, error) {
	snapshot, err := h.remote.client(ctx).Hosts(false)
	if err != nil {
		return nil, err
	}
	var out []HostState
	for _, s := range snapshot.Hosts {
		var cause AvailabilityCause
		if s.AvailabilityCause != nil {
			cause = AvailabilityCause(*s.AvailabilityCause)
		}
		out = append(out, HostState{Host: h.Name + "/" + s.Host, Base: s.Base, Up: s.Up, Why: s.Why, Installed: int(s.Installed), Resident: s.Resident,
			Servable: s.Servable, Hosted: s.Hosted, Wire: s.Wire, Credential: s.Credential, DeclaredBy: s.DeclaredBy, Profiles: s.Profiles, Domain: h.Name, AvailabilityCause: cause})
	}
	return out, nil
}

// remoteOffer asks the remote runtime's own router@1 Offer.
func (h *Host) remoteOffer(ctx context.Context, request wire.OfferRequest) (wire.OfferSnapshot, error) {
	return h.remote.client(ctx).Offer(request)
}
