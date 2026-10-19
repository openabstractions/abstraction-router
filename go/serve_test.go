package router

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stdoutOf runs f with os.Stdout redirected and returns what f wrote there.
func stdoutOf(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	func() {
		defer func() { os.Stdout = saved }()
		f()
	}()
	w.Close()
	out, _ := io.ReadAll(r)
	r.Close()
	return string(out)
}

func TestServeHelp(t *testing.T) {
	out := stdoutOf(t, func() {
		if err := Serve([]string{"--help"}); err != nil {
			t.Fatal(err)
		}
	})
	first, _, _ := strings.Cut(out, "\n")
	if first != "Usage: openabstractions serve router [options]" {
		t.Fatalf("first line = %q", first)
	}
	if endpoint := DefaultEndpoint(); endpoint != "" && strings.Contains(out, endpoint) {
		t.Fatalf("usage names this machine's resolved endpoint: %s", out)
	}
}

func TestServeBadFlagReturnsErrUsage(t *testing.T) {
	err := Serve([]string{"--not-a-real-flag"})
	if !errors.Is(err, ErrUsage) {
		t.Fatalf("err = %v, want errors.Is(err, ErrUsage)", err)
	}
}

func TestServeWindowRefusesBeforeEndpointStart(t *testing.T) {
	endpoint := filepath.Join(t.TempDir(), "never-opened.sock")
	if err := Serve([]string{"--endpoint", endpoint, "--window", "127.0.0.1:0"}); err != errWindowRetired {
		t.Fatal("window did not refuse before Start", err)
	}
	if _, err := os.Lstat(endpoint); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("retired window created endpoint", err)
	}
}
