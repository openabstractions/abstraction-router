package router

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/openabstractions/abstraction-router/go/hostedcatalogue"
)

type CandidateValidation string

const (
	CandidateValid       CandidateValidation = "valid"
	CandidateRejected    CandidateValidation = "rejected"
	CandidateUnreachable CandidateValidation = "unreachable"
)

var errHostedCandidateRedirect = errors.New("candidate provider redirect refused")

// ValidateHostedCandidate is the protected native controller's transient
// pre-storage check. It selects the endpoint from the owning catalogue and
// borrows candidate bytes for this one provider listing, zeroing them on
// return. It stores no key and grants no use or spending authority. Published
// Use.Check retains its saved-key policy/scope meaning.
func ValidateHostedCandidate(ctx context.Context, provider string, candidate []byte) CandidateValidation {
	defer clear(candidate)
	preset, ok := hostedcatalogue.Lookup(provider)
	if !ok || !preset.AcceptsKey(candidate) {
		return CandidateRejected
	}
	binding, err := hostedcatalogue.Select(provider)
	if err != nil || binding.Kind != WireOpenAICompatible {
		return CandidateRejected
	}
	return validateHostedCandidate(ctx, binding, candidate, candidateRequestClient(hostedClient))
}

func candidateRequestClient(base *http.Client) *http.Client {
	client := *base
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return errHostedCandidateRedirect }
	return &client
}

func validateHostedCandidate(ctx context.Context, binding hostedcatalogue.Binding, candidate []byte, client *http.Client) CandidateValidation {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	headers := map[string]string{"Authorization": "Bearer " + string(candidate)}
	defer clear(headers)
	if _, err := listIDsWithClient(ctx, binding.Base+"/models", headers, client); err != nil {
		if cause := causeOf(err); cause == AvailabilityCredential || cause == AvailabilityRights {
			return CandidateRejected
		}
		return CandidateUnreachable
	}
	return CandidateValid
}
