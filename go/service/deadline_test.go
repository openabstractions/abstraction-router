package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	identity "github.com/openabstractions/abstraction-identity"
	router "github.com/openabstractions/abstraction-router/go"
	"github.com/openabstractions/abstraction-router/go/client"
)

// TestFreshPickHonoursCallDeadline (router P0-3): a hosted server whose
// listing takes 60 s does not hold a fresh Pick past the service's 10 s call
// deadline. The answer is a typed unavailable, and the previous snapshot stays
// readable.
func TestFreshPickHonoursCallDeadline(t *testing.T) {
	if err := identity.CanEver(router.Bound); err != nil {
		t.Skip(err)
	}
	var slow atomic.Bool
	var slowSeen atomic.Int32
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if slow.Load() {
			slowSeen.Add(1)
			select {
			case <-time.After(60 * time.Second):
			case <-release:
			case <-r.Context().Done():
			}
		}
		w.Write([]byte(`{"data":[{"id":"anthropic/claude-sonnet-5"}]}`))
	}))
	defer upstream.Close()
	defer close(release)
	r := router.New(router.NewHosted("openrouter", upstream.URL+"/api/v1", router.WireOpenAICompatible, ""))
	r.Survey()
	endpoint := endpointFor(t)
	h, err := Listen(endpoint, r)
	if err != nil {
		t.Fatal(err)
	}
	// Without a gate every gated call refuses as unavailable (cq3a), which
	// would satisfy the deadline assertion without reaching the survey.
	if err := enableLocalGate(h); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.Serve(ctx) }()
	defer func() { cancel(); <-done }()

	slow.Store(true)
	c := client.New(endpoint)
	callCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	start := time.Now()
	_, err = client.New(endpoint).PickContext(callCtx, client.PickRequest{Model: "anthropic/claude-sonnet-5", Fresh: true})
	took := time.Since(start)
	if took > 11*time.Second {
		t.Fatalf("fresh pick took %v, past the 10 s call deadline", took)
	}
	var refusal *client.ServiceError
	if !errors.As(err, &refusal) || string(refusal.Code) != "unavailable" {
		t.Fatalf("fresh pick past the deadline: %v, want a typed unavailable", err)
	}
	// The unavailable answer counts only when the Pick reached the slow
	// listing; an early refusal for another reason would pass the checks above.
	if slowSeen.Load() == 0 {
		t.Fatalf("fresh pick answered unavailable after %v without reaching the slow listing", took)
	}
	models, err := c.Models(false)
	if err != nil || len(models.Models) != 1 {
		t.Fatalf("previous snapshot: %+v, %v", models.Models, err)
	}
}
