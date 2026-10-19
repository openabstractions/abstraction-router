package router

import (
	"context"
	"testing"

	"github.com/openabstractions/abstraction-identity/listen"
)

// A provider naming a loaded model it never declared states nothing for it
// (ROUTE-D14).
func TestNativeResidencyKeepsOnlyDeclaredModels(t *testing.T) {
	saved := describeResidents
	t.Cleanup(func() { describeResidents = saved })
	describeResidents = func(context.Context, listen.FrameClient, string) ([]string, map[string]int64, error) {
		return []string{"other"}, map[string]int64{"other": 4096}, nil
	}
	native, err := NewNative("modelhost", "unused", listen.ServerExpectation{}, []string{"qwen"}, []string{ProfileChat})
	if err != nil {
		t.Fatal(err)
	}
	r := New(native)
	if err := r.SurveyContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	families, _ := r.Models(false)
	for _, f := range families {
		for _, a := range f.Names {
			if a.Name == "other" {
				t.Fatalf("undeclared resident became an alias: %+v", a)
			}
			if a.Resident {
				t.Fatalf("alias %q reads resident", a.Name)
			}
		}
	}
	states, _, _, _, _ := r.Residency(false)
	for _, s := range states {
		if len(s.Resident) != 0 {
			t.Fatalf("host %s resident %v", s.Host, s.Resident)
		}
	}
}
