package router

import (
	"context"
	"testing"

	identity "github.com/openabstractions/abstraction-identity"
	"github.com/openabstractions/abstraction-identity/listen"
)

// ROUTE-D8: an alias whose server reports no window is bounded by the
// context the store's descriptor says the weights were trained for
// (descriptor@1 context_length), as its maximum. SmolLM2-135M was trained at
// 8,192 tokens, so the chat default of 32,768 would overstate it; a family
// whose descriptor names no context keeps the default.
func TestOfferBoundsAnAliasByTheTrainedContext(t *testing.T) {
	expect := listen.ServerExpectation{Principal: identity.User{Kind: "posix", UID: 1}, Program: "/modelhostd"}
	h, err := NewNative("modelhost", "modelhost", expect, []string{"smollm2-135m", "qwen2.5-0.5b"}, []string{ProfileChat})
	if err != nil {
		t.Fatal(err)
	}
	r := New(h)
	r.SetHeld([]HeldObject{
		{Store: "huggingface", Names: []string{"unsloth/SmolLM2-135M-Instruct-GGUF:SmolLM2-135M-Instruct-Q4_K_M.gguf"},
			Descriptor: map[string]string{"family": "smollm2-135m", "format": "gguf", "context_length": "8192"}},
		{Store: "lmstudio", Names: []string{"bartowski/SmolLM2-135M-Instruct-GGUF/SmolLM2-135M-Instruct-Q8_0.gguf"},
			Descriptor: map[string]string{"family": "smollm2-135m", "format": "gguf", "context_length": "16384"}},
		{Store: "huggingface", Names: []string{"bartowski/Qwen2.5-0.5B-Instruct-GGUF:Qwen2.5-0.5B-Instruct-Q4_K_M.gguf"},
			Descriptor: map[string]string{"family": "qwen2.5-0.5b", "format": "gguf"}},
	})
	if err := r.SurveyContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	offers, _, err := r.Offer(Request{Op: OpOffer, Profile: ProfileChat, Placement: PlacementLocal, Reserve: 4096})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Offer{}
	for _, o := range offers {
		got[o.Family] = o
	}
	if o := got["smollm2-135m"]; o.Context != 8192 || o.ContextSource != ContextMaximum || o.Input != 4096 {
		t.Fatalf("smollm2-135m offer %+v, want the smaller trained context 8192 as maximum", o)
	}
	if o := got["qwen2.5-0.5b"]; o.Context != 32768 || o.ContextSource != ContextDefault {
		t.Fatalf("qwen2.5-0.5b offer %+v, want the chat default 32768", o)
	}
}

// Decision D (2026-11-08): a native provider reports the context one
// conversation gets, which outranks the trained maximum. The model host runs
// Qwen3.6 35B with --ctx-size 65536 split over --parallel 2, so each
// conversation gets 32768 and the offer says 32768, never the 65536 total
// or the trained 262144.
func TestOfferReportsTheContextOneConversationGets(t *testing.T) {
	expect := listen.ServerExpectation{Principal: identity.User{Kind: "posix", UID: 1}, Program: "/modelhostd"}
	h, err := NewNative("modelhost", "modelhost", expect, []string{"qwen3.6-35b-a3b", "smollm2-135m"}, []string{ProfileChat})
	if err != nil {
		t.Fatal(err)
	}
	var asked []string
	h.ReportContexts(func(_ context.Context, names []string) map[string]int64 {
		asked = append(asked, names...)
		return map[string]int64{"qwen3.6-35b-a3b": 65536 / 2, "not-installed": 999}
	})
	r := New(h)
	r.SetHeld([]HeldObject{{Store: "huggingface", Names: []string{"unsloth/Qwen3.6-35B-A3B-MTP-GGUF:Qwen3.6-35B-A3B-UD-Q4_K_XL.gguf"},
		Descriptor: map[string]string{"family": "qwen3.6-35b-a3b", "format": "gguf", "context_length": "262144"}}})
	if err := r.SurveyContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 2 {
		t.Fatalf("context read asked %v, want the installed names", asked)
	}
	offers, _, err := r.Offer(Request{Op: OpOffer, Profile: ProfileChat, Placement: PlacementLocal, Reserve: 4096})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Offer{}
	for _, o := range offers {
		got[o.Family] = o
	}
	if o := got["qwen3.6-35b-a3b"]; o.Context != 32768 || o.ContextSource != ContextEffective || o.Input != 32768-4096 {
		t.Fatalf("qwen offer %+v, want each conversation's 32768 as effective", o)
	}
	// A name the provider reports nothing for keeps the default rule.
	if o := got["smollm2-135m"]; o.Context != 32768 || o.ContextSource != ContextDefault {
		t.Fatalf("smollm2 offer %+v", o)
	}
}

// The context a client is told is the one the provider loaded the model
// with. The model host loaded Qwen3.6 35B at 65536 per conversation beside a
// picture engine, though its plan for an empty card is 262144: the offer
// says 65536, and a provider that states no loaded context keeps the plan.
func TestOfferReportsTheLoadedContext(t *testing.T) {
	saved := describeResidents
	t.Cleanup(func() { describeResidents = saved })
	loaded := map[string]int64{"qwen3.6-35b-a3b": 65536}
	describeResidents = func(context.Context, listen.FrameClient, string) ([]string, map[string]int64, error) {
		return []string{"qwen3.6-35b-a3b"}, loaded, nil
	}
	expect := listen.ServerExpectation{Principal: identity.User{Kind: "posix", UID: 1}, Program: "/modelhostd"}
	h, err := NewNative("modelhost", "modelhost", expect, []string{"qwen3.6-35b-a3b"}, []string{ProfileChat})
	if err != nil {
		t.Fatal(err)
	}
	h.ReportContexts(func(context.Context, []string) map[string]int64 {
		return map[string]int64{"qwen3.6-35b-a3b": 262144}
	})
	r := New(h)
	offer := func() Offer {
		t.Helper()
		if err := r.SurveyContext(context.Background()); err != nil {
			t.Fatal(err)
		}
		offers, _, err := r.Offer(Request{Op: OpOffer, Profile: ProfileChat, Placement: PlacementLocal, Reserve: 4096})
		if err != nil || len(offers) != 1 {
			t.Fatalf("offers %+v, %v", offers, err)
		}
		return offers[0]
	}
	if o := offer(); o.Context != 65536 || o.ContextSource != ContextEffective {
		t.Fatalf("offer %+v, want the loaded 65536", o)
	}
	loaded = nil
	if o := offer(); o.Context != 262144 {
		t.Fatalf("offer %+v, want the plan once no loaded context is stated", o)
	}
	// The description's capabilities carry the loaded context beside the
	// resident name, and only within the endpoint's 32 entries.
	caps := ResidentContextCapabilities([]string{"a", "b"}, map[string]int64{"b": 32768})
	if got := ParseResidentContextCapabilities(caps); len(got) != 1 || got["b"] != 32768 {
		t.Fatalf("parsed %v from %v", got, caps)
	}
	many := make([]string, 20)
	contexts := map[string]int64{}
	for i := range many {
		many[i] = string(rune('a' + i))
		contexts[many[i]] = 4096
	}
	if caps := ResidentContextCapabilities(many, contexts); len(caps) > 32 {
		t.Fatalf("%d capabilities", len(caps))
	}
}
