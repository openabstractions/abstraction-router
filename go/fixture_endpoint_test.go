package router

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
)

var routerFixtureSequence atomic.Uint64

func routerFixtureEndpoint(t *testing.T, prefix string) string {
	t.Helper()
	name := fmt.Sprintf("oa-test-%s-%d-%d", prefix, os.Getpid(), routerFixtureSequence.Add(1))
	if runtime.GOOS == "windows" {
		return `\\.\pipe\` + name
	}
	return filepath.Join(t.TempDir(), name+".sock")
}
