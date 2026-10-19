package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/openabstractions/abstraction-identity/go/privatedir/privatetest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	identity "github.com/openabstractions/abstraction-identity"
	"github.com/openabstractions/abstraction-identity/listen"
	"github.com/openabstractions/abstraction-rights/go/authorization/gate"
	"github.com/openabstractions/abstraction-router/go/internal/admission"
)

type legacyPeerControl struct {
	peer   *identity.Peer
	calls  int
	retire bool
}

func (c *legacyPeerControl) Peer() (*identity.Peer, error) {
	c.calls++
	if c.retire && c.calls > 1 {
		return nil, errors.New("retired binding")
	}
	return c.peer, nil
}

func legacyCountedRouter() (*Router, *atomic.Int32) {
	count := new(atomic.Int32)
	read := func(context.Context, *Host) ([]string, error) { count.Add(1); return []string{"fixture"}, nil }
	return New(&Host{Name: "fixture", installed: read, resident: read}), count
}

func legacyFrame(t *testing.T, op string) []byte {
	t.Helper()
	b, err := json.Marshal(Request{Op: op, Fresh: true, Model: "fixture", Placement: PlacementAny})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestLegacyGateRefusesBeforeEveryProviderFront(t *testing.T) {
	for _, op := range []string{OpModels, OpResidency, OpRoute, OpOffer} {
		for _, tc := range []struct {
			name, code string
			policy     gate.Gate
			noPeer     bool
		}{
			{"denied", admission.CodeForbidden, func(context.Context, *identity.Peer, string, string) error { return errors.New("denied") }, false},
			{"unavailable", CodeUnavailable, func(context.Context, *identity.Peer, string, string) error { return gate.ErrUnavailable }, false},
			{"missing policy", CodeUnavailable, nil, false},
			{"missing proof", CodeCallerRefused, func(context.Context, *identity.Peer, string, string) error {
				t.Fatal("unproved caller reached policy")
				return nil
			}, true},
		} {
			t.Run(op+"/"+tc.name, func(t *testing.T) {
				r, count := legacyCountedRouter()
				c := &legacyPeerControl{peer: &identity.Peer{}}
				if tc.noPeer {
					c.peer = nil
				}
				s := &Service{r: r, policy: tc.policy}
				out := s.answer(context.Background(), c, legacyFrame(t, op), listen.Seen{Bound: true, User: "claimed", Path: "claimed"})
				if out.Code != tc.code || out.Error == "" || count.Load() != 0 || len(r.Asked()) != 0 {
					t.Fatalf("response=%+v provider=%d audit=%d", out, count.Load(), len(r.Asked()))
				}
			})
		}
	}
}

func TestLegacyGateMapsEveryFrontAndRechecksAfterDecision(t *testing.T) {
	for _, op := range []string{OpModels, OpResidency, OpRoute, OpOffer} {
		for _, mode := range []string{"allowed", "retired", "cancelled"} {
			t.Run(op+"/"+mode, func(t *testing.T) {
				r, count := legacyCountedRouter()
				c := &legacyPeerControl{peer: &identity.Peer{}, retire: mode == "retired"}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				calls := 0
				s := &Service{r: r, policy: func(_ context.Context, p *identity.Peer, a, res string) error {
					calls++
					wantA, wantR := admission.ActionInventory, admission.ResourceInventory
					if op == OpRoute {
						wantA, wantR = admission.ActionRoute, admission.ResourceRoutes
					}
					if p != c.peer || a != wantA || res != wantR {
						t.Fatalf("wrong gate input %p %q %q", p, a, res)
					}
					if mode == "cancelled" {
						cancel()
					}
					return nil
				}}
				out := s.answer(ctx, c, legacyFrame(t, op), listen.Seen{Bound: true, Path: "fixture", User: "fixture"})
				if calls != 1 {
					t.Fatalf("gate calls %d", calls)
				}
				if mode == "allowed" {
					if c.calls != 2 || count.Load() == 0 {
						t.Fatalf("no admitted provider: %+v peers=%d reads=%d", out, c.calls, count.Load())
					}
				} else if count.Load() != 0 || len(r.Asked()) != 0 || out.Code == "" {
					t.Fatalf("post-decision refusal failed: %+v reads=%d", out, count.Load())
				}
			})
		}
	}
}

func TestLegacyGateKeepsMalformedAndUnknownRequestsInert(t *testing.T) {
	for _, tc := range []struct {
		frame string
		code  string
	}{{"{", CodeInvalidRequest}, {`{"op":"unknown"}`, CodeUnknownOperation}} {
		r, count := legacyCountedRouter()
		s := &Service{r: r, policy: func(context.Context, *identity.Peer, string, string) error {
			t.Fatal("invalid request reached policy")
			return nil
		}}
		out := s.answer(context.Background(), &legacyPeerControl{peer: &identity.Peer{}}, []byte(tc.frame), listen.Seen{})
		if out.Code != tc.code || count.Load() != 0 || len(r.Asked()) != 0 {
			t.Fatalf("%+v reads=%d", out, count.Load())
		}
	}
}

var legacyEndpointSerial atomic.Uint64

func legacyEndpoint(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return fmt.Sprintf(`\\.\pipe\oa-legacy-gate-%d-%d`, os.Getpid(), legacyEndpointSerial.Add(1))
	}
	return filepath.Join(privatetest.Dir(t), "router.sock")
}

