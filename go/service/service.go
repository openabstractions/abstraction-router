// Package service binds the generated router protocol to the existing provider.
// It keeps legacy line framing and the HTTP window on their existing endpoints.
package service

import (
	"context"
	"errors"
	"fmt"
	identity "github.com/openabstractions/abstraction-identity"
	"github.com/openabstractions/abstraction-identity/listen"
	"github.com/openabstractions/abstraction-rights/go/authorization/gate"
	router "github.com/openabstractions/abstraction-router/go"
	wire "github.com/openabstractions/abstraction-router/go/abstraction/router"
	"github.com/openabstractions/abstraction-router/go/internal/admission"
	"sync"
	"time"
	"unicode/utf8"
)

type Host struct {
	listener    listen.Listener
	router      *router.Router
	ctx         context.Context
	cancel      context.CancelFunc
	once        sync.Once
	workers     sync.WaitGroup
	lifecycle   sync.Mutex
	serving     bool
	policy      Policy
	hostListing HostListing
	OnError     func(error)
	// OnStopped is called when admission stops. Assign it before Serve.
	OnStopped func()
}

// Router refusal codes for an explicit receiving policy.
const (
	CodeForbidden   = admission.CodeForbidden
	CodeUnavailable = router.CodeUnavailable
)

// Actions a Policy receives, and the resource each names.
const (
	// ActionInventory covers Models, Hosts and Offer; its resource is
	// ResourceInventory (CONTRACT.md ROUTE-A2; VISION ROUTE-D8 "read under
	// inventory.read").
	ActionInventory   = admission.ActionInventory
	ResourceInventory = admission.ResourceInventory
	// ActionRoute covers Pick; its resource is ResourceRoutes for every model
	// (research/rights-defaults/DECISION.md §1). The per-host switch is
	// inference complete on host:<name>.
	ActionRoute    = admission.ActionRoute
	ResourceRoutes = admission.ResourceRoutes
)

// actionNamespace is the namespace every router action belongs to; the host
// designates the policy it installs for it (RIGHTS-A7).
const actionNamespace = admission.Namespace

// Policy authorizes one router operation for the rechecked bound caller. It
// is the rights gate: it must honor ctx and be safe for concurrent calls.
// Wrap ErrPolicyUnavailable when the decision cannot be obtained; every other
// error is a refusal.
type Policy = gate.Gate

// HostListing narrows a native caller's host observation using the receiving
// runtime's credential and host-use owners. It reads no credential value.
type HostListing func(context.Context, *identity.Peer, []router.HostState) ([]router.HostState, error)

func (h *Host) EnableHostListing(check HostListing) error {
	h.lifecycle.Lock()
	defer h.lifecycle.Unlock()
	if h.serving || h.ctx.Err() != nil || check == nil {
		return errors.New("router service: configure host listing before Serve")
	}
	h.hostListing = check
	return nil
}

// ErrPolicyUnavailable distinguishes a failed decision lookup from refusal.
var ErrPolicyUnavailable = gate.ErrUnavailable

// EnablePolicy narrows every operation to callers the policy authorizes.
// Configure it before Serve. Without it every operation reads unavailable
// (ROUTE-E2, ROUTE-E3): a missing policy is never a permit. The installed
// policy is designated for the router's namespace and refuses every action
// outside it (RIGHTS-A7).
func (h *Host) EnablePolicy(policy Policy) error {
	h.lifecycle.Lock()
	defer h.lifecycle.Unlock()
	if h.serving || h.ctx.Err() != nil {
		return errors.New("router service: configure policy before Serve")
	}
	if policy == nil {
		return errors.New("router service: explicit policy required")
	}
	designated, err := gate.For(actionNamespace, policy)
	if err != nil {
		return fmt.Errorf("router service: %w", err)
	}
	h.policy = designated
	return nil
}

