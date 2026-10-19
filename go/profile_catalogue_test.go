package router

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestModalityCatalogueCurrentDeclarations(t *testing.T) {
	r := New()
	page := r.Modalities("", 64, "subject")
	want := slices.Clone(SeedProfiles)
	slices.Sort(want)
	if page.Outcome != "page" || !page.Complete || !slices.Equal(page.Modalities, want) {
		t.Fatalf("empty runtime: %+v", page)
	}
	r.SetHosts(&Host{Profiles: []string{"org/custom@1", "chat", "org/custom@1", "bad", "org/custom@0", strings.Repeat("a", 257)}})
	page = r.Modalities("", 64, "subject")
	want = append(want, "org/custom@1")
	slices.Sort(want)
	if !slices.Equal(page.Modalities, want) {
		t.Fatalf("declarations: %+v", page)
	}
	r.SetHosts()
	if got := r.Modalities("", 64, "subject"); slices.Contains(got.Modalities, "org/custom@1") {
		t.Fatalf("removed declaration: %+v", got)
	}
}

func TestModalityIDRegistrationGrammar(t *testing.T) {
	for _, id := range []string{"chat", "video", "9org/1custom.name-thing@999999", strings.Repeat("a", 63) + "/" + strings.Repeat("b", 63) + "@123456"} {
		if !ValidModality(id) {
			t.Fatalf("valid modality rejected %q", id)
		}
	}
	for _, id := range []string{"", "Chat", "unknown", "owner/name@0", "owner/name@01", "owner/name@1000000", "owner.name/name@1", "owner/_name@1", strings.Repeat("a", 64) + "/name@1"} {
		if ValidModality(id) {
			t.Fatalf("invalid modality accepted %q", id)
		}
	}
}

func TestModalityCatalogueStablePagesAndGaps(t *testing.T) {
	var modalities []string
	for i := 0; i < 140; i++ {
		modalities = append(modalities, fmt.Sprintf("org/custom-%03d@1", i))
	}
	r := New(&Host{Profiles: modalities})
	first := r.Modalities("", 64, "one")
	if len(first.Modalities) != 64 || first.Complete || len(first.Next) > 256 {
		t.Fatalf("first %+v", first)
	}
	var all []string
	page := first
	for {
		all = append(all, page.Modalities...)
		if page.Complete {
			break
		}
		page = r.Modalities(page.Next, 64, "one")
		if page.Outcome != "page" {
			t.Fatalf("continuation %+v", page)
		}
	}
	if len(all) != len(modalities)+len(SeedProfiles) || !slices.IsSorted(all) || len(slices.Compact(slices.Clone(all))) != len(all) {
		t.Fatalf("catalogue %v", all)
	}
	for _, got := range []ModalityPage{r.Modalities(first.Next, 64, "two"), New(&Host{Profiles: modalities}).Modalities(first.Next, 64, "one")} {
		if got.Outcome != "gap" || len(got.Modalities) != 0 || got.Next != "" || got.Complete {
			t.Fatalf("foreign: %+v", got)
		}
	}
	r.SetHosts()
	if got := r.Modalities(first.Next, 64, "one"); got.Outcome != "gap" || len(got.Modalities) != 0 {
		t.Fatalf("changed: %+v", got)
	}
	for _, args := range []struct {
		cursor string
		limit  int64
	}{{"", 0}, {"", 65}, {strings.Repeat("x", 257), 64}, {string([]byte{255}), 64}} {
		if got := r.Modalities(args.cursor, args.limit, "one"); got.Outcome != "invalid" || len(got.Modalities) != 0 {
			t.Fatalf("invalid: %+v", got)
		}
	}
}
