package service

import (
	"github.com/openabstractions/abstraction-identity/listen"
	router "github.com/openabstractions/abstraction-router/go"
	wire "github.com/openabstractions/abstraction-router/go/abstraction/router"
)

// answerFunc answers one router request; its Models, Hosts and Pick turn the
// answer into router@1 values.
type answerFunc func(router.Request) (router.Response, error)

// views is the router@1 handler of one native call.
func (r *receiver) views() answerFunc { return r.answer }

// Remote is the router@1 handler for a caller of another trust domain that the
// receiving host has already authenticated and mapped: caller names it in
// every answer and in the routing audit. The host applies its own method
// policy before dispatching to it.
func Remote(r *router.Router, caller listen.Seen) wire.Router {
	return answerFunc(func(request router.Request) (router.Response, error) {
		if !caller.Bound {
			return router.Response{}, &wire.ServiceError{Code: router.CodeCallerRefused, Message: "caller is not mapped"}
		}
		out := r.Answer(request, caller)
		if out.Err() != nil {
			code := out.Code
			if code == "" {
				code = router.CodeInternal
			}
			return out, &wire.ServiceError{Code: wire.ServiceErrorCode(code), Message: out.Error}
		}
		return out, nil
	})
}
