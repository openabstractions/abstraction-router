package service

import (
	"errors"
	"strings"
	"testing"

	router "github.com/openabstractions/abstraction-router/go"
	wire "github.com/openabstractions/abstraction-router/go/abstraction/router"
)

func TestEngineGuideBoundsCountUTF8Bytes(t *testing.T) {
	engine := wire.Engine{ID: strings.Repeat("é", 32), Name: "selected"}
	if _, err := wire.ToJSON(engine); err != nil {
		t.Fatalf("64 UTF-8 bytes refused: %v", err)
	}
	engine.ID += "x"
	if _, err := wire.ToJSON(engine); err == nil {
		t.Fatal("65 UTF-8 bytes accepted")
	}
	association := wire.ModelEngine{Profiles: []string{strings.Repeat("p", 256)}}
	if _, err := wire.ToJSON(association); err != nil {
		t.Fatal(err)
	}
	association.Profiles[0] += "x"
	if _, err := wire.ToJSON(association); err == nil {
		t.Fatal("profile element one byte over its existing bound accepted")
	}
}

func TestRouterReplyBoundsCutModelsAtTheFrame(t *testing.T) {
	objects := make([]router.ModelObject, 300)
	for i := range objects {
		objects[i] = router.ModelObject{Name: strings.Repeat("n", 4096), Cause: wire.EngineCauseUnknown}
	}
	calls := 0
	answer := answerFunc(func(router.Request) (router.Response, error) {
		calls++
		return router.Response{Models: []router.Family{{Family: "kept", Objects: objects}}}, nil
	})
	result, err := answer.Models(false)
	if err != nil || calls != 1 || len(result.Models) != 1 {
		t.Fatalf("over-frame Models: calls=%d models=%d err=%v", calls, len(result.Models), err)
	}
	kept := len(result.Models[0].Objects)
	if kept == 0 || kept >= len(objects) {
		t.Fatalf("over-frame Models kept %d of %d objects", kept, len(objects))
	}
	if len(objects[0].Name) != 4096 || len(objects) != 300 {
		t.Fatal("fitting wrote through to the provider's value")
	}
	if err := checkReply(result); err != nil {
		t.Fatalf("fitted reply refused: %v", err)
	}
}

func TestRouterReplyBoundsCutListOverflow(t *testing.T) {
	answer := answerFunc(func(router.Request) (router.Response, error) {
		return router.Response{Models: []router.Family{{Family: "kept", Objects: []router.ModelObject{{
			Cause: wire.EngineCauseUnknown, Engines: make([]router.ModelEngine, 65),
		}}}}}, nil
	})
	result, err := answer.Models(false)
	if err != nil || len(result.Models) != 1 || len(result.Models[0].Objects) != 1 || len(result.Models[0].Objects[0].Engines) != 64 {
		t.Fatalf("list overflow: result=%+v err=%v", result, err)
	}
}

// One engine whose display source exceeds its bound drops that engine and
// keeps the rest of the models and hosts listings.
func TestRouterReplyBoundsDropOneBadEngine(t *testing.T) {
	bad := strings.Repeat("s", 1024)
	answer := answerFunc(func(request router.Request) (router.Response, error) {
		return router.Response{
			Models: []router.Family{{Family: "kept", Objects: []router.ModelObject{{
				Store: "store", Name: "weights", Cause: wire.EngineCauseBound,
				Engines: []router.ModelEngine{{Host: "lemonade", Engine: "llama", Source: bad}, {Host: "ollama", Engine: "llama", Source: "binding"}},
			}}}, {Family: "second"}},
			Hosts: []router.HostState{{Host: "lemonade", Base: "http://127.0.0.1:1", Engines: []router.Engine{{ID: "llama", Name: "llama.cpp", Source: bad}, {ID: "sd", Name: "sd.cpp", Source: "binding"}}}, {Host: "ollama", Base: "http://127.0.0.1:2"}},
		}, nil
	})
	models, err := answer.Models(false)
	if err != nil || len(models.Models) != 2 {
		t.Fatalf("models: %+v %v", models, err)
	}
	engines := models.Models[0].Objects[0].Engines
	if len(engines) != 1 || engines[0].Host != "ollama" {
		t.Fatalf("model engines: %+v", engines)
	}
	hosts, err := answer.Hosts(false)
	if err != nil || len(hosts.Hosts) != 2 || len(hosts.Hosts[0].Engines) != 1 || hosts.Hosts[0].Engines[0].ID != "sd" {
		t.Fatalf("hosts: %+v %v", hosts, err)
	}
}

// A field outside every list cannot be dropped: the reply is unavailable and
// the reason names the field and why.
func TestRouterReplyBoundsNameTheFieldOutsideAList(t *testing.T) {
	answer := answerFunc(func(router.Request) (router.Response, error) {
		return router.Response{Cursor: strings.Repeat("c", 300)}, nil
	})
	_, err := answer.ObserveOffer(wire.OfferRequest{Placement: wire.PlacementAny}, "", 0)
	var refusal *wire.ServiceError
	if !errors.As(err, &refusal) || refusal.Code != CodeUnavailable || refusal.Message != "field:cursor:invalid_result" {
		t.Fatalf("root field: %v", err)
	}
}

func TestRouterReplyBoundsKeepSmallCompleteResults(t *testing.T) {
	want := []router.ModelObject{{Store: "store", Name: "first", Cause: wire.EngineCauseUnknown},
		{Store: "store", Name: "second", Cause: wire.EngineCauseUnknown}}
	answer := answerFunc(func(router.Request) (router.Response, error) {
		return router.Response{Models: []router.Family{{Family: "kept", Objects: want}}}, nil
	})
	result, err := answer.Models(false)
	if err != nil || len(result.Models) != 1 || len(result.Models[0].Objects) != 2 || result.Models[0].Objects[1].Name != "second" {
		t.Fatalf("complete result: %+v %v", result, err)
	}
}

// A registration binding ID is "provider-binding-v1:" and 64 hex digits, 84
// bytes. The engine source bound carries it with more than twice that room.
func TestEngineSourceCarriesARegistrationBindingID(t *testing.T) {
	binding := "provider-binding-v1:" + strings.Repeat("0123456789abcdef", 4)
	if len(binding) != 84 {
		t.Fatalf("fixture binding is %d bytes", len(binding))
	}
	if _, err := wire.ToJSON(wire.Engine{ID: "llama.cpp", Name: "llama.cpp", Source: binding}); err != nil {
		t.Fatalf("engine source %d bytes refused: %v", len(binding), err)
	}
	if _, err := wire.ToJSON(wire.ModelEngine{Host: "modelhost", Engine: "llama.cpp", Profiles: []string{}, Source: strings.Repeat(binding, 2)}); err != nil {
		t.Fatalf("model engine source %d bytes refused: %v", 2*len(binding), err)
	}
}
