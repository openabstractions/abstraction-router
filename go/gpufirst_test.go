package router

import (
	"math"
	"os"
	"strconv"
	"testing"
)

// The first GPU reading in a process used to be a different number from the
// second — 2.81 GiB against 24.01 for the process holding this machine's model —
// and a residency answer is worth nothing if its first reading is wrong.
func TestTheFirstGPUReadingIsTheSecond(t *testing.T) {
	if os.Getenv("ROUTER_LIVE") == "" {
		t.Skip("set ROUTER_LIVE=1: this reads the whole machine")
	}
	first, err := gpuHolders()
	if err != nil {
		t.Skip(err)
	}
	second, err := gpuHolders()
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(held(first)-held(second)) > 1 {
		t.Fatalf("%.2f GiB then %.2f GiB: %v vs %v", held(first), held(second), first, second)
	}
	for _, h := range first {
		if h.Process == "pid"+strconv.Itoa(h.PID) {
			t.Fatalf("a process holding %.2f GiB is unnamed: %+v", h.GiB, h)
		}
	}
	t.Logf("%.2f GiB held: %v", held(first), first)
}
