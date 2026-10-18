package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	identity "github.com/openabstractions/abstraction-identity"
	"github.com/openabstractions/abstraction-identity/listen"
	router "github.com/openabstractions/abstraction-router/go"
	wire "github.com/openabstractions/abstraction-router/go/abstraction/router"
	"github.com/openabstractions/abstraction-router/go/client"
)

// TestOfferOverIPC: Offer crosses the framed service with the offers the
// provider folds, decides abstraction.router/inventory.read, and a caller
// the policy refuses reads a typed not_permitted outcome.
func TestOfferOverIPC(t *testing.T) {
	if err := identity.CanEver(router.Bound); err != nil {
		t.Skip(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"id":"offer-model"}]}`))
	}))
	defer upstream.Close()
	r := router.New(router.NewHosted("openrouter", upstream.URL+"/api/v1", router.WireOpenAICompatible, ""))

	var mu sync.Mutex
	var actions []string
	refuse := false
	unavailable := false
	endpoint := endpointFor(t)
	h, err := Listen(endpoint, r)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.EnablePolicy(func(_ context.Context, _ *identity.Peer, action, resource string) error {
		mu.Lock()
		defer mu.Unlock()
		actions = append(actions, action+" "+resource)
		if unavailable {
			return ErrPolicyUnavailable
		}
		if refuse {
			return errors.New("refused")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.Serve(ctx) }()
	defer func() { cancel(); <-done }()

	c := client.New(endpoint)
	got, err := c.Offer(client.OfferRequest{Profile: "", Placement: client.PlacementHosted, Reserve: 4096, Fresh: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome != wire.OfferOutcomeOffered || len(got.Offers) != 1 {
		t.Fatalf("offers %+v", got.Offers)
	}
	o := got.Offers[0]
	if strings.Join(o.Names, ",") != "offer-model" || strings.Join(o.Servers, ",") != "openrouter" ||
		o.Context != 32768 || o.ContextSource != client.ContextSourceDefault || o.Input != 28672 {
		t.Fatalf("offer %+v", o)
	}
	local, err := c.Offer(client.OfferRequest{Placement: client.PlacementLocal, Reserve: 4096})
	if err != nil || local.Outcome != wire.OfferOutcomeOffered || len(local.Offers) != 0 {
		t.Fatalf("local offers %+v %v", local.Offers, err)
	}
	mu.Lock()
	if len(actions) != 2 || actions[0] != ActionInventory+" "+ResourceInventory {
		t.Fatalf("policy asked %q", actions)
	}
	refuse = true
	mu.Unlock()
	denied, err := c.Offer(client.OfferRequest{Placement: client.PlacementAny})
	if err != nil || denied.Outcome != wire.OfferOutcomeNotPermitted || len(denied.Offers) != 0 || denied.Observation != (wire.Observation{}) {
		t.Fatalf("refused offer: %+v %v", denied, err)
	}
	mu.Lock()
	refuse, unavailable = false, true
	mu.Unlock()
	uncertain, err := c.Offer(client.OfferRequest{Placement: client.PlacementAny})
	if err != nil || uncertain.Outcome != wire.OfferOutcomeUnavailable || len(uncertain.Offers) != 0 {
		t.Fatalf("uncertain policy: %+v %v", uncertain, err)
	}
	mu.Lock()
	unavailable = false
	mu.Unlock()
	invalid, err := c.Offer(client.OfferRequest{Placement: client.PlacementAny, Reserve: -1})
	if err != nil || invalid.Outcome != wire.OfferOutcomeInvalid || len(invalid.Offers) != 0 {
		t.Fatalf("invalid reserve: %+v %v", invalid, err)
	}
}

// TestOfferRefusesPlacementOutsideTheEnum: the generated dispatcher refuses
// a placement word outside the closed enum before the provider is asked.
func TestOfferRefusesPlacementOutsideTheEnum(t *testing.T) {
	asked := false
	dispatch := wire.RouterDispatcher{Handler: answerFunc(func(router.Request) (router.Response, error) {
		asked = true
		return router.Response{}, nil
	})}
	frame := `{"version":1,"service":"abstraction.router/router@1","method":"Offer","arguments":{"request":{"profile":"","placement":"elsewhere","reserve":0,"fresh":false}}}`
	reply, err := dispatch.ExchangeFrame([]byte(frame))
	if err != nil {
		t.Fatal(err)
	}
	if asked || !strings.Contains(string(reply), `"bad_enum"`) {
		t.Fatalf("asked %v, reply %s", asked, reply)
	}
}

// TestOfferUnboundNeverReachesProvider: an Offer without a bound call is
// refused before the provider folds anything.
func TestOfferUnboundNeverReachesProvider(t *testing.T) {
	r := receiver{provider: router.New()}
	_, err := r.views().Offer(wire.OfferRequest{Placement: wire.PlacementAny})
	var refused *wire.ServiceError
	if !errors.As(err, &refused) || refused.Code != router.CodeCallerRefused {
		t.Fatal(err)
	}
}

func TestOfferBusinessRefusalsAreTypedAndEnvelopeFailuresRemainErrors(t *testing.T) {
	for _, test := range []struct {
		code    string
		outcome wire.OfferOutcome
	}{
		{router.CodeInvalidRequest, wire.OfferOutcomeInvalid},
		{CodeForbidden, wire.OfferOutcomeForbidden},
		{CodeUnavailable, wire.OfferOutcomeUnavailable},
		{router.CodeInternal, wire.OfferOutcomeUnavailable},
		{"future_provider_refusal", wire.OfferOutcomeUnavailable},
		{router.CodeCallerRefused, wire.OfferOutcomeUnavailable},
		{string(wire.ServiceErrorCodeUnknownMethod), wire.OfferOutcomeUnavailable},
		{"not_permitted", wire.OfferOutcomeNotPermitted},
	} {
		t.Run(test.code, func(t *testing.T) {
			answer := answerFunc(func(router.Request) (router.Response, error) {
				out := router.Response{Code: test.code, Error: "provider refusal"}
				return out, providerOfferResponseError(out)
			})
			got, err := answer.Offer(wire.OfferRequest{Placement: wire.PlacementAny})
			if err != nil || got.Outcome != test.outcome || len(got.Offers) != 0 || got.Observation != (wire.Observation{}) {
				t.Fatalf("business refusal: %+v %v", got, err)
			}
		})
	}
	for _, code := range []wire.ServiceErrorCode{wire.ServiceErrorCodeCallerRefused, wire.ServiceErrorCodeUnknownMethod, wire.ServiceErrorCodeUnknownVersion} {
		answer := answerFunc(func(router.Request) (router.Response, error) {
			return router.Response{}, &wire.ServiceError{Code: code}
		})
		_, err := answer.Offer(wire.OfferRequest{Placement: wire.PlacementAny})
		var refusal *wire.ServiceError
		if !errors.As(err, &refusal) || refusal.Code != code {
			t.Fatalf("envelope refusal %s: %v", code, err)
		}
	}
}

func TestOfferUnmappedRemoteCallerRemainsABindingError(t *testing.T) {
	_, err := Remote(nil, listen.Seen{}).Offer(wire.OfferRequest{Placement: wire.PlacementAny})
	var refusal *wire.ServiceError
	if !errors.As(err, &refusal) || refusal.Code != router.CodeCallerRefused {
		t.Fatalf("unmapped caller: %v", err)
	}
}
