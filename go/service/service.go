// Package service binds the generated router protocol to the existing provider.
// It keeps legacy line framing and the HTTP window on their existing endpoints.
package service

import (
	"context"
	"errors"
	"fmt"
	identity "github.com/openabstractions/abstraction-identity"
	"github.com/openabstractions/abstraction-identity/listen"
	router "github.com/openabstractions/abstraction-router/go"
	wire "github.com/openabstractions/abstraction-router/go/abstraction/router"
	"sync"
	"time"
)

type Host struct {
	listener  listen.Listener
	router    *router.Router
	ctx       context.Context
	cancel    context.CancelFunc
	once      sync.Once
	workers   sync.WaitGroup
	lifecycle sync.Mutex
	serving   bool
	policy    Policy
	OnError   func(error)
	// OnStopped is called when admission stops. Assign it before Serve.
	OnStopped func()
}

// Router refusal codes for an explicit receiving policy.
const (
	CodeForbidden   = "forbidden"
	CodeUnavailable = "unavailable"
)

// Actions a Policy receives, and the resource each names.
const (
	// ActionInventory covers Models and Hosts; its resource is ResourceInventory.
	ActionInventory   = "abstraction.router/inventory.read"
	ResourceInventory = "abstraction.router/inventory"
	// ActionRoute covers Pick; its resource is ResourceRoutes for every model
	// (research/rights-defaults/DECISION.md §1). The per-host switch is
	// inference complete on host:<name>.
	ActionRoute    = "abstraction.router/route"
	ResourceRoutes = "abstraction.router/routes"
)

// Policy authorizes one router operation for the rechecked bound caller. It
// must honor ctx and be safe for concurrent calls. Wrap ErrPolicyUnavailable
// when the decision cannot be obtained; every other error is a refusal.
type Policy func(ctx context.Context, peer *identity.Peer, action, resource string) error

// ErrPolicyUnavailable distinguishes a failed decision lookup from refusal.
var ErrPolicyUnavailable = errors.New("router service: policy unavailable")

// EnablePolicy narrows every operation to callers the policy authorizes.
// Configure it before Serve. Without it bound callers keep access.
func (h *Host) EnablePolicy(policy Policy) error {
	h.lifecycle.Lock()
	defer h.lifecycle.Unlock()
	if h.serving || h.ctx.Err() != nil {
		return errors.New("router service: configure policy before Serve")
	}
	if policy == nil {
		return errors.New("router service: explicit policy required")
	}
	h.policy = policy
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
	for {
		conn, err := h.listener.Accept()
		if err != nil {
			if h.ctx.Err() != nil || ctx.Err() != nil {
				return nil
			}
			return err
		}
		h.workers.Add(1)
		go func() {
			defer h.workers.Done()
			defer conn.Close()
			requestCtx, cancel := context.WithTimeout(h.ctx, 10*time.Second)
			defer cancel()
			call, err := listen.ReceiveFramed(requestCtx, conn, router.Bound, 1<<20)
			if call != nil {
				defer call.Close()
			}
			if err == nil {
				dispatch := wire.RouterDispatcher{Handler: (&receiver{provider: h.router, call: call, policy: policy, ctx: requestCtx}).views()}
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
	provider *router.Router
	call     *listen.FramedCall
	policy   Policy
	ctx      context.Context
}

// authorize applies the configured policy to one operation after the caller
// binding is rechecked and before the provider is asked.
func (r *receiver) authorize(action, resource string) error {
	if r.policy == nil {
		return nil
	}
	ctx := r.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	peer, err := r.call.Peer()
	if err != nil {
		return &wire.ServiceError{Code: router.CodeCallerRefused, Message: "caller identity could not be rechecked"}
	}
	err = r.policy(ctx, peer, action, resource)
	if err == nil && ctx.Err() == nil {
		return nil
	}
	if ctx.Err() != nil || errors.Is(err, ErrPolicyUnavailable) {
		return &wire.ServiceError{Code: CodeUnavailable, Message: "rights:unavailable"}
	}
	return &wire.ServiceError{Code: CodeForbidden, Message: "router operation not permitted"}
}

func (r *receiver) answer(request router.Request) (router.Response, error) {
	if r.call == nil {
		return router.Response{}, &wire.ServiceError{Code: router.CodeCallerRefused, Message: "caller is not bound"}
	}
	if err := r.call.Recheck(); err != nil {
		return router.Response{}, &wire.ServiceError{Code: router.CodeCallerRefused, Message: "caller binding no longer valid"}
	}
	action, resource := ActionInventory, ResourceInventory
	if request.Op == router.OpRoute {
		action, resource = ActionRoute, ResourceRoutes
	}
	if err := r.authorize(action, resource); err != nil {
		return router.Response{}, err
	}
	// Caller is created by ReceiveFramed after checking router.Bound, never decoded.
	out := r.provider.Answer(request, r.call.Caller)
	return out, providerResponseError(out)
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

// toWireAlias is the one place that carries router.Alias's fields onto the
// generated wire.Alias. convert_test.go checks by reflection that the two
// structs' field sets still match, so a field added to either without this
// function updated fails a test instead of dropping silently.
func toWireAlias(a router.Alias) wire.Alias {
	return wire.Alias{Host: a.Host, Name: a.Name, Resident: a.Resident, Servable: a.Servable, Hosted: a.Hosted, Profiles: a.Profiles, HeldIn: a.HeldIn, ContextLength: a.ContextLength}
}

// toWireFamily is the one place that carries router.Family's fields onto the
// generated wire.Family, converting each of its Names through toWireAlias and
// each of its Components through toWireComponent. convert_test.go's
// reflection parity test covers it the same way as toWireAlias.
func toWireFamily(f router.Family) wire.Family {
	out := wire.Family{Family: f.Family, HeldIn: f.HeldIn, FamilySource: f.FamilySource}
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
	return wire.HostState{Host: h.Host, Base: h.Base, Up: h.Up, Why: h.Why, Installed: int64(h.Installed), Resident: h.Resident, Servable: h.Servable, Hosted: h.Hosted, Wire: h.Wire, Credential: h.Credential, DeclaredBy: h.DeclaredBy, Profiles: h.Profiles, Domain: h.Domain}
}

// askTimeLayout is the layout toWireAsk formats router.Ask's At into; it is
// also the timestamp format for the legacy wire.Ask over the framed
// protocol. convert_test.go parses wire.Ask's At back with the same layout
// to check the value round-trips.
const askTimeLayout = "2006-01-02T15:04:05.000000Z"

// toWireAsk is the one place that carries router.Ask's fields onto the
// generated wire.Ask. At formats router.Ask's time.Time as the string
// wire.Ask carries instead. convert_test.go's reflection parity test covers
// it the same way as toWireAlias.
func toWireAsk(a router.Ask) wire.Ask {
	return wire.Ask{At: a.At.UTC().Format(askTimeLayout), Caller: a.Caller, User: a.User, Model: a.Model, Family: a.Family, Verdict: a.Verdict, Host: a.Host}
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
	return value, nil
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
	return value, nil
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
