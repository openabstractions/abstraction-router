package router

import (
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/openabstractions/abstraction-identity/listen"
)

// WireNative is abstraction.inference served by another local process over
// the shared authenticated native transport.
const WireNative = "oa-native@1"

const nativeFrameBytes = 1 << 20

type nativeLink struct {
	transport listen.FrameClient
}

// NewNative constructs a local-execution inference host from trusted
// declaration state. The model allowlist is explicit because chat@1 has no
// model inventory operation. expected authenticates the provider process on
// every transport connection.
func NewNative(name, endpoint string, expected listen.ServerExpectation, models, profiles []string) (*Host, error) {
	if strings.TrimSpace(name) == "" || strings.TrimSpace(endpoint) == "" {
		return nil, errors.New("router: native host needs name and endpoint")
	}
	if len(models) == 0 || slices.Contains(models, "") {
		return nil, errors.New("router: native host needs an explicit model allowlist")
	}
	transport := listen.FrameClient{Endpoint: endpoint, Server: &expected, Timeout: 35 * time.Second, MaxFrame: nativeFrameBytes, Sessions: true}
	listed := slices.Clone(models)
	h := &Host{Name: name, Base: endpoint, Chat: "abstraction.inference/chat@1", Wire: WireNative,
		Profiles: slices.Clone(profiles), native: &nativeLink{transport: transport}}
	h.installed = func(*Host) ([]string, error) { return slices.Clone(listed), nil }
	h.resident = func(*Host) ([]string, error) { return slices.Clone(listed), nil }
	return h, nil
}

// NativeTransport is the authenticated transport of a declared native host.
func (h *Host) NativeTransport() (listen.FrameClient, bool) {
	if h.native == nil {
		return listen.FrameClient{}, false
	}
	return h.native.transport, true
}
