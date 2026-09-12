package router

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// go vet type-checks the _test.go files that go build ignores, which is the
// only reason this catches anything: on 2026-09-08 the shipped code of this
// module already cross-compiled and its tests did not, so `go build` was green
// and the repository gate was red. The gate is thirty-three minutes away and
// this is three seconds, which is the whole point of it being here.
func TestEveryPlatformThisModuleClaimsStillTypeChecks(t *testing.T) {
	for _, target := range []string{"windows/amd64", "linux/amd64", "darwin/arm64"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			goos, goarch, _ := strings.Cut(target, "/")
			vet := exec.Command("go", "vet", "./...")
			// These are pure-Go target checks; the native race-test compiler
			// cannot compile runtime/cgo for a different operating system.
			vet.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0", "GOPROXY=off")
			out, err := vet.CombinedOutput()
			if errors.Is(err, exec.ErrNotFound) {
				t.Fatal("no go toolchain on PATH, so this module's portability is unproven rather than proven")
			}
			if err != nil {
				t.Fatalf("%s: %v\n%s", target, err, out)
			}
		})
	}
}
