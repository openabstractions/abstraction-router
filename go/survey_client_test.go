package router

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	identity "github.com/openabstractions/abstraction-identity"
)

func TestSurveyProjectsOnlyLocalEngineProofFailure(t *testing.T) {
	for _, c := range []struct {
		name, wire string
		err        error
		cause      AvailabilityCause
		unverified bool
	}{
		{"unverified local engine", "", identity.ErrNotProven, AvailabilityConfiguration, true},
		{"transport outage", "", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("offline")}, AvailabilityTransportUnavailable, false},
		{"unrelated error text", "", errors.New("enginehttp: unexpected engine peer"), AvailabilityUnknown, false},
		{"native provider", WireNative, identity.ErrNotProven, AvailabilityUnknown, false},
		{"remote provider", WireRemote, identity.ErrNotProven, AvailabilityUnknown, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			host := LMStudio("http://127.0.0.1:1")
			host.Wire = c.wire
			host.installed = func(context.Context, *Host) ([]string, error) { return nil, c.err }
			state := New(host).readHost(t.Context(), host).state
			if state.Up || state.AvailabilityCause != c.cause || (state.Why == "engine:unverified:lmstudio") != c.unverified {
				t.Fatalf("failed listing cause: %+v", state)
			}
		})
	}
}

type surveyRoundTripper func(*http.Request) (*http.Response, error)

func (f surveyRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestSurveyHTTPClientIsScopedAndKeepsDeadline(t *testing.T) {
	var normal, supplied atomic.Int64
	var posted atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		normal.Add(1)
		w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	custom := &http.Client{Transport: surveyRoundTripper(func(req *http.Request) (*http.Response, error) {
		supplied.Add(1)
		if at, ok := req.Context().Deadline(); !ok || at.After(deadline) {
			t.Error("survey lost caller deadline", at, ok)
		}
		body := `{"data":[]}`
		if req.URL.Path == "/api/tags" {
			body = `{"models":[{"name":"fixture"}]}`
		}
		if req.URL.Path == "/api/ps" {
			body = `{"models":[]}`
		}
		if req.Method == http.MethodPost {
			posted.Store(true)
			body = `{"capabilities":["completion"]}`
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	r := NewWithHTTPClient(custom, LMStudio(server.URL))
	if err := r.SurveyContext(ctx); err != nil {
		t.Fatal(err)
	}
	states, _, _, _, _ := r.Residency(false)
	if len(states) != 1 || !states[0].Up || supplied.Load() == 0 || normal.Load() != 0 {
		t.Fatal(states, supplied.Load(), normal.Load())
	}
	if err := NewWithHTTPClient(custom, Ollama(server.URL)).SurveyContext(ctx); err != nil {
		t.Fatal(err)
	}
	if !posted.Load() || normal.Load() != 0 {
		t.Fatal("read-only metadata POST bypassed supplied client", posted.Load(), normal.Load())
	}
	for _, ordinary := range []*Router{New(LMStudio(server.URL)), NewWithHTTPClient(nil, LMStudio(server.URL))} {
		if err := ordinary.SurveyContext(ctx); err != nil {
			t.Fatal(err)
		}
		states, _, _, _, _ := ordinary.Residency(false)
		if len(states) != 1 || !states[0].Up {
			t.Fatal("default construction changed", states)
		}
	}
	if normal.Load() == 0 {
		t.Fatal("custom client escaped its router instance")
	}
}

func TestSurveyHTTPClientKeepsCancellationAndRedirectPolicy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	custom := &http.Client{Transport: surveyRoundTripper(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	})}
	if err := NewWithHTTPClient(custom, LMStudio("http://127.0.0.1:1")).SurveyContext(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("survey lost cancellation", err)
	}
	var forwarded atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { forwarded.Add(1); w.Write([]byte(`{"data":[]}`)) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer redirect.Close()
	custom = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r := NewWithHTTPClient(custom, LMStudio(redirect.URL))
	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := r.SurveyContext(ctx); err != nil {
		t.Fatal(err)
	}
	states, _, _, _, _ := r.Residency(false)
	if len(states) != 1 || states[0].Up || forwarded.Load() != 0 {
		t.Fatal("survey changed supplied redirect policy", states, forwarded.Load())
	}
}
