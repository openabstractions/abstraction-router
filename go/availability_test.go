package router

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHostedListingAvailabilityCause(t *testing.T) {
	for _, tc := range []struct {
		status int
		cause  AvailabilityCause
	}{
		{http.StatusUnauthorized, AvailabilityCredential},
		{http.StatusForbidden, AvailabilityRights},
		{http.StatusBadRequest, AvailabilityMalformed},
		{http.StatusBadGateway, AvailabilityTransportUnavailable},
		{http.StatusServiceUnavailable, AvailabilityTransportUnavailable},
		{http.StatusGatewayTimeout, AvailabilityTransportUnavailable},
		{http.StatusTooManyRequests, AvailabilityUnknown},
	} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer server.Close()
			r := New(NewHosted("hosted", server.URL, WireOpenAICompatible, ""))
			r.Survey()
			hosts, _, _, _, _ := r.Residency(false)
			if len(hosts) != 1 || hosts[0].Up || hosts[0].AvailabilityCause != tc.cause {
				t.Fatalf("status %d: %+v", tc.status, hosts)
			}
		})
	}
}

func TestRouteContextCancelsHostedListingWithoutReplacingSnapshot(t *testing.T) {
	started := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	r := New(NewHosted("cloud", server.URL, WireOpenAICompatible, ""))
	_, _, _, _, original := r.Residency(false)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := r.RouteContext(ctx, Request{Model: "cloud-model", Fresh: true})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("hosted listing did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled route: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("hosted listing ignored cancellation")
	}
	_, _, _, _, after := r.Residency(false)
	if !after.Equal(original) {
		t.Fatalf("cancelled survey replaced snapshot: %s -> %s", original, after)
	}
}

func TestRouteContextCancelsLocalListing(t *testing.T) {
	started := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		started <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()
	r := New(OpenAICompatible("local", server.URL, "/v1", nil))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := r.RouteContext(ctx, Request{Model: "local-model", Fresh: true})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("local listing did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled route: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("local listing ignored cancellation")
	}
}

func TestListingTransportAndConfigurationCauses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	address := server.URL
	server.Close()
	for _, tc := range []struct {
		host  *Host
		cause AvailabilityCause
	}{
		{NewHosted("closed", address, WireOpenAICompatible, ""), AvailabilityTransportUnavailable},
		{NewHosted("unconfigured", address, "unsupported-wire", ""), AvailabilityConfiguration},
		{NewHosted("credential", address, WireOpenAICompatible, "missing"), AvailabilityCredential},
	} {
		r := New(tc.host)
		r.Survey()
		hosts, _, _, _, _ := r.Residency(false)
		if len(hosts) != 1 || hosts[0].AvailabilityCause != tc.cause {
			t.Fatalf("host %s: %+v", tc.host.Name, hosts)
		}
	}
}