func Listen(endpoint string, provider *router.Router) (*Host, error) {
	if provider == nil {
		return nil, errors.New("router service: nil provider")
	}
	if err := listen.CanEver(endpoint, router.Bound); err != nil {
		return nil, fmt.Errorf("router service cannot bind callers: %w", err)
	}
	l, err := listen.ListenFramed(endpoint, router.Bound)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Host{listener: listen.Sessions(l, listen.SessionOptions{}), router: provider, ctx: ctx, cancel: cancel}, nil
}
func (h *Host) Close() error {
	var err error
	h.once.Do(func() { h.cancel(); err = h.listener.Close() })
	return err
}
func (h *Host) Serve(ctx context.Context) error {
	h.lifecycle.Lock()
	if h.serving {
		h.lifecycle.Unlock()
		return errors.New("router service: host already served")
	}
	h.serving = true
	policy := h.policy
	h.lifecycle.Unlock()
	stop := context.AfterFunc(ctx, func() { _ = h.Close() })
	defer stop()
	defer h.workers.Wait()
	defer func() {
		if h.OnStopped != nil {
			h.OnStopped()
		}
	}()
	defer h.Close()
	slots := listen.NewCallAdmission(64)
	for {
		conn, err := h.listener.Accept()
		if err != nil {
			if h.ctx.Err() != nil || ctx.Err() != nil {
				return nil
			}
			return err
		}
		release, admitted := slots.Take(conn)
		if !admitted {
			continue
		}
		h.workers.Add(1)
		go func() {
			defer h.workers.Done()
			defer release()
			defer conn.Close()
			// The call lives long enough for an ObserveOffer wait; every other
			// operation keeps its ordinary ten seconds (requestCtx).
			callCtx, cancelCall := context.WithTimeout(h.ctx, router.MaxOfferWait+observeCallBudget)
			defer cancelCall()
			call, err := listen.ReceiveFramed(callCtx, conn, router.Bound, 1<<20)
			if call != nil {
				defer call.Close()
			}
			if err == nil {
				requestCtx, cancel := context.WithTimeout(callCtx, observeCallBudget)
				defer cancel()
				dispatch := wire.RouterDispatcher{Handler: (&receiver{provider: h.router, call: call, policy: policy, hostListing: h.hostListing, ctx: requestCtx, serve: callCtx}).views()}
				var response []byte
				response, err = dispatch.ExchangeFrame(call.Frame)
				if err == nil {
					err = call.Reply(response)
				}
			}
			if err != nil && h.OnError != nil && h.ctx.Err() == nil {
				h.OnError(err)
			}
		}()
	}
}

type receiver struct {
	provider    *router.Router
	call        *listen.FramedCall
	policy      Policy
	hostListing HostListing
	ctx         context.Context
	// serve outlives one call's ordinary deadline: ObserveOffer waits from
	// it for up to its wait plus observeCallBudget.
	serve context.Context
}

// observeCallBudget is what an ObserveOffer call may take beyond its wait:
// the ordinary call's time to read and answer.
const observeCallBudget = 10 * time.Second

// opRight is the right each served operation decides (CONTRACT.md
// ROUTE-A2): reading what exists, offers included, decides inventory.read;
// only a routing decision decides route. Any other operation is refused, so a
// later operation never inherits a read right by default.
func opRight(op string) (action, resource string, known bool) {
	switch op {
	case router.OpModels, router.OpResidency, router.OpOffer, router.OpObserveOffer, router.OpModalities:
		return ActionInventory, ResourceInventory, true
	case router.OpRoute:
		return ActionRoute, ResourceRoutes, true
	}
	return "", "", false
}

// offerOp says whether op is answered and gated as Offer is (ROUTE-A2).
func offerOp(op string) bool { return op == router.OpOffer || op == router.OpObserveOffer }

// replyMargin is the part of a call's deadline kept for writing the reply, so
// a fresh survey that outlasts the call is refused while the caller still
// waits for the answer.
const replyMargin = time.Second

