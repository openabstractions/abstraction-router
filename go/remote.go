package router

import (
	"context"
	"crypto/tls"
	"errors"
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

// remoteLink is the transport to a remote runtime.
type remoteLink struct {
	transport listen.FrameClient
}

// NewRemote is a remote runtime under explicit trust: address is host:port,
// config carries the trusted roots, server name and this runtime's client
// certificate. credential names the credential the remote applies to the
// requests it performs for this runtime; it is never applied or carried here.
// The remote's router@1 Models and Hosts are read through the transport, and
// its hosts are listed as <name>/<host> in its domain.
func NewRemote(name, address string, config *tls.Config, credential string) (*Host, error) {
	transport, err := remote.Client(address, config, 30*time.Second, remoteFrameBytes)
	if err != nil {
		return nil, err
	}
	h := &Host{Name: name, Base: "tls://" + address, Chat: "abstraction.inference/chat@1", Hosted: true, Wire: WireRemote, Credential: credential, Domain: name,
		remote:   &remoteLink{transport: transport},
		resident: func(*Host) ([]string, error) { return nil, nil }}
	h.listing = func(ctx context.Context, h *Host, _ map[string]string) ([]string, error) {
		models, err := wire.NewRouterClient(h.remote.transport.WithContext(ctx)).Models(false)
		if err != nil {
			return nil, errors.New("remote " + address + ": " + err.Error())
		}
		var out []string
		for _, f := range models.Models {
			for _, a := range f.Names {
				if a.Servable && !has(out, a.Name) {
					out = append(out, a.Name)
				}
			}
		}
		return out, nil
	}
	return h, nil
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
	snapshot, err := wire.NewRouterClient(h.remote.transport.WithContext(ctx)).Hosts(false)
	if err != nil {
		return nil, err
	}
	var out []HostState
	for _, s := range snapshot.Hosts {
		out = append(out, HostState{Host: h.Name + "/" + s.Host, Base: s.Base, Up: s.Up, Why: s.Why, Installed: int(s.Installed), Resident: s.Resident,
			Servable: s.Servable, Hosted: s.Hosted, Wire: s.Wire, Credential: s.Credential, DeclaredBy: s.DeclaredBy, Profiles: s.Profiles, Domain: h.Name})
	}
	return out, nil
}
