package router

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/openabstractions/abstraction-router/go/hostedcatalogue"
)

type candidateTransport func(*http.Request) (*http.Response, error)

func (f candidateTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHostedCandidateReusesBoundedListingAndRedactsFailures(t *testing.T) {
	binding, _ := hostedcatalogue.Select("deepseek")
	for _, status := range []int{200, 401, 429, 500} {
		key := []byte("inert-candidate-secret")
		var retained *http.Request
		client := &http.Client{Transport: candidateTransport(func(r *http.Request) (*http.Response, error) {
			retained = r
			if r.URL.String() != "https://api.deepseek.com/v1/models" || r.Header.Get("Authorization") != "Bearer inert-candidate-secret" {
				t.Fatal("fixed binding/header not applied")
			}
			if at, ok := r.Context().Deadline(); !ok || time.Until(at) > 10*time.Second {
				t.Fatal("unbounded candidate validation")
			}
			return &http.Response{StatusCode: status, Status: "synthetic-secret-error", Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"synthetic"}]}`))}, nil
		})}
		out := validateHostedCandidate(context.Background(), binding, key, client)
		want := CandidateUnreachable
		if status == 200 {
			want = CandidateValid
		} else if status == 401 {
			want = CandidateRejected
		}
		if out != want {
			t.Fatal(status, out)
		}
		if len(retained.Header) != 0 {
			t.Fatal("candidate header retained after listing")
		}
	}
}

func TestHostedCandidateRefusesUnknownBeforeTransportAndZeroesInput(t *testing.T) {
	for _, id := range []string{"missing", "openai-compatible", "github-download"} {
		key := []byte("inert-candidate")
		if out := ValidateHostedCandidate(t.Context(), id, key); out != CandidateRejected {
			t.Fatal(id, out)
		}
		if !bytes.Equal(key, make([]byte, len(key))) {
			t.Fatal("candidate not zeroed")
		}
	}
}

func TestHostedCandidateRefusesRedirectWithoutForwardingCandidate(t *testing.T) {
	binding, _ := hostedcatalogue.Select("deepseek")
	calls := 0
	client := candidateRequestClient(&http.Client{Transport: candidateTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://elsewhere.invalid/models"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})})
	if out := validateHostedCandidate(t.Context(), binding, []byte("inert-candidate"), client); out != CandidateUnreachable || calls != 1 {
		t.Fatal(calls, out)
	}
}

func TestHostedCandidateRequiresAnActualModelListing(t *testing.T) {
	binding, _ := hostedcatalogue.Select("deepseek")
	for _, body := range []string{`{}`, `null`, `{"data":null}`, `{"error":{"message":"inert-candidate"}}`} {
		client := &http.Client{Transport: candidateTransport(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		})}
		if out := validateHostedCandidate(t.Context(), binding, []byte("inert-candidate"), client); out != CandidateUnreachable {
			t.Fatal("non-listing accepted", out)
		}
	}
}
