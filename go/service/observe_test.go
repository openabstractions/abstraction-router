package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	identity "github.com/openabstractions/abstraction-identity"
	router "github.com/openabstractions/abstraction-router/go"
	wire "github.com/openabstractions/abstraction-router/go/abstraction/router"
	"github.com/openabstractions/abstraction-router/go/client"
)

// TestObserveOfferOverIPC: ObserveOffer crosses the framed service, decides
// what Offer decides, answers a changed reading once a survey finds it, waits
// past the ordinary ten-second call when asked to, and refuses as Offer does
// with an empty cursor.
func TestObserveOfferOverIPC(t *testing.T) {
	if err := identity.CanEver(router.Bound); err != nil {
		t.Skip(err)
	}
	var modelsMu sync.Mutex
	models := []string{"first-model"}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelsMu.Lock()
		defer modelsMu.Unlock()
		ids := make([]string, len(models))
		for i, m := range models {
			ids[i] = fmt.Sprintf(`{"id":%q}`, m)
		}
		fmt.Fprintf(w, `{"data":[%s]}`, strings.Join(ids, ","))
	}))
	defer upstream.Close()
	r := router.New(router.NewHosted("openrouter", upstream.URL+"/api/v1", router.WireOpenAICompatible, ""))
	r.Survey()

	var mu sync.Mutex
	var actions []string
	refuse := false
	endpoint := endpointFor(t)
	h, err := Listen(endpoint, r)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.EnablePolicy(func(_ context.Context, _ *identity.Peer, action, resource string) error {
		mu.Lock()
		defer mu.Unlock()
		actions = append(actions, action+" "+resource)
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
	request := client.OfferRequest{Placement: client.PlacementHosted, Reserve: 4096}
	first, err := c.ObserveOffer(request, "", 0)
	if err != nil || first.Outcome != wire.OfferOutcomeOffered || first.Cursor == "" || len(first.Offers) != 1 {
		t.Fatalf("first observation %+v %v", first, err)
	}
	mu.Lock()
	if len(actions) != 1 || actions[0] != ActionInventory+" "+ResourceInventory {
		t.Fatalf("policy asked %q, want what Offer asks", actions)
	}
	mu.Unlock()

	changed := make(chan client.OfferObservation, 1)
	go func() {
		got, err := c.ObserveOffer(request, first.Cursor, 20000)
		if err != nil {
			t.Error(err)
		}
		changed <- got
	}()
	time.Sleep(100 * time.Millisecond)
	modelsMu.Lock()
	models = append(models, "second-model")
	modelsMu.Unlock()
	r.Survey()
	select {
	case got := <-changed:
		if got.Outcome != wire.OfferOutcomeOffered || got.Cursor == first.Cursor || len(got.Offers) != 2 {
			t.Fatalf("changed observation %+v", got)
		}
		first = got
	case <-time.After(10 * time.Second):
		t.Fatal("a survey that changed the offers did not answer the wait")
	}

	if !testing.Short() {
		// The ordinary call is ten seconds; an unchanged wait outlasts it.
		start := time.Now()
		same, err := c.ObserveOffer(request, first.Cursor, 11500)
		if err != nil || same.Outcome != wire.OfferOutcomeOffered || same.Cursor != first.Cursor {
			t.Fatalf("long unchanged wait %+v %v", same, err)
		}
		if waited := time.Since(start); waited < 10*time.Second {
			t.Fatalf("an unchanged wait answered after %v", waited)
		}
	}

	invalid, err := c.ObserveOffer(request, first.Cursor, 30001)
	if err != nil || invalid.Outcome != wire.OfferOutcomeInvalid || invalid.Cursor != "" || len(invalid.Offers) != 0 {
		t.Fatalf("invalid wait %+v %v", invalid, err)
	}
	mu.Lock()
	refuse = true
	mu.Unlock()
	denied, err := c.ObserveOffer(request, "", 0)
	if err != nil || denied.Outcome != wire.OfferOutcomeNotPermitted || denied.Cursor != "" || len(denied.Offers) != 0 || denied.Observation != (wire.Observation{}) {
		t.Fatalf("refused observation %+v %v", denied, err)
	}
}

// The route right alone reads no offers: ObserveOffer, like Offer, decides
// inventory.read (ROUTE-A2), locally and from a paired runtime.
func TestRouteAloneIsRefusedObserveOffer(t *testing.T) {
	routeOnly := func(action, resource string) error {
		if action == ActionRoute && resource == ResourceRoutes {
			return nil
		}
		return errors.New("not granted")
	}
	r := router.New()
	remote := Remote(context.Background(), r, remoteCaller(t, "route only"), func(_ context.Context, _ RemoteCaller, action, resource string) error {
		return routeOnly(action, resource)
	})
	got, err := remote.ObserveOffer(wire.OfferRequest{Placement: wire.PlacementAny}, "", 0)
	if err != nil || got.Outcome != wire.OfferOutcomeNotPermitted || got.Cursor != "" || len(got.Offers) != 0 {
		t.Fatalf("remote ObserveOffer with route alone: %+v %v", got, err)
	}
}

// Every served operation names its right; any other is refused, so a later
// operation never inherits a read right by default.
func TestEveryServedOperationNamesItsRight(t *testing.T) {
	for op, want := range map[string]string{
		router.OpModels: ActionInventory, router.OpResidency: ActionInventory, router.OpOffer: ActionInventory,
		router.OpObserveOffer: ActionInventory, router.OpModalities: ActionInventory, router.OpRoute: ActionRoute,
	} {
		if action, _, known := opRight(op); !known || action != want {
			t.Errorf("%s decides %q (known %v), want %q", op, action, known, want)
		}
	}
	if _, _, known := opRight("unload"); known {
		t.Fatal("an unlisted operation was given a right")
	}
}
