package router

import (
	"runtime"
	"testing"

	identity "github.com/openabstractions/abstraction-identity"
	"github.com/openabstractions/abstraction-identity/listen"
)

// Architect ruling 2026-11-09 (provider-xpc-endpoints-on-macos): Start's
// guard follows the endpoint's transport ceiling. An xpc: endpoint passes
// where XPC can prove the program (macOS); a socket follows the native
// ceiling, which refuses on macOS and passes on Windows and Linux.
func TestStartGuardFollowsEndpointCeiling(t *testing.T) {
	need := Bound
	for _, endpoint := range []string{"xpc:com.openabstractions.test.router", "/tmp/oa-router-guard.sock"} {
		limits, err := listen.Ceiling(endpoint)
		want := err
		if err == nil {
			want = limits.CanEver(need)
		}
		got := startGuard(endpoint)
		if (got == nil) != (want == nil) {
			t.Fatalf("%s: guard %v, ceiling %v", endpoint, got, want)
		}
	}
	if err := startGuard("xpc:com.openabstractions.test.router"); (err == nil) != (runtime.GOOS == "darwin") {
		t.Fatalf("xpc endpoint on %s: %v", runtime.GOOS, err)
	}
	native := identity.CanEver(need)
	if err := startGuard("/tmp/oa-router-guard.sock"); (err == nil) != (native == nil) {
		t.Fatalf("socket endpoint: guard %v, native ceiling %v", err, native)
	}
	if runtime.GOOS == "darwin" && native == nil {
		t.Fatal("a macOS socket proved the program")
	}
}
