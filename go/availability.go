package router

import (
	"context"
	"errors"
	"net"
	"net/http"
)

// listingFailure retains the diagnostic error while preserving its cause.
// Callers of Survey use Cause, never the wording of Error, for decisions.
type listingFailure struct {
	cause AvailabilityCause
	err   error
}

func (e *listingFailure) Error() string { return e.err.Error() }
func (e *listingFailure) Unwrap() error { return e.err }

func failedListing(cause AvailabilityCause, err error) error {
	return &listingFailure{cause: cause, err: err}
}

func statusCause(status int) AvailabilityCause {
	switch status {
	case http.StatusUnauthorized:
		return AvailabilityCredential
	case http.StatusForbidden:
		return AvailabilityRights
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return AvailabilityMalformed
	case http.StatusServiceUnavailable, http.StatusBadGateway, http.StatusGatewayTimeout:
		return AvailabilityTransportUnavailable
	default:
		return AvailabilityUnknown
	}
}

func causeOf(err error) AvailabilityCause {
	var failure *listingFailure
	if errors.As(err, &failure) {
		return failure.cause
	}
	var credential *CredentialRefusal
	if errors.As(err, &credential) {
		return AvailabilityCredential
	}
	var network *net.OpError
	if errors.As(err, &network) || errors.Is(err, context.DeadlineExceeded) {
		return AvailabilityTransportUnavailable
	}
	return AvailabilityUnknown
}
