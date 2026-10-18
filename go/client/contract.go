package client

// The generated contract types this package's API reaches, re-exported so an
// application names them through this package and never imports the generated
// one. scripts/idiom_check.py refuses a reachable type this file leaves out.

import (
	wire "github.com/openabstractions/abstraction-router/go/abstraction/router"
)

type Alias = wire.Alias

type AvailabilityCause = wire.AvailabilityCause

const (
	AvailabilityCauseTransportUnavailable = wire.AvailabilityCauseTransportUnavailable
	AvailabilityCauseCredential           = wire.AvailabilityCauseCredential
	AvailabilityCauseRights               = wire.AvailabilityCauseRights
	AvailabilityCauseConfiguration        = wire.AvailabilityCauseConfiguration
	AvailabilityCauseMalformed            = wire.AvailabilityCauseMalformed
	AvailabilityCauseUnknown              = wire.AvailabilityCauseUnknown
	AvailabilityCauseOther                = wire.AvailabilityCauseOther
)

// AvailabilityCauseValues returns every member of AvailabilityCause in declaration order, in a new slice.
func AvailabilityCauseValues() []AvailabilityCause { return wire.AvailabilityCauseValues() }

type Ask = wire.Ask

type Caller = wire.Caller

type Component = wire.Component

type Decision = wire.Decision

type Family = wire.Family

type HostState = wire.HostState

type Observation = wire.Observation

type Offer = wire.Offer

type OfferOutcome = wire.OfferOutcome

const (
	OfferOutcomeOffered      = wire.OfferOutcomeOffered
	OfferOutcomeInvalid      = wire.OfferOutcomeInvalid
	OfferOutcomeUnavailable  = wire.OfferOutcomeUnavailable
	OfferOutcomeForbidden    = wire.OfferOutcomeForbidden
	OfferOutcomeNotPermitted = wire.OfferOutcomeNotPermitted
)

func OfferOutcomeValues() []OfferOutcome { return wire.OfferOutcomeValues() }

type Placement = wire.Placement

const (
	PlacementLocal  = wire.PlacementLocal
	PlacementHosted = wire.PlacementHosted
	PlacementAny    = wire.PlacementAny
)

// PlacementValues returns every member of Placement in declaration order, in a new slice.
func PlacementValues() []Placement { return wire.PlacementValues() }

type ContextSource = wire.ContextSource

const (
	ContextSourceEffective = wire.ContextSourceEffective
	ContextSourceMaximum   = wire.ContextSourceMaximum
	ContextSourceDefault   = wire.ContextSourceDefault
	ContextSourceUnknown   = wire.ContextSourceUnknown
	ContextSourceNone      = wire.ContextSourceNone
)

// ContextSourceValues returns every member of ContextSource in declaration order, in a new slice.
func ContextSourceValues() []ContextSource { return wire.ContextSourceValues() }

type ServiceErrorCode = wire.ServiceErrorCode

const (
	ServiceErrorCodeHandlerError     = wire.ServiceErrorCodeHandlerError
	ServiceErrorCodeInvalidResult    = wire.ServiceErrorCodeInvalidResult
	ServiceErrorCodeUnknownVersion   = wire.ServiceErrorCodeUnknownVersion
	ServiceErrorCodeUnknownService   = wire.ServiceErrorCodeUnknownService
	ServiceErrorCodeUnknownMethod    = wire.ServiceErrorCodeUnknownMethod
	ServiceErrorCodeWrongMode        = wire.ServiceErrorCodeWrongMode
	ServiceErrorCodeInternal         = wire.ServiceErrorCodeInternal
	ServiceErrorCodeInvalidRequest   = wire.ServiceErrorCodeInvalidRequest
	ServiceErrorCodeCallerRefused    = wire.ServiceErrorCodeCallerRefused
	ServiceErrorCodeUnknownOperation = wire.ServiceErrorCodeUnknownOperation
	ServiceErrorCodeUnavailable      = wire.ServiceErrorCodeUnavailable
	ServiceErrorCodeForbidden        = wire.ServiceErrorCodeForbidden
)

// ServiceErrorCodeValues returns every member of ServiceErrorCode in declaration order, in a new slice.
func ServiceErrorCodeValues() []ServiceErrorCode { return wire.ServiceErrorCodeValues() }
