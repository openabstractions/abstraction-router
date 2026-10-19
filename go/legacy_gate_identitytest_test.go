//go:build oa_identitytest

package router

import (
	"context"
	"testing"

	identity "github.com/openabstractions/abstraction-identity"
	"github.com/openabstractions/abstraction-identity/identitytest"
	"github.com/openabstractions/abstraction-identity/listen"
	"github.com/openabstractions/abstraction-rights/go/authorization/gate"
	"github.com/openabstractions/abstraction-router/go/internal/admission"
)

// Receive's native Bound admission is exercised separately. These synthetic
// account facts exercise the shared policy owner without impersonating an OS user.
// They deliberately carry no Process proof, so the shared owner returns unavailable
// rather than evaluating the synthetic account as a native caller. The policy
// owner's own native wrong-account receiving remains a separate control.
func TestLegacyGateWrongAccountCannotUseSerializedCaller(t *testing.T) {
	policy, err := gate.Local(admission.Namespace)
	if err != nil {
		t.Fatal(err)
	}
	peer := identitytest.New(t, identitytest.Facts{User: identity.User{Kind: "windows", SID: "S-1-5-21-1736-9999"}, Path: "C:/fixture/foreign.exe"})[0]
	for _, op := range []string{OpModels, OpResidency, OpRoute, OpOffer} {
		r, count := legacyCountedRouter()
		s := &Service{r: r, policy: policy}
		out := s.answer(context.Background(), &legacyPeerControl{peer: peer}, legacyFrame(t, op), listen.Seen{Bound: true, User: "local account claim", Path: "approved program claim"})
		if out.Code != CodeUnavailable || count.Load() != 0 || len(r.Asked()) != 0 {
			t.Fatalf("%s: %+v provider=%d", op, out, count.Load())
		}
	}
}
