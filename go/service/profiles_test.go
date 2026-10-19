package service

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	identity "github.com/openabstractions/abstraction-identity"
	router "github.com/openabstractions/abstraction-router/go"
	wire "github.com/openabstractions/abstraction-router/go/abstraction/router"
	"github.com/openabstractions/abstraction-router/go/client"
)

func TestModalitiesGeneratedDispatchAndRemoteAuthority(t *testing.T) {
	caller := remoteCaller(t, "profiles key")
	provider := router.New(&router.Host{Profiles: []string{"org/custom@1"}})
	called := false
	policy := func(_ context.Context, _ RemoteCaller, action, resource string) error {
		called = true
		if action != ActionInventory || resource != ResourceInventory {
			t.Fatalf("authority %s %s", action, resource)
		}
		return nil
	}
	dispatch := wire.RouterDispatcher{Handler: Remote(context.Background(), provider, caller, policy)}
	frame := `{"version":1,"service":"abstraction.router/router@1","method":"Modalities","arguments":{"cursor":"","limit":64}}`
	reply, err := dispatch.ExchangeFrame([]byte(frame))
	if err != nil || !called || !strings.Contains(string(reply), `"org/custom@1"`) || !strings.Contains(string(reply), `"outcome": "page"`) {
		t.Fatalf("dispatch %s %v", reply, err)
	}
	for _, policy := range []RemotePolicy{nil, func(context.Context, RemoteCaller, string, string) error { return errors.New("denied") }, func(context.Context, RemoteCaller, string, string) error { return ErrPolicyUnavailable }} {
		got, err := Remote(context.Background(), nil, caller, policy).Modalities("", 64)
		if err != nil || (got.Outcome != wire.ModalityOutcomeUnavailable && got.Outcome != wire.ModalityOutcomeNotPermitted) || len(got.Modalities) != 0 || got.Next != "" || got.Complete {
			t.Fatalf("refusal %+v %v", got, err)
		}
	}
	got, err := Remote(context.Background(), nil, caller, func(context.Context, RemoteCaller, string, string) error { return nil }).Modalities("", 64)
	if err != nil || got.Outcome != wire.ModalityOutcomeUnavailable || len(got.Modalities) != 0 || got.Next != "" || got.Complete {
		t.Fatalf("missing provider %+v %v", got, err)
	}
	_, err = (&receiver{provider: provider}).views().Modalities("", 64)
	var refused *wire.ServiceError
	if !errors.As(err, &refused) || refused.Code != router.CodeCallerRefused {
		t.Fatalf("unbound %v", err)
	}
}

func TestModalitiesRefusesUnrepresentableProviderPage(t *testing.T) {
	for _, page := range []router.ModalityPage{
		{Outcome: "page", Modalities: make([]string, 65)},
		{Outcome: "page", Next: strings.Repeat("x", 257)},
		{Outcome: "page", Next: "\xff"},
		{Outcome: "page", Modalities: []string{strings.Repeat("x", 257)}},
		{Outcome: "page", Modalities: []string{""}},
	} {
		answer := answerFunc(func(router.Request) (router.Response, error) { return router.Response{ModalityPage: &page}, nil })
		got, err := answer.Modalities("", 64)
		if err != nil || got.Outcome != wire.ModalityOutcomeUnavailable || len(got.Modalities) != 0 || got.Next != "" {
			t.Fatalf("invalid provider page disclosed: %+v %v", got, err)
		}
	}
}

func TestModalitiesNativeService(t *testing.T) {
	if err := identity.CanEver(router.Bound); err != nil {
		t.Skip(err)
	}
	endpoint := endpointFor(t)
	provider := router.New()
	h, err := Listen(endpoint, provider)
	if err != nil {
		t.Fatal(err)
	}
	var state atomic.Int32
	if err = h.EnablePolicy(func(_ context.Context, _ *identity.Peer, action, resource string) error {
		if action != ActionInventory || resource != ResourceInventory {
			return errors.New("wrong action")
		}
		switch state.Load() {
		case 1:
			return errors.New("denied")
		case 2:
			return ErrPolicyUnavailable
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.Serve(ctx) }()
	defer func() { cancel(); <-done }()
	got, err := client.New(endpoint).Modalities("", 64)
	if err != nil || got.Outcome != wire.ModalityOutcomePage || len(got.Modalities) != len(router.SeedProfiles) {
		t.Fatalf("native %+v %v", got, err)
	}
	provider.SetHosts(&router.Host{Profiles: []string{"org/custom@1"}})
	first, err := client.New(endpoint).Modalities("", 1)
	if err != nil || first.Next == "" {
		t.Fatalf("first %+v %v", first, err)
	}
	provider.SetHosts()
	gap, err := client.New(endpoint).Modalities(first.Next, 1)
	if err != nil || gap.Outcome != wire.ModalityOutcomeGap || len(gap.Modalities) != 0 {
		t.Fatalf("gap %+v %v", gap, err)
	}
	for i, want := range []wire.ModalityOutcome{wire.ModalityOutcomeNotPermitted, wire.ModalityOutcomeUnavailable} {
		state.Store(int32(i + 1))
		got, err := client.New(endpoint).Modalities("", 64)
		if err != nil || got.Outcome != want || len(got.Modalities) != 0 || got.Next != "" || got.Complete {
			t.Fatalf("authority %+v %v", got, err)
		}
	}
}
