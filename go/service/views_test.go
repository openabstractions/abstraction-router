package service

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"os/user"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	identity "github.com/openabstractions/abstraction-identity"
	"github.com/openabstractions/abstraction-identity/listen"
	"github.com/openabstractions/abstraction-identity/remote"
	rights "github.com/openabstractions/abstraction-rights/go"
	rightswire "github.com/openabstractions/abstraction-rights/go/abstraction/rights/api"
	"github.com/openabstractions/abstraction-rights/go/authorization"
	rightsclient "github.com/openabstractions/abstraction-rights/go/client"
	router "github.com/openabstractions/abstraction-router/go"
	wire "github.com/openabstractions/abstraction-router/go/abstraction/router"
)

func remoteCaller(t *testing.T, key string) RemoteCaller {
	t.Helper()
	spki := []byte(key)
	p, err := remote.SubjectOf(remote.Peer{Certificate: &x509.Certificate{RawSubjectPublicKeyInfo: spki}, Key: sha256.Sum256(spki)}, func(remote.Peer) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	return RemoteCaller{Peer: p, Domain: "display-only"}
}

func TestRemoteViewsPolicyRunsBeforeProvider(t *testing.T) {
	caller := remoteCaller(t, "first key")
	for _, policy := range []RemotePolicy{nil, func(context.Context, RemoteCaller, string, string) error { return errors.New("refused") }, func(context.Context, RemoteCaller, string, string) error { return ErrPolicyUnavailable }} {
		// A nil provider would panic if dispatch reached it. Every operation is
		// refused before inventory, fresh survey, routing or audit mutation.
		v := Remote(context.Background(), nil, caller, policy)
		if _, err := v.Models(true); err == nil {
			t.Fatal("Models allowed")
		}
		if _, err := v.Hosts(true); err == nil {
			t.Fatal("Hosts allowed")
		}
		if _, err := v.Pick(wire.PickRequest{Fresh: true}); err == nil {
			t.Fatal("Pick allowed")
		}
		o, err := v.Offer(wire.OfferRequest{Fresh: true, Placement: wire.PlacementAny})
		if err != nil || (o.Outcome != wire.OfferOutcomeNotPermitted && o.Outcome != wire.OfferOutcomeUnavailable) {
			t.Fatalf("Offer %+v %v", o, err)
		}
	}
}

func TestRemoteViewsActionsAndHonestAttribution(t *testing.T) {
	r := router.New()
	caller := remoteCaller(t, "first key")
	var actions []string
	v := Remote(context.Background(), r, caller, func(_ context.Context, got RemoteCaller, action, resource string) error {
		if got.Peer.Program() != caller.Peer.Program() {
			t.Fatal("caller changed")
		}
		actions = append(actions, action+" "+resource)
		return nil
	})
	v.Models(false)
	v.Hosts(false)
	v.Offer(wire.OfferRequest{Placement: wire.PlacementAny})
	v.Pick(wire.PickRequest{Model: "missing"})
	// Models, Hosts and Offer read under inventory.read; Pick decides route
	// (CONTRACT.md ROUTE-A2).
	if len(actions) != 4 || actions[0] != ActionInventory+" "+ResourceInventory || actions[1] != actions[0] || actions[2] != actions[0] || actions[3] != ActionRoute+" "+ResourceRoutes {
		t.Fatalf("actions %v", actions)
	}
	audit := r.Asked()
	if len(audit) != 1 || audit[0].Caller != caller.Peer.Program() || audit[0].User != caller.Domain {
		t.Fatalf("audit %+v", audit)
	}
	out := r.AnswerRemoteContext(context.Background(), router.Request{Op: router.OpRoute}, caller.Peer, caller.Domain)
	if out.Caller.Bound || out.Caller.Path != "" || out.Caller.User != "" {
		t.Fatalf("fabricated native identity %+v", out.Caller)
	}
	if out := r.Answer(router.Request{Op: router.OpRoute}, listen.Seen{}); out.Code != router.CodeCallerRefused {
		t.Fatal("native unbound Pick allowed")
	}
}

func TestReadonlyPickRetainsAuditWithoutDomainActuation(t *testing.T) {
	host := &router.Host{Name: "declared", Base: "http://127.0.0.1:1", Wire: router.WireOpenAICompatible, Credential: "unchanged"}
	r := router.New(host)
	caller := remoteCaller(t, "readonly audit")
	var actions []string
	v := Remote(context.Background(), r, caller, func(_ context.Context, _ RemoteCaller, action, resource string) error {
		actions = append(actions, action+" "+resource)
		return nil
	})
	models, at := r.Models(false)
	hostsBefore := *host
	first, err := v.Pick(wire.PickRequest{Model: "missing", Fresh: false})
	if err != nil {
		t.Fatal(err)
	}
	second, err := v.Pick(wire.PickRequest{Model: "missing", Fresh: false})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Decision, second.Decision) {
		t.Fatal("read changed routing answer")
	}
	after, afterAt := r.Models(false)
	if !reflect.DeepEqual(models, after) || !at.Equal(afterAt) || !reflect.DeepEqual(hostsBefore, *host) || len(r.Hosts()) != 1 || r.Hosts()[0] != host {
		t.Fatal("Pick changed provider registration, credential, model inventory or snapshot")
	}
	if !reflect.DeepEqual(actions, []string{ActionRoute + " " + ResourceRoutes, ActionRoute + " " + ResourceRoutes}) {
		t.Fatal(actions)
	}
	audit := r.Asked()
	if len(audit) != 2 || audit[0].Caller != caller.Peer.Program() {
		t.Fatal("missing permitted read audit", audit)
	}
	listed, err := v.Hosts(false)
	if err != nil || len(listed.Asked) != 2 {
		t.Fatal("audit not readable", listed, err)
	}
}