func TestLegacyGateFactoryCannotListenWithoutPolicy(t *testing.T) {
	for _, tc := range []struct {
		name  string
		local func(string) (gate.Gate, error)
	}{
		{"failed", func(string) (gate.Gate, error) { return nil, errors.New("account lookup failed") }},
		{"nil", func(string) (gate.Gate, error) { return nil, nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := legacyEndpoint(t)
			s, err := start(endpoint, New(), tc.local)
			if err == nil || s != nil {
				t.Fatal("missing policy started a listener")
			}
			if c, err := listen.Dial(endpoint); err == nil {
				c.Close()
				t.Fatal("refused factory left listener")
			}
		})
	}
}

func TestLegacyGateNativeAllFronts(t *testing.T) {
	if err := identity.CanEver(Bound); err != nil {
		t.Skip(err)
	}
	for _, denied := range []bool{true, false} {
		r, count := legacyCountedRouter()
		endpoint := legacyEndpoint(t)
		var s *Service
		var err error
		if denied {
			s, err = start(endpoint, r, func(string) (gate.Gate, error) {
				return func(context.Context, *identity.Peer, string, string) error { return errors.New("denied") }, nil
			})
		} else {
			s, err = Start(endpoint, r)
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, op := range []string{OpModels, OpResidency, OpRoute, OpOffer} {
			out, err := (&Client{Endpoint: endpoint}).Ask(Request{Op: op, Fresh: true, Model: "fixture", Placement: PlacementAny})
			if err != nil && out.Code == "" {
				s.Close()
				t.Fatal(err)
			}
			if denied && out.Code != admission.CodeForbidden {
				t.Fatalf("%s: %+v", op, out)
			}
			if !denied && (out.Code == CodeCallerRefused || out.Code == CodeUnavailable || out.Code == admission.CodeForbidden) {
				t.Fatalf("local %s: %+v", op, out)
			}
		}
		if denied && (count.Load() != 0 || len(r.Asked()) != 0) {
			t.Fatal("denied native caller reached provider")
		}
		if !denied && count.Load() == 0 {
			t.Fatal("local native caller never reached provider")
		}
		s.Close()
	}
}

func TestLegacyGateNativeDisconnectAndShutdownCancelDecision(t *testing.T) {
	if err := identity.CanEver(Bound); err != nil {
		t.Skip(err)
	}
	for _, shutdown := range []bool{false, true} {
		t.Run(fmt.Sprint(shutdown), func(t *testing.T) {
			entered, ended := make(chan struct{}), make(chan struct{})
			r, count := legacyCountedRouter()
			endpoint := legacyEndpoint(t)
			s, err := start(endpoint, r, func(string) (gate.Gate, error) {
				return func(ctx context.Context, _ *identity.Peer, _, _ string) error {
					close(entered)
					<-ctx.Done()
					close(ended)
					return nil
				}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			c, err := listen.Dial(endpoint)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if _, err = c.Write(append(legacyFrame(t, OpRoute), '\n')); err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("gate not entered")
			}
			if shutdown {
				s.Close()
			} else {
				c.Close()
			}
			select {
			case <-ended:
			case <-time.After(5 * time.Second):
				t.Fatal("decision context survived owner close")
			}
			if count.Load() != 0 || len(r.Asked()) != 0 {
				t.Fatal("cancelled decision reached provider")
			}
		})
	}
}
