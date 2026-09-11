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
	listener listen.Listener
	router   *router.Router
	ctx      context.Context
	cancel   context.CancelFunc
	once     sync.Once
	workers  sync.WaitGroup
	OnError  func(error)
}

func Listen(endpoint string, provider *router.Router) (*Host, error) {
	if provider == nil {
		return nil, errors.New("router service: nil provider")
	}
	if err := identity.CanEver(router.Bound); err != nil {
		return nil, fmt.Errorf("router service cannot bind callers: %w", err)
	}
	l, err := listen.Listen(endpoint)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Host{listener: l, router: provider, ctx: ctx, cancel: cancel}, nil
}
func (h *Host) Close() error {
	var err error
	h.once.Do(func() { h.cancel(); err = h.listener.Close() })
	return err
}
func (h *Host) Serve(ctx context.Context) error {
	stop := context.AfterFunc(ctx, func() { _ = h.Close() })
	defer stop()
	defer h.workers.Wait()
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
				dispatch := wire.RouterDispatcher{Handler: &receiver{provider: h.router, call: call}}
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
}

func (r *receiver) answer(request router.Request) (router.Response, error) {
	if r.call == nil {
		return router.Response{}, &wire.ServiceError{Code: router.CodeCallerRefused, Message: "caller is not bound"}
	}
	if err := r.call.Recheck(); err != nil {
		return router.Response{}, &wire.ServiceError{Code: router.CodeCallerRefused, Message: "caller binding no longer valid"}
	}
	// Caller is created by ReceiveFramed after checking router.Bound, never decoded.
	out := r.provider.Answer(request, r.call.Caller)
	if out.Err() != nil {
		code := out.Code
		if code == "" {
			code = router.CodeInternal
		}
		return out, &wire.ServiceError{Code: code, Message: out.Error}
	}
	return out, nil
}
func observation(r router.Response) wire.Observation {
	return wire.Observation{Caller: wire.Caller{UserDescription: r.Caller.User, PathDescription: r.Caller.Path}, TookMs: r.TookMS, CacheAgeMs: r.AgeMS}
}
func (r *receiver) Models(fresh bool) (wire.ModelsSnapshot, error) {
	out, err := r.answer(router.Request{Op: router.OpModels, Fresh: fresh})
	if err != nil {
		return wire.ModelsSnapshot{}, err
	}
	value := wire.ModelsSnapshot{Observation: observation(out)}
	for _, f := range out.Models {
		family := wire.Family{Family: f.Family}
		for _, a := range f.Names {
			family.Names = append(family.Names, wire.Alias{Host: a.Host, Name: a.Name, Resident: a.Resident, Servable: a.Servable})
		}
		value.Models = append(value.Models, family)
	}
	return value, nil
}
func (r *receiver) Hosts(fresh bool) (wire.HostsSnapshot, error) {
	out, err := r.answer(router.Request{Op: router.OpResidency, Fresh: fresh})
	if err != nil {
		return wire.HostsSnapshot{}, err
	}
	value := wire.HostsSnapshot{Observation: observation(out), Doubled: out.Doubled}
	for _, h := range out.Hosts {
		value.Hosts = append(value.Hosts, wire.HostState{Host: h.Host, Base: h.Base, Up: h.Up, Why: h.Why, Installed: int64(h.Installed), Resident: h.Resident, Servable: h.Servable})
	}
	for _, a := range out.Asked {
		value.Asked = append(value.Asked, wire.Ask{At: a.At.UTC().Format("2006-01-02T15:04:05.000000Z"), Caller: a.Caller, User: a.User, Model: a.Model, Family: a.Family, Verdict: a.Verdict, Host: a.Host})
	}
	return value, nil
}
func (r *receiver) Pick(request wire.PickRequest) (wire.PickResult, error) {
	input := router.Request{Op: router.OpRoute, Model: request.Model, Fresh: request.Fresh}
	if request.Allowed != nil {
		hosts := append([]string{}, request.Allowed.Hosts...)
		input.Hosts = &hosts
	}
	out, err := r.answer(input)
	if err != nil {
		return wire.PickResult{}, err
	}
	if out.Decision == nil {
		return wire.PickResult{}, &wire.ServiceError{Code: router.CodeInternal, Message: "provider returned no decision"}
	}
	d := out.Decision
	value := wire.PickResult{Observation: observation(out), Decision: wire.Decision{Asked: d.Asked, Family: d.Family, Verdict: d.Verdict, Host: d.Host, Model: d.Model, Endpoint: d.Endpoint, InstalledOn: d.InstalledOn, Withheld: d.Withheld, Loads: int64(d.Loads), Why: d.Why}}
	if d.Authorised != nil {
		value.Decision.Authorised = &wire.HostAllowance{Hosts: append([]string{}, (*d.Authorised)...)}
	}
	return value, nil
}
