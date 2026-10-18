package router

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// sameAliasHost is a model server serving "same-model" with its own context
// observation, holding it in memory when resident is true.
func sameAliasHost(name string, maximum, effective int64, resident bool) *Host {
	return &Host{Name: name, Base: "http://" + name + ".invalid", Chat: "/chat",
		installed: func(context.Context, *Host) ([]string, error) { return []string{"same-model"}, nil },
		resident: func(context.Context, *Host) ([]string, error) {
			if resident {
				return []string{"same-model"}, nil
			}
			return nil, nil
		},
		modelContexts: func(context.Context, *Host, []string) map[string]modelContext {
			return map[string]modelContext{"same-model": {maximum: maximum, effective: effective}}
		},
	}
}

func sameModelAliases(t *testing.T, r *Router) (string, []Alias) {
	t.Helper()
	families, _ := r.Models(false)
	for _, family := range families {
		var out []Alias
		for _, alias := range family.Names {
			if alias.Name == "same-model" {
				out = append(out, alias)
			}
		}
		if len(out) > 0 {
			return family.Family, out
		}
	}
	t.Fatal("same-model alias missing")
	return "", nil
}

// Two model servers serving the same alias are two entries of one family,
// each carrying its own server's context and residency.
func TestSameAliasOnTwoServersReportsEachServersOwnFacts(t *testing.T) {
	r := New(sameAliasHost("first", 40960, 16384, false), sameAliasHost("second", 32768, 8192, true))
	r.Survey()
	_, aliases := sameModelAliases(t, r)
	if len(aliases) != 2 {
		t.Fatalf("two servers serving one alias listed as %d entries: %+v", len(aliases), aliases)
	}
	want := map[string]Alias{
		"first":  {Host: "first", Name: "same-model", Resident: false, Servable: true, ContextLength: 40960, EffectiveContextLength: 16384},
		"second": {Host: "second", Name: "same-model", Resident: true, Servable: true, ContextLength: 32768, EffectiveContextLength: 8192},
	}
	for _, alias := range aliases {
		w, ok := want[alias.Host]
		if !ok {
			t.Fatalf("alias names an unknown server: %+v", alias)
		}
		if alias.Resident != w.Resident || alias.Servable != w.Servable || alias.ContextLength != w.ContextLength ||
			alias.EffectiveContextLength != w.EffectiveContextLength {
			t.Fatalf("server %s reports %+v, want %+v", alias.Host, alias, w)
		}
		delete(want, alias.Host)
	}
	if aliases[0].Host != "first" || aliases[1].Host != "second" {
		t.Fatalf("entries of one name follow the servers' preference order: %+v", aliases)
	}
}

// A request naming the shared alias is one choice: it reaches the family
// once, every server holding the name is a candidate, and the server that
// holds it in memory serves it.
func TestSameAliasOnTwoServersIsOneChoice(t *testing.T) {
	r := New(sameAliasHost("first", 40960, 16384, false), sameAliasHost("second", 32768, 8192, true))
	r.Survey()
	family, _ := sameModelAliases(t, r)
	d, _ := r.Route(Request{Model: "same-model"})
	if d.Family != family || d.Verdict != Resident || d.Host != "second" || d.Model != "same-model" || d.Loads != 0 {
		t.Fatalf("the server holding the alias did not serve it: %+v", d)
	}
	if !slices.Equal(d.InstalledOn, []string{"first", "second"}) {
		t.Fatalf("installed on %v, want both servers", d.InstalledOn)
	}
	only := []string{"first"}
	d, _ = r.Route(Request{Model: "same-model", Hosts: &only})
	if d.Verdict != WouldLoad || d.Host != "first" || d.Loads != 1 {
		t.Fatalf("a server that does not hold the alias was reported as holding it: %+v", d)
	}
}

// The same alias held by two servers is the same model loaded twice.
func TestSameAliasHeldByTwoServersIsDoubled(t *testing.T) {
	r := New(sameAliasHost("first", 40960, 16384, true), sameAliasHost("second", 32768, 8192, true))
	r.Survey()
	_, _, _, doubled, _ := r.Residency(false)
	if len(doubled) != 1 || !strings.Contains(doubled[0], "[first second]") {
		t.Fatalf("one alias loaded on two servers was not named: %v", doubled)
	}
}