func TestRemoteViewsCancellationAndDistinctKeys(t *testing.T) {
	first, second := remoteCaller(t, "first"), remoteCaller(t, "second")
	if first.Domain != second.Domain || first.Peer.Program() == second.Peer.Program() {
		t.Fatal("display label merged certificate identities")
	}
	ctx, cancel := context.WithCancel(context.Background())
	called := 0
	v := Remote(ctx, nil, first, func(context.Context, RemoteCaller, string, string) error { called++; cancel(); return nil })
	_, err := v.Models(true)
	var refusal *wire.ServiceError
	if !errors.As(err, &refusal) || refusal.Code != wire.ServiceErrorCode(CodeUnavailable) || called != 1 {
		t.Fatalf("canceled decision: %v calls=%d", err, called)
	}
	_, err = v.Hosts(true)
	if err == nil || called != 1 {
		t.Fatal("canceled context called policy or provider")
	}
}

func TestRemoteRightsUsesNativeDecideForEnforcer(t *testing.T) {
	if err := identity.CanEver(listen.Program); err != nil {
		t.Skip(err)
	}
	owner, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	p, err := rights.LoadDecisionPolicy(filepath.Join(t.TempDir(), "policy.json"), []string{ActionInventory, ActionRoute})
	if err != nil {
		t.Fatal(err)
	}
	caller := remoteCaller(t, "native relay key")
	subject := rightswire.Subject{Account: owner.Uid, Program: caller.Peer.Program()}
	if err := p.Set(subject, ActionInventory, ResourceInventory, true); err != nil {
		t.Fatal(err)
	}
	endpoint := endpointFor(t)
	var enforcerCalls atomic.Int64
	h, err := authorization.Listen(endpoint, p, func(_ context.Context, peer *identity.Peer, action, resource string) bool {
		enforcerCalls.Add(1)
		return peer != nil && action == ActionInventory && resource == ResourceInventory
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- h.Serve(context.Background()) }()
	t.Cleanup(func() {
		h.Close()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	policy := RemoteRights(owner.Uid, rightsclient.New(endpoint))
	if err := policy(context.Background(), caller, ActionInventory, ResourceInventory); err != nil {
		t.Fatal(err)
	}
	if err := policy(context.Background(), caller, ActionRoute, ResourceRoutes); err == nil {
		t.Fatal("native enforcer bypassed")
	}
	if err := RemoteRights("foreign-account", rightsclient.New(endpoint))(context.Background(), caller, ActionInventory, ResourceInventory); err == nil {
		t.Fatal("receiving account bypassed")
	}
	if enforcerCalls.Load() != 2 {
		t.Fatalf("enforcer calls %d", enforcerCalls.Load())
	}
}
