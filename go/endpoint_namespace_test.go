package router

import (
	"errors"
	"github.com/openabstractions/abstraction-facade/go-core/bootstrap"

	"os"
	"runtime"
	"testing"
)

func TestLegacyDefaultRefusesBeforeNamespaceIO(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix namespace failure")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_RUNTIME_DIR", dir)
	if DefaultEndpoint() != "" {
		t.Fatal("default resolution fell back")
	}
	c := &Client{Endpoint: DefaultEndpoint()}
	_, err := c.Ask(Request{})
	if !errors.Is(err, bootstrap.ErrNoTrustedInstallation) {
		t.Fatalf("lost namespace refusal: %v", err)
	}
}

func TestLegacyDefaultRefusesInstalledNamespace(t *testing.T) {
	if runtime.GOOS != "windows" {
		dir := t.TempDir()
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("XDG_RUNTIME_DIR", dir)
	}
	if endpoint, err := bootstrap.Endpoint("router"); endpoint != "" || !errors.Is(err, bootstrap.ErrNoTrustedInstallation) {
		t.Fatalf("default lookup: %q %v", endpoint, err)
	}
	if got := DefaultEndpoint(); got != "" {
		t.Fatalf("default endpoint escaped test refusal: %q", got)
	}
}
