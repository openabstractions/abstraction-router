package router

import (
	wire "github.com/openabstractions/abstraction-router/go/abstraction/router"
	"reflect"
	"strings"
	"testing"
)

func TestFamilyTitlesPreserveRoutingInventory(t *testing.T) {
	r := New(LMStudio(hostWithModels(t, "smollm2-135m")))
	r.SetHeld([]HeldObject{{Store: "files", Names: []string{"wan2.2_ti2v_5B_fp16.safetensors"}}})
	r.Survey()
	before, _ := r.Models(false)
	authorized := []string{"lmstudio"}
	routeRequest := Request{Model: "smollm2-135m", Profile: ProfileChat, Hosts: &authorized}
	decisionBefore, _ := r.Route(routeRequest)
	request := Request{Profile: ProfileChat, Placement: PlacementLocal}
	offersBefore, _, err := r.Offer(request)
	if err != nil {
		t.Fatal(err)
	}
	r.SetFamilyTitleLookup(func(family string) string { return "  Title for " + family + "  " })
	r.Survey()
	decisionAfter, _ := r.Route(routeRequest)
	if !reflect.DeepEqual(decisionBefore, decisionAfter) {
		t.Fatalf("title changed routing: before %+v after %+v", decisionBefore, decisionAfter)
	}
	after, _ := r.Models(false)
	if len(after) != 2 {
		t.Fatalf("served and stored families: %+v", after)
	}
	for i := range after {
		if after[i].Title != "Title for "+after[i].Family {
			t.Fatalf("title missing: %+v", after[i])
		}
		after[i].Title = ""
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("title changed inventory: before %+v after %+v", before, after)
	}
	offersAfter, _, err := r.Offer(request)
	if err != nil {
		t.Fatal(err)
	}
	for i := range offersAfter {
		if offersAfter[i].Title != "Title for "+offersAfter[i].Family {
			t.Fatal("offer title missing")
		}
		offersAfter[i].Title = ""
	}
	if !reflect.DeepEqual(offersBefore, offersAfter) {
		t.Fatal("titles changed offers or selection inputs")
	}
	for _, invalid := range []string{"", "\n", "bad\x00name", "bad\u202ename", "\xff", strings.Repeat("x", 129), strings.Repeat("界", 43), "55983c5a75a1ab969824077b3bb3de414", "sha256:example"} {
		r.SetFamilyTitleLookup(func(string) string { return invalid })
		if got := r.familyTitle("family"); got != "" {
			t.Errorf("unsafe title %q retained", got)
		}
	}
	r.SetFamilyTitleLookup(nil)
	if r.familyTitle("family") != "" {
		t.Fatal("nil lookup")
	}
}

type titleWireReceiver struct {
	wire.Router
	title string
}

func (r titleWireReceiver) Models(bool) (wire.ModelsSnapshot, error) {
	return wire.ModelsSnapshot{Models: []wire.Family{{Family: "key", Title: r.title}}}, nil
}

func (r titleWireReceiver) Offer(wire.OfferRequest) (wire.OfferSnapshot, error) {
	return wire.OfferSnapshot{Outcome: wire.OfferOutcomeOffered, Offers: []wire.Offer{{Family: "key", Title: r.title, ContextSource: wire.ContextSourceDefault}}}, nil
}

func TestFamilyTitleWireByteBound(t *testing.T) {
	for _, title := range []string{strings.Repeat("x", 128), strings.Repeat("x", 129), strings.Repeat("界", 42) + "xx", strings.Repeat("界", 43)} {
		r := New()
		r.SetFamilyTitleLookup(func(string) string { return title })
		want := title
		if len(title) > 128 {
			want = ""
		}
		normalized := r.familyTitle("key")
		client := wire.NewRouterClient(&wire.RouterDispatcher{Handler: titleWireReceiver{title: normalized}})
		models, err := client.Models(false)
		if err != nil || len(models.Models) != 1 || models.Models[0].Title != want {
			t.Fatalf("Models %d-byte title: %+v %v", len(title), models, err)
		}
		offers, err := client.Offer(wire.OfferRequest{Placement: wire.PlacementLocal})
		if err != nil || len(offers.Offers) != 1 || offers.Offers[0].Title != want {
			t.Fatalf("Offer %d-byte title: %+v %v", len(title), offers, err)
		}
		if len(title) > 128 {
			raw := wire.NewRouterClient(&wire.RouterDispatcher{Handler: titleWireReceiver{title: title}})
			if _, err := raw.Models(false); err == nil {
				t.Fatal("raw oversized Models title encoded")
			}
			if _, err := raw.Offer(wire.OfferRequest{Placement: wire.PlacementLocal}); err == nil {
				t.Fatal("raw oversized Offer title encoded")
			}
		}
	}
}