// callContext is the call's deadline less replyMargin, or Background for a
// receiver built without one.
func (r *receiver) callContext() (context.Context, context.CancelFunc) {
	if r.ctx == nil {
		return context.Background(), func() {}
	}
	if deadline, ok := r.ctx.Deadline(); ok {
		return context.WithDeadline(r.ctx, deadline.Add(-replyMargin))
	}
	return r.ctx, func() {}
}

// authorize applies the configured policy to one operation after the caller
// binding is rechecked and before the provider is asked.
func (r *receiver) authorize(action, resource string) error {
	ctx := r.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	peer, err := r.call.Peer()
	if err != nil {
		return &wire.ServiceError{Code: router.CodeCallerRefused, Message: "caller identity could not be rechecked"}
	}
	err = r.policy.Check(ctx, peer, action, resource)
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrPolicyUnavailable) {
		return &wire.ServiceError{Code: CodeUnavailable, Message: "rights:unavailable"}
	}
	return &wire.ServiceError{Code: CodeForbidden, Message: admission.ForbiddenMessage(err)}
}

func (r *receiver) answer(request router.Request) (router.Response, error) {
	if r.call == nil {
		return router.Response{}, &wire.ServiceError{Code: router.CodeCallerRefused, Message: "caller is not bound"}
	}
	if err := r.call.Recheck(); err != nil {
		return router.Response{}, &wire.ServiceError{Code: router.CodeCallerRefused, Message: "caller binding no longer valid"}
	}
	action, resource, known := opRight(request.Op)
	if !known {
		return router.Response{}, &wire.ServiceError{Code: router.CodeUnknownOperation, Message: "router operation not served"}
	}
	if err := r.authorize(action, resource); err != nil {
		if request.Op == router.OpModalities {
			return router.Response{}, modalityDecisionError(err)
		}
		if offerOp(request.Op) {
			var refusal *wire.ServiceError
			if errors.As(err, &refusal) {
				switch refusal.Code {
				case CodeForbidden:
					return router.Response{}, &offerRefusal{outcome: wire.OfferOutcomeNotPermitted}
				case CodeUnavailable:
					return router.Response{}, &offerRefusal{outcome: wire.OfferOutcomeUnavailable}
				}
			}
		}
		return router.Response{}, err
	}
	// Caller is created by ReceiveFramed after checking router.Bound, never decoded.
	ctx, cancel := r.callContext()
	if request.Op == router.OpObserveOffer && r.serve != nil {
		cancel()
		ctx, cancel = context.WithTimeout(r.serve, request.Wait+observeCallBudget)
	}
	defer cancel()
	if request.Op == router.OpModalities && ctx.Err() != nil {
		return router.Response{}, &modalityRefusal{wire.ModalityOutcomeUnavailable}
	}
	out := r.provider.AnswerContext(ctx, request, r.call.Caller)
	if request.Op == router.OpResidency && r.hostListing != nil {
		peer, err := r.call.Peer()
		if err != nil {
			return router.Response{}, &wire.ServiceError{Code: router.CodeCallerRefused, Message: "caller identity could not be rechecked"}
		}
		out.Hosts, err = r.hostListing(ctx, peer, out.Hosts)
		if err != nil {
			return router.Response{}, &wire.ServiceError{Code: CodeUnavailable, Message: "host listing unavailable"}
		}
		if err := r.call.Recheck(); err != nil {
			return router.Response{}, &wire.ServiceError{Code: router.CodeCallerRefused, Message: "caller binding no longer valid"}
		}
	}
	if offerOp(request.Op) {
		return out, providerOfferResponseError(out)
	}
	return out, providerResponseError(out)
}

func providerOfferResponseError(out router.Response) error {
	if out.Err() == nil {
		return nil
	}
	outcome := wire.OfferOutcomeUnavailable
	switch out.Code {
	case router.CodeInvalidRequest:
		outcome = wire.OfferOutcomeInvalid
	case CodeForbidden:
		outcome = wire.OfferOutcomeForbidden
	case "not_permitted":
		outcome = wire.OfferOutcomeNotPermitted
	}
	// A provider response cannot establish a framing or caller-binding failure.
	return &offerRefusal{outcome: outcome}
}

