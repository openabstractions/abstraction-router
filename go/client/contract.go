package client

// The generated contract types this package's API reaches, re-exported so an
// application names them through this package and never imports the generated
// one. scripts/idiom_check.py refuses a reachable type this file leaves out.

import (
	wire "github.com/openabstractions/abstraction-router/go/abstraction/router"
)

type Alias = wire.Alias

type Ask = wire.Ask

type Caller = wire.Caller

type Decision = wire.Decision

type Family = wire.Family

type HostState = wire.HostState

type Observation = wire.Observation

type ServiceErrorCode = wire.ServiceErrorCode

const (
	ServiceErrorCodeHandlerError      = wire.ServiceErrorCodeHandlerError
	ServiceErrorCodeInvalidResult     = wire.ServiceErrorCodeInvalidResult
	ServiceErrorCodeUnknownVersion    = wire.ServiceErrorCodeUnknownVersion
	ServiceErrorCodeUnknownService    = wire.ServiceErrorCodeUnknownService
	ServiceErrorCodeUnknownMethod     = wire.ServiceErrorCodeUnknownMethod
	ServiceErrorCodeWrongMode         = wire.ServiceErrorCodeWrongMode
	ServiceErrorCodeInternal          = wire.ServiceErrorCodeInternal
	ServiceErrorCodeInvalidRequest    = wire.ServiceErrorCodeInvalidRequest
	ServiceErrorCodeCallerRefused     = wire.ServiceErrorCodeCallerRefused
	ServiceErrorCodeUnknownOperation  = wire.ServiceErrorCodeUnknownOperation
	ServiceErrorCodePolicyUnavailable = wire.ServiceErrorCodePolicyUnavailable
	ServiceErrorCodeForbidden         = wire.ServiceErrorCodeForbidden
)

// ServiceErrorCodeValues returns every member of ServiceErrorCode in declaration order, in a new slice.
func ServiceErrorCodeValues() []ServiceErrorCode { return wire.ServiceErrorCodeValues() }
