package client

import (
	wire "github.com/openabstractions/abstraction-router/go/abstraction/router"
	"testing"
)

func TestOfferSnapshotRefusesDataOnTypedRefusal(t *testing.T) {
	for _, outcome := range []OfferOutcome{OfferOutcomeInvalid, OfferOutcomeUnavailable, OfferOutcomeForbidden, OfferOutcomeNotPermitted} {
		refused := OfferSnapshot{Outcome: outcome}
		if got, err := validateOfferSnapshot(refused); err != nil || got.Outcome != outcome {
			t.Fatalf("typed refusal: %+v %v", got, err)
		}
		refused.Offers = []Offer{{Family: "must not be consumed"}}
		if _, err := validateOfferSnapshot(refused); err == nil {
			t.Fatalf("%s accepted models", outcome)
		}
		refused.Offers = nil
		refused.Observation.Caller.UserDescription = "must not be disclosed"
		if _, err := validateOfferSnapshot(refused); err == nil {
			t.Fatalf("%s accepted observation", outcome)
		}
	}
	if _, err := validateOfferSnapshot(OfferSnapshot{}); err == nil {
		t.Fatal("missing outcome accepted")
	}
	if _, err := validateOfferSnapshot(OfferSnapshot{Outcome: wire.OfferOutcomeOffered}); err != nil {
		t.Fatal(err)
	}
}
