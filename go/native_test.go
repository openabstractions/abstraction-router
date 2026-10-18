package router

import (
	"context"
	"strings"
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
	h, err := NewNative("provider", "endpoint", expect, []string{"qwen/qwen3.6-35b-a3b"}, []string{ProfileChat})
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
	installed, err := h.installed(context.Background(), h)
	if err != nil || len(installed) != 1 || installed[0] != "qwen/qwen3.6-35b-a3b" {
		t.Fatalf("installed %v, %v", installed, err)
	}
	resident, err := h.resident(context.Background(), h)
	if len(resident) != 0 || err == nil || !strings.Contains(err.Error(), "unreported") {
		t.Fatalf("unverified residency %v, %v", resident, err)
	}
	routes := New(h)
	routes.Survey()
	decision, _ := routes.Route(Request{Model: "qwen/qwen3.6-35b-a3b", Profile: ProfileChat})
	if decision.Verdict != WouldLoad {
		t.Fatalf("declaration route %s, want potential load", decision.Verdict)
	}
}

func TestNativeImageEndpointRequiresDeclaredProfile(t *testing.T) {
	expect := listen.ServerExpectation{Principal: identity.User{Kind: "posix", UID: 1}, Program: "/provider"}
	image, err := NewNative("image", "endpoint", expect, []string{"stability/sdxl"}, []string{ProfileImage})
	if err != nil {
		t.Fatal(err)
	}
	if image.ImageURL() != "abstraction.inference/image@1" {
		t.Fatalf("image URL %q", image.ImageURL())
	}
	chat, err := NewNative("chat", "endpoint", expect, []string{"qwen/qwen3"}, []string{ProfileChat})
	if err != nil {
		t.Fatal(err)
	}
	if chat.ImageURL() != "" {
		t.Fatalf("chat image URL %q", chat.ImageURL())
	}
}
