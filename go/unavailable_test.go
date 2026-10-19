package router

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/openabstractions/abstraction-identity/listen"
)

// A host whose Unavailable reads a reason lists its models as not servable
// with that reason and takes no route; another host serving the same name
// takes the request. Once the reason clears, the host serves again (R34
// live test D1: Lemonade without Setup was listed servable and took a picture
// request it could never start).
func TestAnUnavailableHostListsNotServableAndTakesNoRoute(t *testing.T) {
	saved := describeResidents
	t.Cleanup(func() { describeResidents = saved })
	describeResidents = func(context.Context, listen.FrameClient, string) ([]string, map[string]int64, error) {
		return nil, nil, nil
	}
	stopped, err := NewNative("a-stopped", "unused-a", listen.ServerExpectation{}, []string{"picture"}, []string{ProfileImage})
	if err != nil {
		t.Fatal(err)
	}
	var reason atomic.Value
	reason.Store("start_failed:a-stopped")
	stopped.Unavailable = func() string { return reason.Load().(string) }
	other, err := NewNative("b-other", "unused-b", listen.ServerExpectation{}, []string{"picture"}, []string{ProfileImage})
	if err != nil {
		t.Fatal(err)
	}
	r := New(stopped, other)
	if err := r.SurveyContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	families, _ := r.Models(false)
	servable := map[string]bool{}
	for _, f := range families {
		for _, a := range f.Names {
			servable[a.Host] = a.Servable
		}
	}
	if len(servable) != 2 || servable["a-stopped"] || !servable["b-other"] {
		t.Fatalf("aliases by host %v", servable)
	}
	states, _, _, _, _ := r.Residency(false)
	for _, s := range states {
		if s.Host == "a-stopped" && (s.Servable || s.Up || s.Why != "start_failed:a-stopped" || s.AvailabilityCause != AvailabilityConfiguration) {
			t.Fatalf("stopped host state %+v", s)
		}
	}
	if d, _ := r.Route(Request{Model: "picture", Profile: ProfileImage}); d.Host != "b-other" {
		t.Fatalf("routed %+v", d)
	}
	// With the other host left out, nothing serves it and the decision
	// still names the stopped host as having it.
	only := []string{"a-stopped"}
	if d, _ := r.Route(Request{Model: "picture", Profile: ProfileImage, Hosts: &only}); d.Host != "" || d.Verdict == Resident || d.Verdict == WouldLoad || !has(d.InstalledOn, "a-stopped") {
		t.Fatalf("routed onto the stopped host %+v", d)
	}
	reason.Store("")
	if d, _ := r.Route(Request{Model: "picture", Profile: ProfileImage, Hosts: &only}); d.Host != "a-stopped" {
		t.Fatalf("a host whose reason cleared takes no route: %+v", d)
	}
}
