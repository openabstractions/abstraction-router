package router

import (
	"strings"
	"testing"
)

// A Lemonade host with no /api/v1/health route still has its installed
// list: the two reads are independent, so the models read does not get
// discarded for a residency read that fails. Resident stays unset and Why
// names the missing read, rather than the whole host reading as down.
func TestLemonadeInstalledStandsWithoutHealth(t *testing.T) {
	lemonade := serve(t, map[string]string{
		"/api/v1/models": `{"data":[{"id":"Qwen3.6-35B-A3B-GGUF","checkpoint":"unsloth/Qwen3.6-35B-A3B-GGUF:Qwen3.6-35B-A3B-UD-Q4_K_XL.gguf","downloaded":true}]}`,
	})
	r := New(Lemonade(lemonade))
	r.Survey()
	hosts, _, _, _, _ := r.Residency(false)
	if len(hosts) != 1 {
		t.Fatalf("hosts %+v", hosts)
	}
	h := hosts[0]
	if !h.Up {
		t.Fatalf("a host whose installed list was read should read up: %+v", h)
	}
	if h.Installed != 2 {
		t.Fatalf("installed %d, want 2 (the id and its checkpoint)", h.Installed)
	}
	if h.Resident != nil {
		t.Fatalf("resident should be unknown, not read: %+v", h.Resident)
	}
	if !strings.HasPrefix(h.Why, "health:") {
		t.Fatalf("why %q, want a health: reason naming the missing residency read", h.Why)
	}

	fams, _ := r.Models(false)
	var found bool
	for _, f := range fams {
		for _, n := range f.Names {
			if n.Name == "Qwen3.6-35B-A3B-GGUF" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("the installed alias should still be catalogued: %+v", fams)
	}
}
