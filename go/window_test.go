package router

import (
	"context"
	"net"
	"testing"
	"time"

	identity "github.com/openabstractions/abstraction-identity"
	"github.com/openabstractions/abstraction-identity/listen"
)

func TestWindowIsRetired(t *testing.T) {
	surveys := 0
	r := New(&Host{Name: "inert", listing: func(context.Context, *Host, map[string]string) ([]string, error) {
		surveys++
		return nil, nil
	}})
	probe, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"127.0.0.1:0", "0.0.0.0:0", "[::1]:0", addr} {
		if l, err := Window(address, r); l != nil || err != errWindowRetired || err.Error() != "router: the loopback window is retired: a loopback TCP caller cannot be bound at the proof this service requires (identity listen.Program); use the router endpoint" {
			t.Fatalf("Window(%q) = %v, %v", address, l, err)
		}
	}
	if surveys != 0 {
		t.Fatal("retired window surveyed hosts", surveys)
	}
	if conn, err := net.DialTimeout("tcp4", addr, time.Second); err == nil {
		conn.Close()
		t.Fatal("retired window opened a socket")
	}
	if l, err := Window("", nil); l != nil || err != errWindowRetired {
		t.Fatal("retirement depends on router or address", l, err)
	}
}

func TestWindowLoopbackCeilingCannotMeetProgram(t *testing.T) {
	best, need := identity.LoopbackCeiling().Best, listen.Program
	below := false
	for _, field := range []struct {
		name       string
		best, need identity.Proof
	}{
		{"user", best.User, need.User}, {"process", best.Process, need.Process},
		{"path", best.Path, need.Path}, {"package", best.Package, need.Package}, {"code", best.Code, need.Code},
	} {
		if field.need == 0 {
			continue
		}
		t.Logf("%s: loopback=%v required=%v", field.name, field.best, field.need)
		below = below || field.best < field.need
	}
	if !below {
		t.Fatal("loopback can meet listen.Program; reopen the Window admission decision")
	}
}