// providerResponseError maps the router's outcome words to the service error
// contract. Legacy text-only refusals receive the stable internal code.
func providerResponseError(out router.Response) error {
	if out.Err() == nil {
		return nil
	}
	code := out.Code
	if code == "" {
		code = router.CodeInternal
	}
	return &wire.ServiceError{Code: wire.ServiceErrorCode(code), Message: out.Error}
}

func observation(r router.Response) wire.Observation {
	return wire.Observation{Caller: wire.Caller{UserDescription: r.Caller.User, PathDescription: r.Caller.Path}, TookMs: r.TookMS, CacheAgeMs: r.AgeMS}
}

// toWireAlias converts the legacy line-protocol alias to the framed wire record.
func toWireAlias(a router.Alias) wire.Alias {
	return wire.Alias(a)
}

// toWireFamily is the one place that carries router.Family's fields onto the
// generated wire.Family, converting each of its Names through toWireAlias and
// each of its Components through toWireComponent. convert_test.go's
// reflection parity test covers it the same way as toWireAlias.
func toWireFamily(f router.Family) wire.Family {
	out := wire.Family{Family: f.Family, HeldIn: f.HeldIn, FamilySource: f.FamilySource, Title: f.Title, Objects: f.Objects}
	for _, a := range f.Names {
		out.Names = append(out.Names, toWireAlias(a))
	}
	for _, c := range f.Components {
		out.Components = append(out.Components, toWireComponent(c))
	}
	return out
}

// toWireComponent is the one place that carries router.Component's fields
// onto the generated wire.Component. convert_test.go's reflection parity
// test covers it the same way as toWireAlias.
func toWireComponent(c router.Component) wire.Component {
	return wire.Component{Store: c.Store, Role: c.Role, Name: c.Name}
}

// toWireHostState is the one place that carries router.HostState's fields
// onto the generated wire.HostState. Installed widens from int to int64;
// every other field copies by name. convert_test.go's reflection parity test
// covers it the same way as toWireAlias.
func toWireHostState(h router.HostState) wire.HostState {
	state := wire.HostState{Host: h.Host, Base: h.Base, Up: h.Up, Why: h.Why, Installed: int64(h.Installed), Resident: h.Resident, Servable: h.Servable, Hosted: h.Hosted, Wire: h.Wire, Credential: h.Credential, DeclaredBy: h.DeclaredBy, Profiles: h.Profiles, Domain: h.Domain}
	if h.AvailabilityCause != "" {
		cause := wire.AvailabilityCause(h.AvailabilityCause)
		state.AvailabilityCause = &cause
	}
	state.Engines = h.Engines
	return state
}

// askTimeLayout is the layout toWireAsk formats router.Ask's At into; it is
// also the timestamp format for the legacy wire.Ask over the framed
// protocol. convert_test.go parses wire.Ask's At back with the same layout
// to check the value round-trips.
const askTimeLayout = wire.TimestampLayout

// toWireAsk is the one place that carries router.Ask's fields onto the
// generated wire.Ask. At formats router.Ask's time.Time as the string
// wire.Ask carries instead. convert_test.go's reflection parity test covers
// it the same way as toWireAlias.
func toWireAsk(a router.Ask) wire.Ask {
	return wire.Ask{At: wire.FormatTimestamp(a.At), Caller: a.Caller, User: a.User, Model: a.Model, Family: a.Family, Verdict: a.Verdict, Host: a.Host}
}

// toWireDecision is the one place that carries router.Decision's fields onto
// the generated wire.Decision. Loads widens from int to int64; Authorised
// moves from a pointer to a string slice to a pointer to the generated
// HostAllowance wrapper. convert_test.go's reflection parity test covers it
// the same way as toWireAlias.
func toWireDecision(d router.Decision) wire.Decision {
	out := wire.Decision{Asked: d.Asked, Family: d.Family, Verdict: d.Verdict, Host: d.Host, Model: d.Model, Endpoint: d.Endpoint, InstalledOn: d.InstalledOn, Withheld: d.Withheld, Loads: int64(d.Loads), Why: d.Why}
	if d.Authorised != nil {
		out.Authorised = &wire.HostAllowance{Hosts: append([]string{}, (*d.Authorised)...)}
	}
	return out
}

