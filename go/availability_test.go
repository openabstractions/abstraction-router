package router

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	wire "github.com/openabstractions/abstraction-router/go/abstraction/router"
)

type unavailableRemoteListing struct {
	fakeRemote
	err error
}

func (f *unavailableRemoteListing) Models(bool) (wire.ModelsSnapshot, error) {
	return wire.ModelsSnapshot{}, f.err
}

// Synthetic causes exercise the real remote listing and survey path without
// opening a socket. Authority and unsupported-method refusals stay distinct.
func TestRemoteListingPreservesAvailabilityCause(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		cause AvailabilityCause
	}{
		{"network", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("unreachable")}, AvailabilityTransportUnavailable},
		{"deadline", fmt.Errorf("exchange: %w", context.DeadlineExceeded), AvailabilityTransportUnavailable},
		{"forbidden", failedListing(AvailabilityRights, errors.New("forbidden")), AvailabilityRights},
		{"unsupported", &wire.ServiceError{Code: wire.ServiceErrorCodeUnknownMethod}, AvailabilityUnknown},
		{"unknown", errors.New("unclassified"), AvailabilityUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &unavailableRemoteListing{err: tc.err}
			h := remoteHost("peer", "fixture.invalid:1", "", func(context.Context) remoteRouter { return client })
			_, err := h.listing(context.Background(), h, nil)
			if !errors.Is(err, tc.err) || causeOf(err) != tc.cause {
				t.Fatalf("wrapped listing: %v cause=%s", err, causeOf(err))
			}
			r := New(h)
			r.Survey()
			hosts, _, _, _, _ := r.Residency(false)
			if len(hosts) != 1 || hosts[0].Up || hosts[0].AvailabilityCause != tc.cause {
				t.Fatalf("remote snapshot: %+v", hosts)
			}
		})
	}
}

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
	aborted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-r.Context().Done()
		close(aborted)
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
	select {
	case <-aborted:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not reach the hosted listing request")
	}
	_, _, _, _, after := r.Residency(false)
	if !after.Equal(original) {
		t.Fatalf("cancelled survey replaced snapshot: %s -> %s", original, after)
	}
}

func TestRouteContextCancelsLocalListing(t *testing.T) {
	started := make(chan struct{}, 1)
	aborted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		started <- struct{}{}
		<-r.Context().Done()
		close(aborted)
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
	select {
	case <-aborted:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not reach the local listing request")
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
