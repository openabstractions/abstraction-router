package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	identity "github.com/openabstractions/abstraction-identity"
	router "github.com/openabstractions/abstraction-router/go"
	wire "github.com/openabstractions/abstraction-router/go/abstraction/router"
	"github.com/openabstractions/abstraction-router/go/client"
)

// serveRouter serves r on a fresh endpoint until the test ends and returns a
// client bound to it. install configures the host before Serve.
func serveRouter(t *testing.T, r *router.Router, install func(*Host) error) *client.Client {
	t.Helper()
	if err := identity.CanEver(router.Bound); err != nil {
		t.Skip(err)
	}
	endpoint := endpointFor(t)
	h, err := Listen(endpoint, r)
	if err != nil {
		t.Fatal(err)
	}
	if install != nil {
		if err := install(h); err != nil {
			h.Close()
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.Serve(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	return client.New(endpoint)
}

func unavailable(t *testing.T, op string, err error) {
	t.Helper()
	var refusal *wire.ServiceError
	if !errors.As(err, &refusal) || string(refusal.Code) != CodeUnavailable {
		t.Fatalf("%s on a host without a gate: %v, want %s", op, err, CodeUnavailable)
	}
}

// countedHosted is a router over one hosted server whose model listing counts
// the requests it receives. Nothing is surveyed yet, so every listing the
// count shows was asked for by a call that reached the provider.
func countedHosted(t *testing.T) (*router.Router, *atomic.Int64) {
	t.Helper()
	var listings atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		listings.Add(1)
		w.Write([]byte(`{"data":[{"id":"anthropic/claude-sonnet-5"}]}`))
	}))
	t.Cleanup(upstream.Close)
	return router.New(router.NewHosted("openrouter", upstream.URL+"/api/v1", router.WireOpenAICompatible, "")), &listings
}

// TestHostWithoutGateRefusesEveryOperation: missing policy follows the
// contract default, never universal Allow (ARCHITECTURE.md). A host nobody
// installed a gate on answers every gated operation unavailable (ROUTE-E2,
// ROUTE-E3; RIGHTS-R26), and the provider is never asked: each call asks for
// a fresh listing, and the hosted server receives none.
func TestHostWithoutGateRefusesEveryOperation(t *testing.T) {
	r, listings := countedHosted(t)
	c := serveRouter(t, r, nil)
	_, err := c.Models(true)
	unavailable(t, "Models", err)
	_, err = c.Hosts(true)
	unavailable(t, "Hosts", err)
	_, err = c.Pick(client.PickRequest{Model: "anthropic/claude-sonnet-5", Fresh: true})
	unavailable(t, "Pick", err)
	offer, err := c.Offer(client.OfferRequest{Placement: client.PlacementAny, Fresh: true})
	if err != nil || offer.Outcome != wire.OfferOutcomeUnavailable || len(offer.Offers) != 0 {
		t.Fatalf("Offer on a host without a gate: %+v %v, want unavailable", offer, err)
	}
	if n := listings.Load(); n != 0 {
		t.Fatalf("the hosted server was asked %d times by calls no gate decided", n)
	}
}

// TestLocalHostAnswersItsOwnAccount: the gate serve router-v1 installs
// permits a caller under the service's own account for every router action,
// and the same fresh calls reach the hosted server.
func TestLocalHostAnswersItsOwnAccount(t *testing.T) {
	r, listings := countedHosted(t)
	c := serveRouter(t, r, enableLocalGate)
	if _, err := c.Models(true); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Hosts(true); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Pick(client.PickRequest{Model: "anthropic/claude-sonnet-5", Fresh: true}); err != nil {
		t.Fatal(err)
	}
	offer, err := c.Offer(client.OfferRequest{Placement: client.PlacementAny, Fresh: true})
	if err != nil || offer.Outcome == wire.OfferOutcomeUnavailable {
		t.Fatalf("Offer under the local gate: %+v %v", offer, err)
	}
	if listings.Load() == 0 {
		t.Fatal("no permitted call reached the hosted server")
	}
}

// TestInstalledPolicyDecidesRouterActionsOnly: a permit-all policy installed
// on the host refuses every action outside the router namespace (RIGHTS-A7).
func TestInstalledPolicyDecidesRouterActionsOnly(t *testing.T) {
	if err := identity.CanEver(router.Bound); err != nil {
		t.Skip(err)
	}
	h, err := Listen(endpointFor(t), router.New())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if err := h.EnablePolicy(func(context.Context, *identity.Peer, string, string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"abstraction.resource/hold", "abstraction.routerx/route", "abstraction.router", "app.comfyui/workflow.run"} {
		if err := h.policy.Check(context.Background(), &identity.Peer{}, action, ResourceRoutes); err == nil || errors.Is(err, ErrPolicyUnavailable) {
			t.Fatalf("the installed policy decided %s as %v, want a refusal", action, err)
		}
	}
	if err := h.policy.Check(context.Background(), &identity.Peer{}, ActionRoute, ResourceRoutes); err != nil {
		t.Fatalf("the router's own action read %v", err)
	}
}

// pendingQuestionRefusal is a policy refusal that raised the first-use
// question q1, as the runtime's rights decision reports it.
type pendingQuestionRefusal struct{}

func (pendingQuestionRefusal) Error() string { return "not granted" }
func (pendingQuestionRefusal) PermissionRefusalReason() string {
	return "rights:not_granted rights:question_pending:q1"
}

// TestPolicyRefusalNamesItsPendingQuestion: a router read refused for want of
// a first-use grant names the question it raised (RIGHTS-FLOW ruling 2), a
// refusal without one keeps the bare sentence, and the same read succeeds
// once the person allowed.
func TestPolicyRefusalNamesItsPendingQuestion(t *testing.T) {
	r, _ := countedHosted(t)
	var answer atomic.Int32 // 0 pending question, 1 plain refusal, 2 allowed
	c := serveRouter(t, r, func(h *Host) error {
		return h.EnablePolicy(func(context.Context, *identity.Peer, string, string) error {
			switch answer.Load() {
			case 0:
				return pendingQuestionRefusal{}
			case 1:
				return errors.New("denied")
			}
			return nil
		})
	})
	for _, want := range []string{"router operation not permitted rights:not_granted rights:question_pending:q1", "router operation not permitted"} {
		_, err := c.Models(true)
		var refusal *wire.ServiceError
		if !errors.As(err, &refusal) || string(refusal.Code) != CodeForbidden || refusal.Message != want {
			t.Fatalf("Models refusal %v, want forbidden %q", err, want)
		}
		answer.Add(1)
	}
	if _, err := c.Models(true); err != nil {
		t.Fatalf("Models after Allow: %v", err)
	}
}