func (answer answerFunc) Models(fresh bool) (wire.ModelsSnapshot, error) {
	out, err := answer(router.Request{Op: router.OpModels, Fresh: fresh})
	if err != nil {
		return wire.ModelsSnapshot{}, err
	}
	value := wire.ModelsSnapshot{Observation: observation(out)}
	for _, f := range out.Models {
		value.Models = append(value.Models, toWireFamily(f))
	}
	return fitReply("Models", value)
}
func (answer answerFunc) Hosts(fresh bool) (wire.HostsSnapshot, error) {
	out, err := answer(router.Request{Op: router.OpResidency, Fresh: fresh})
	if err != nil {
		return wire.HostsSnapshot{}, err
	}
	value := wire.HostsSnapshot{Observation: observation(out), Doubled: out.Doubled}
	for _, h := range out.Hosts {
		value.Hosts = append(value.Hosts, toWireHostState(h))
	}
	for _, a := range out.Asked {
		value.Asked = append(value.Asked, toWireAsk(a))
	}
	return fitReply("Hosts", value)
}

// fromWirePickRequest is the one place that carries wire.PickRequest's
// fields onto router.Request for Pick, the reverse of the toWire* functions
// above. Op is computed rather than carried: every Pick call performs
// router.OpRoute, and the wire request has no operation field to read it
// from. Allowed's *wire.HostAllowance unwraps onto Hosts's *[]string, the
// field router.Request already uses for every operation.
// convert_test.go's reverse parity test covers it the same way as
// toWireAlias, with Op, Hosts and Allowed allowlisted for the reasons above.
func fromWirePickRequest(p wire.PickRequest) router.Request {
	req := router.Request{Op: router.OpRoute, Model: p.Model, Fresh: p.Fresh, Profile: p.Profile}
	if p.Allowed != nil {
		hosts := append([]string{}, p.Allowed.Hosts...)
		req.Hosts = &hosts
	}
	return req
}

func (answer answerFunc) Pick(request wire.PickRequest) (wire.PickResult, error) {
	out, err := answer(fromWirePickRequest(request))
	if err != nil {
		return wire.PickResult{}, err
	}
	if out.Decision == nil {
		return wire.PickResult{}, &wire.ServiceError{Code: router.CodeInternal, Message: "provider returned no decision"}
	}
	value := wire.PickResult{Observation: observation(out), Decision: toWireDecision(*out.Decision)}
	return value, nil
}

// fromWireOfferRequest is the one place that carries wire.OfferRequest's
// fields onto router.Request for Offer. Op is computed: every Offer call
// performs router.OpOffer. Placement moves from the generated closed enum to
// its wire word; a value outside the enum reads "" and the provider refuses
// it as invalid_request. convert_test.go's reverse parity test covers it.
func fromWireOfferRequest(o wire.OfferRequest) router.Request {
	placement, _ := o.Placement.WireName()
	return router.Request{Op: router.OpOffer, Profile: o.Profile, Placement: placement, Reserve: o.Reserve, Fresh: o.Fresh}
}

// toWireOffer is the one place that carries router.Offer's fields onto the
// generated wire.Offer. convert_test.go's reflection parity test covers it.
func toWireOffer(o router.Offer) wire.Offer {
	return wire.Offer{Family: o.Family, Names: o.Names, Servers: o.Servers, Context: o.Context, ContextSource: wire.ContextSource(o.ContextSource), Input: o.Input, Unknown: o.Unknown, Title: o.Title}
}

