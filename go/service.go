package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"time"

	identity "github.com/openabstractions/abstraction-identity"
	"github.com/openabstractions/abstraction-identity/listen"
	"github.com/openabstractions/abstraction-identity/remote"
	"github.com/openabstractions/abstraction-rights/go/authorization/gate"
	"github.com/openabstractions/abstraction-router/go/internal/admission"
)

const maxFrame = 64 << 10

type Service struct {
	Endpoint string
	r        *Router
	l        listen.Listener
	policy   gate.Gate
	ctx      context.Context
	cancel   context.CancelFunc
}

// Start refuses to run at all on a machine that cannot enforce the identity this
// service's answers are attributed to. A router that cannot say who asked keeps
// an audit of nobody.
func Start(endpoint string, r *Router) (*Service, error) {
	return start(endpoint, r, gate.Local)
}

func start(endpoint string, r *Router, local func(string) (gate.Gate, error)) (*Service, error) {
	policy, err := local(admission.Namespace)
	if err != nil {
		return nil, fmt.Errorf("router: local policy unavailable: %w", err)
	}
	policy, err = gate.For(admission.Namespace, policy)
	if err != nil {
		return nil, fmt.Errorf("router: local policy unavailable: %w", err)
	}
	if err := startGuard(endpoint); err != nil {
		return nil, err
	}
	l, err := listen.Listen(endpoint)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{Endpoint: endpoint, r: r, l: listen.Sessions(l, listen.SessionOptions{}), policy: policy, ctx: ctx, cancel: cancel}
	go s.loop()
	return s, nil
}

func (s *Service) Close() error {
	s.cancel()
	return s.l.Close()
}

func (s *Service) loop() {
	for {
		c, err := s.l.Accept()
		if errors.Is(err, net.ErrClosed) {
			return
		}
		if err != nil {
			slog.Warn("accept", "err", err)
			continue
		}
		go s.serve(c)
	}
}

func (s *Service) serve(c listen.Conn) {
	call, err := listen.Receive(c, Bound, maxFrame)
	defer call.Close()
	if err != nil {
		if !errors.Is(err, listen.ErrNoFrame) {
			reply(call, Response{Code: CodeCallerRefused, Caller: call.Caller, Error: "caller identity could not be received"})
		}
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = call.Close() })
	defer stop()
	go func() {
		select {
		case <-call.Gone():
			cancel()
		case <-ctx.Done():
		}
	}()
	reply(call, s.answer(ctx, call, call.Frame, call.Caller))
}

// legacyCall is supplied only by Receive after Bound admission. The interface
// keeps inert receiving controls separate from native connection fixtures.
type legacyCall interface {
	Peer() (*identity.Peer, error)
}

func (s *Service) answer(ctx context.Context, call legacyCall, frame []byte, seen listen.Seen) Response {
	refuse := func(code, message string) Response { return Response{Code: code, Error: message, Caller: seen} }
	if call == nil {
		return refuse(CodeCallerRefused, "caller is not bound")
	}
	peer, err := call.Peer()
	if err != nil || peer == nil {
		return refuse(CodeCallerRefused, "caller identity could not be rechecked")
	}
	var req Request
	if err := json.Unmarshal(frame, &req); err != nil {
		return refuse(CodeInvalidRequest, "not a request: "+err.Error())
	}
	action, resource := admission.ActionInventory, admission.ResourceInventory
	switch req.Op {
	case OpModels, OpResidency, OpOffer:
	case OpRoute:
		action, resource = admission.ActionRoute, admission.ResourceRoutes
	default:
		return refuse(CodeUnknownOperation, unknownOperation(req.Op))
	}
	if err := s.policy.Check(ctx, peer, action, resource); err != nil {
		if errors.Is(err, gate.ErrUnavailable) {
			return refuse(CodeUnavailable, "rights:unavailable")
		}
		return refuse(admission.CodeForbidden, admission.ForbiddenMessage(err))
	}
	// A decision can wait. Recheck the retained binding and context again before
	// any fresh survey, provider call or audit effect, without trusting Seen.
	if peer, err := call.Peer(); err != nil || peer == nil {
		return refuse(CodeCallerRefused, "caller binding no longer valid")
	}
	if err := ctx.Err(); err != nil {
		return refuse(CodeUnavailable, "rights:unavailable")
	}
	return s.r.AnswerContext(ctx, req, seen)
}

func unknownOperation(op string) string {
	return fmt.Sprintf("unknown op %q; this service answers %q, %q, %q and %q and nothing else", op, OpModels, OpResidency, OpRoute, OpOffer)
}

func reply(w io.Writer, r Response) {
	raw, err := json.Marshal(r)
	if err != nil {
		return
	}
	w.Write(append(raw, '\n'))
}

