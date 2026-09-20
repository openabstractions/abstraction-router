package router

import (
	"testing"

	identity "github.com/openabstractions/abstraction-identity"
	"github.com/openabstractions/abstraction-identity/listen"
)

func TestNativeRequiresExplicitRoutingAndTrustInputs(t *testing.T) {
	expect := listen.ServerExpectation{Principal: identity.User{Kind: "posix", UID: 1}, Program: "/provider"}
	if _, err := NewNative("", "endpoint", expect, []string{"model"}, []string{ProfileChat}); err == nil {
		t.Fatal("empty name accepted")
	}
	if _, err := NewNative("provider", "endpoint", expect, nil, []string{ProfileChat}); err == nil {
		t.Fatal("missing model allowlist accepted")
	}
	h, err := NewNative("provider", "endpoint", expect, []string{"model"}, []string{ProfileChat})
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := h.NativeTransport()
	if !ok || transport.Server == nil || transport.Endpoint != "endpoint" || !transport.Sessions {
		t.Fatalf("transport %+v, ok %v", transport, ok)
	}
	if h.Hosted || h.Wire != WireNative || h.Endpoint() != "" {
		t.Fatalf("native host %+v", h)
	}
	installed, err := h.installed(h)
	if err != nil || len(installed) != 1 || installed[0] != "model" {
		t.Fatalf("installed %v, %v", installed, err)
	}
}