func (answer answerFunc) Offer(request wire.OfferRequest) (wire.OfferSnapshot, error) {
	out, err := answer(fromWireOfferRequest(request))
	if err != nil {
		var refusal *offerRefusal
		if errors.As(err, &refusal) {
			return wire.OfferSnapshot{Outcome: refusal.outcome, Offers: []wire.Offer{}}, nil
		}
		return wire.OfferSnapshot{}, err
	}
	value := wire.OfferSnapshot{Outcome: wire.OfferOutcomeOffered, Observation: observation(out), Offers: []wire.Offer{}}
	for _, o := range out.Offers {
		value.Offers = append(value.Offers, toWireOffer(o))
	}
	return fitReply("Offer", value)
}

// ObserveOffer answers router@1 ObserveOffer through the same answer, gate
// and refusals as Offer; a refusal carries an empty cursor.
func (answer answerFunc) ObserveOffer(request wire.OfferRequest, cursor string, waitMs int64) (wire.OfferObservation, error) {
	refused := func(outcome wire.OfferOutcome) wire.OfferObservation {
		return wire.OfferObservation{Outcome: outcome, Offers: []wire.Offer{}}
	}
	if waitMs < 0 || waitMs > router.MaxOfferWait.Milliseconds() {
		return refused(wire.OfferOutcomeInvalid), nil
	}
	req := fromWireOfferRequest(request)
	req.Op, req.Cursor, req.Wait = router.OpObserveOffer, cursor, time.Duration(waitMs)*time.Millisecond
	out, err := answer(req)
	if err != nil {
		var refusal *offerRefusal
		if errors.As(err, &refusal) {
			return refused(refusal.outcome), nil
		}
		return wire.OfferObservation{}, err
	}
	value := wire.OfferObservation{Outcome: wire.OfferOutcomeOffered, Observation: observation(out), Offers: []wire.Offer{}, Cursor: out.Cursor}
	for _, o := range out.Offers {
		value.Offers = append(value.Offers, toWireOffer(o))
	}
	return fitReply("ObserveOffer", value)
}

// offerRefusal carries a business decision made after caller binding. Older
// router methods retain their published response channel.
type offerRefusal struct{ outcome wire.OfferOutcome }

func (e *offerRefusal) Error() string { return "router offer: " + e.outcome.String() }

type modalityRefusal struct{ outcome wire.ModalityOutcome }

func (e *modalityRefusal) Error() string { return "router modalities: " + e.outcome.String() }
func modalityDecisionError(err error) error {
	var refusal *wire.ServiceError
	if errors.As(err, &refusal) {
		switch refusal.Code {
		case CodeForbidden:
			return &modalityRefusal{wire.ModalityOutcomeNotPermitted}
		case CodeUnavailable:
			return &modalityRefusal{wire.ModalityOutcomeUnavailable}
		}
	}
	return err
}
func (answer answerFunc) Modalities(cursor string, limit int64) (wire.ModalityPage, error) {
	out, err := answer(router.Request{Op: router.OpModalities, Cursor: cursor, Limit: limit})
	if err != nil {
		var refusal *modalityRefusal
		if errors.As(err, &refusal) {
			return wire.ModalityPage{Outcome: refusal.outcome, Modalities: []string{}}, nil
		}
		return wire.ModalityPage{}, err
	}
	if out.ModalityPage == nil {
		return wire.ModalityPage{Outcome: wire.ModalityOutcomeUnavailable, Modalities: []string{}}, nil
	}
	p := out.ModalityPage
	outcome, ok := wire.ParseModalityOutcome(p.Outcome)
	if !ok || len(p.Modalities) > 64 || len(p.Next) > 256 || !utf8.ValidString(p.Next) {
		return wire.ModalityPage{Outcome: wire.ModalityOutcomeUnavailable, Modalities: []string{}}, nil
	}
	for _, profile := range p.Modalities {
		if !router.ValidModality(profile) {
			return wire.ModalityPage{Outcome: wire.ModalityOutcomeUnavailable, Modalities: []string{}}, nil
		}
	}
	return wire.ModalityPage{Outcome: outcome, Modalities: p.Modalities, Next: p.Next, Complete: p.Complete}, nil
}