// Answer is the context-free post-admission seam. Transport entry points must
// bind their caller and obtain their receiving policy decision before calling.
func (r *Router) Answer(req Request, caller listen.Seen) Response {
	return r.AnswerContext(context.Background(), req, caller)
}

// AnswerContext is the post-admission seam within ctx: a fresh request whose survey outlasts ctx
// is refused as unavailable, and the snapshot it would have replaced stays
// readable (router P0-3).
func (r *Router) AnswerContext(ctx context.Context, req Request, caller listen.Seen) Response {
	return r.answerContext(ctx, req, caller, caller.Bound, caller.Path, caller.User)
}

// AnswerRemoteContext attributes a receiving-policy-authorized request to its
// canonical remote subject. It carries no native caller proof.
func (r *Router) AnswerRemoteContext(ctx context.Context, req Request, peer remote.RemotePeer, domain string) Response {
	return r.answerContext(ctx, req, listen.Seen{}, peer.Program() != "", peer.Program(), domain)
}

func (r *Router) answerContext(ctx context.Context, req Request, caller listen.Seen, attributable bool, program, domain string) Response {
	start := time.Now()
	out := Response{Caller: caller}
	took := func(at time.Time) Response {
		out.TookMS = time.Since(start).Milliseconds()
		out.AgeMS = start.Sub(at).Milliseconds()
		return out
	}
	// refresh surveys first for a fresh request, so every operation below
	// reads the snapshot without touching a server.
	refresh := func() bool {
		if !req.Fresh {
			return true
		}
		if err := r.freshSurvey(ctx); err != nil {
			out.Code = CodeUnavailable
			out.Error = "router: fresh survey did not finish within the call: " + err.Error()
			return false
		}
		req.Fresh = false
		return true
	}
	switch req.Op {
	case OpModalities:
		page := r.Modalities(req.Cursor, req.Limit, domain+"\x00"+program)
		out.ModalityPage = &page
		return out
	case OpModels:
		if !refresh() {
			return took(time.Now())
		}
		fams, at := r.Models(req.Fresh)
		out.Models = fams
		return took(at)
	case OpResidency:
		if !refresh() {
			return took(time.Now())
		}
		hosts, holders, why, dbl, at := r.Residency(req.Fresh)
		out.Hosts, out.Holders, out.HoldersWhy, out.Doubled, out.Asked = hosts, holders, why, dbl, r.Asked()
		return took(at)
	case OpRoute:
		if !attributable {
			out.Code = CodeCallerRefused
			out.Error = "refused: a routing decision is attributed to the program that asked for it, and " + caller.Why
			return took(time.Now())
		}
		if !refresh() {
			return took(time.Now())
		}
		d, at := r.Route(req)
		out.Decision = d
		r.record(Ask{At: time.Now(), Caller: program, User: domain,
			Model: req.Model, Family: d.Family, Verdict: d.Verdict, Host: d.Host})
		return took(at)
	case OpOffer:
		if !validPlacement(req.Placement) {
			out.Code = CodeInvalidRequest
			out.Error = fmt.Sprintf("placement %q is none of %q, %q and %q", req.Placement, PlacementLocal, PlacementHosted, PlacementAny)
			return took(time.Now())
		}
		if req.Reserve < 0 {
			out.Code = CodeInvalidRequest
			out.Error = fmt.Sprintf("reserve %d is negative", req.Reserve)
			return took(time.Now())
		}
		if !refresh() {
			return took(time.Now())
		}
		offers, at, err := r.Offer(req)
		if err != nil {
			out.Code = CodeInternal
			out.Error = err.Error()
			return took(time.Now())
		}
		out.Offers = offers
		return took(at)
	case OpObserveOffer:
		reading, err := r.ObserveOffer(ctx, req, req.Cursor, req.Wait)
		if err != nil {
			out.Code = CodeUnavailable
			if errors.Is(err, ErrInvalidOffer) {
				out.Code = CodeInvalidRequest
			}
			out.Error = err.Error()
			return took(time.Now())
		}
		out.Offers, out.Cursor = reading.Offers, reading.Cursor
		out = took(reading.At)
		// A wait's answer is as old as its survey when it is sent, not when
		// the call arrived (ROUTE-D11): a caller tells one survey from the
		// next by now minus this age.
		out.AgeMS = time.Since(reading.At).Milliseconds()
		return out
	}
	out.Code = CodeUnknownOperation
	out.Error = unknownOperation(req.Op)
	return took(time.Now())
}

// startGuard refuses an endpoint whose transport can never bind a caller;
// an xpc: endpoint passes on macOS, where a socket refuses.
func startGuard(endpoint string) error {
	if err := listen.CanEver(endpoint, Bound); err != nil {
		return fmt.Errorf("this machine cannot bind a caller to a connection: %w", err)
	}
	return nil
}
