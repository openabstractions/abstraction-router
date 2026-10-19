package service

import (
	"context"
	"errors"
	"fmt"
	"github.com/openabstractions/abstraction-identity/remote"
	rightswire "github.com/openabstractions/abstraction-rights/go/abstraction/rights/api"
	rights "github.com/openabstractions/abstraction-rights/go/client"
	router "github.com/openabstractions/abstraction-router/go"
	wire "github.com/openabstractions/abstraction-router/go/abstraction/router"
)

// answerFunc answers one router request; its Models, Hosts and Pick turn the
// answer into router@1 values.
type answerFunc func(router.Request) (router.Response, error)

// views is the router@1 handler of one native call.
func (r *receiver) views() answerFunc { return r.answer }

// RemoteCaller carries admitted certificate evidence and a display-only domain.
// It contains no local account, executable or kernel proof.
type RemoteCaller struct {
	Peer   remote.RemotePeer
	Domain string
}

type RemotePolicy func(context.Context, RemoteCaller, string, string) error

// RemoteDecider is implemented by the ordinary native Rights client. Its
// receiving host still checks the native enforcer and receiving account.
type RemoteDecider interface {
	DecideForContext(context.Context, rights.Subject, string, string) (rights.Decision, error)
}

// RemoteRights adapts native DecideFor for a canonical remote subject. Account
// is trusted receiving configuration, never the remote display domain.
func RemoteRights(account string, decider RemoteDecider) RemotePolicy {
	return func(ctx context.Context, caller RemoteCaller, action, resource string) error {
		if account == "" || decider == nil || caller.Peer.Program() == "" {
			return errors.New("router remote: receiving subject/decider required")
		}
		decision, err := decider.DecideForContext(ctx, rights.Subject{Account: account, Program: caller.Peer.Program()}, action, resource)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrPolicyUnavailable, err)
		}
		if decision.Outcome == rightswire.DecisionOutcomeUnavailable {
			return ErrPolicyUnavailable
		}
		if decision.Outcome != rightswire.DecisionOutcomePermitted {
			return &rights.DecisionError{Outcome: decision.Outcome.String(), PolicyRevision: decision.PolicyRevision}
		}
		if decision.PolicyRevision == "" {
			return fmt.Errorf("%w: missing decision revision", ErrPolicyUnavailable)
		}
		return nil
	}
}

// Remote decides every operation before reading or routing. Native caller
// proof stays local; a missing policy grants no access.
func Remote(ctx context.Context, r *router.Router, caller RemoteCaller, policy RemotePolicy) wire.Router {
	return answerFunc(func(request router.Request) (router.Response, error) {
		if caller.Peer.Program() == "" {
			return router.Response{}, &wire.ServiceError{Code: router.CodeCallerRefused, Message: "caller is not mapped"}
		}
		action, resource, known := opRight(request.Op)
		if !known {
			return router.Response{}, &wire.ServiceError{Code: router.CodeUnknownOperation, Message: "router operation not served"}
		}
		err := error(ErrPolicyUnavailable)
		if policy != nil && ctx.Err() == nil {
			err = policy(ctx, caller, action, resource)
		}
		if ctx.Err() != nil {
			err = ErrPolicyUnavailable
		}
		if err != nil {
			code := CodeForbidden
			if errors.Is(err, ErrPolicyUnavailable) {
				code = CodeUnavailable
			}
			if request.Op == router.OpModalities {
				return router.Response{}, modalityDecisionError(&wire.ServiceError{Code: wire.ServiceErrorCode(code)})
			}
			if offerOp(request.Op) {
				outcome := wire.OfferOutcomeNotPermitted
				if code == CodeUnavailable {
					outcome = wire.OfferOutcomeUnavailable
				}
				return router.Response{}, &offerRefusal{outcome: outcome}
			}
			return router.Response{}, &wire.ServiceError{Code: wire.ServiceErrorCode(code), Message: "remote router decision: " + err.Error()}
		}
		if r == nil {
			if request.Op == router.OpModalities {
				return router.Response{}, &modalityRefusal{wire.ModalityOutcomeUnavailable}
			}
			return router.Response{}, &wire.ServiceError{Code: CodeUnavailable, Message: "router unavailable"}
		}
		out := r.AnswerRemoteContext(ctx, request, caller.Peer, caller.Domain)
		if offerOp(request.Op) {
			return out, providerOfferResponseError(out)
		}
		return out, providerResponseError(out)
	})
}
