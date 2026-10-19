package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	identity "github.com/openabstractions/abstraction-identity"
	"github.com/openabstractions/abstraction-identity/listen"
	router "github.com/openabstractions/abstraction-router/go"
	"github.com/openabstractions/abstraction-router/go/client"
)

// ollamaFixture is an Ollama server listing installed names and holding
// resident ones in memory.
func ollamaFixture(t *testing.T, name string, installed, resident []string) *router.Host {
	t.Helper()
	list := func(names []string) string {
		var parts []string
		for _, n := range names {
			parts = append(parts, `{"name":"`+n+`"}`)
		}
		return `{"models":[` + strings.Join(parts, ",") + `]}`
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tags", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(list(installed))) })
	mux.HandleFunc("/api/ps", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(list(resident))) })
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	h := router.Ollama(s.URL)
	h.Name = name
	return h
}

// pickOverIPC serves r through the framed router service with its installed
// gate and returns a client for it.
func pickOverIPC(t *testing.T, r *router.Router) *client.Client {
	t.Helper()
	if err := identity.CanEver(router.Bound); err != nil {
		t.Skip(err)
	}
	endpoint := endpointFor(t)
	if err := listen.CanEver(endpoint, router.Bound); err != nil {
		t.Skip(err)
	}
	h, err := Listen(endpoint, r)
	if err != nil {
		t.Fatal(err)
	}
	if err := enableLocalGate(h); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.Serve(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	return client.New(endpoint)
}

// ROUTE-D3: a request naming an exact quant a server lists is served by that
// quant, loading it if needed. The resident Q2 of the same family never
// answers a request for the Q8.
func TestExactAliasIsNotAnsweredByAResidentSibling(t *testing.T) {
	r := router.New(ollamaFixture(t, "ollama", []string{"qwen3:8b-q8_0", "qwen3:8b-q2_K"}, []string{"qwen3:8b-q2_K"}))
	c := pickOverIPC(t, r)
	pick, err := c.Pick(client.PickRequest{Model: "qwen3:8b-q8_0", Fresh: true})
	if err != nil {
		t.Fatal(err)
	}
	d := pick.Decision
	if d.Verdict != router.WouldLoad || d.Host != "ollama" || d.Model != "qwen3:8b-q8_0" || d.Loads != 1 {
		t.Fatalf("the exact Q8 was answered by another file: %+v", d)
	}
	pick, err = c.Pick(client.PickRequest{Model: "qwen3:8b-q2_K"})
	if err != nil {
		t.Fatal(err)
	}
	if d := pick.Decision; d.Verdict != router.Resident || d.Model != "qwen3:8b-q2_K" || d.Loads != 0 {
		t.Fatalf("the resident exact Q2 was not served in place: %+v", d)
	}
}

// ROUTE-D3: an exact alias that cannot be served is refused with the reason,
// and the reason names the resident sibling that did not substitute for it.
func TestExactAliasThatCannotLoadIsRefusedWithAReason(t *testing.T) {
	resident := ollamaFixture(t, "ollama", []string{"qwen3:8b-q2_K"}, []string{"qwen3:8b-q2_K"})
	stopped := ollamaFixture(t, "stopped", []string{"qwen3:8b-q8_0"}, nil)
	stopped.Unavailable = func() string { return "stopped" }
	c := pickOverIPC(t, router.New(resident, stopped))
	for _, tc := range []struct{ asked, verdict, reason string }{
		{"qwen3:8b-q8_0", router.NoHost, "stopped cannot serve now (stopped)"},
		{"qwen3:8b-q4_K_M", router.NotOnDisk, "no host on this machine has it installed"},
	} {
		pick, err := c.Pick(client.PickRequest{Model: tc.asked, Fresh: true})
		if err != nil {
			t.Fatal(tc.asked, err)
		}
		d := pick.Decision
		if d.Verdict != tc.verdict || d.Host != "" || d.Model != "" || d.Loads != 0 {
			t.Fatalf("%s: an exact alias that cannot load was not refused: %+v", tc.asked, d)
		}
		if !strings.Contains(d.Why, tc.reason) || !strings.Contains(d.Why, `ollama holds "qwen3:8b-q2_K"`) {
			t.Fatalf("%s: the refusal does not give its reason and the sibling it kept out: %q", tc.asked, d.Why)
		}
	}
}

// ROUTE-D3: a request naming only the family is served by the resident
// sibling, and the decision names the alias that serves it.
func TestFamilyRequestIsServedByAResidentSiblingItNames(t *testing.T) {
	r := router.New(ollamaFixture(t, "ollama", []string{"qwen3:8b-q8_0", "qwen3:8b-q2_K"}, []string{"qwen3:8b-q2_K"}))
	c := pickOverIPC(t, r)
	pick, err := c.Pick(client.PickRequest{Model: "qwen3:8b", Fresh: true})
	if err != nil {
		t.Fatal(err)
	}
	d := pick.Decision
	if d.Verdict != router.Resident || d.Host != "ollama" || d.Model != "qwen3:8b-q2_K" || d.Loads != 0 {
		t.Fatalf("a family request was not served by the resident sibling: %+v", d)
	}
	if !strings.Contains(d.Why, `"qwen3:8b-q2_K" serves it`) {
		t.Fatalf("the decision does not name the alias that served the family: %q", d.Why)
	}
}

// ROUTE-D3: a request equal to one server's full listed alias is exact. On
// Lemonade's llama.cpp that is the file-derived name: the MTP build is
// served, or loaded, while the plain build of the family sits resident. A
// bare family name, with or without its publisher, stays a family request.
func TestFullListedAliasIsExact(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"data":[{"id":"Qwen3.6-35B-A3B-GGUF","downloaded":true},{"id":"Qwen3.6-35B-A3B-MTP-GGUF","downloaded":true}]}`))
	})
	mux.HandleFunc("/api/v1/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"all_models_loaded":[{"model_name":"Qwen3.6-35B-A3B-GGUF","loaded":true}]}`))
	})
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	c := pickOverIPC(t, router.New(router.Lemonade(s.URL)))
	for _, tc := range []struct{ asked, verdict, model string }{
		{"Qwen3.6-35B-A3B-MTP-GGUF", router.WouldLoad, "Qwen3.6-35B-A3B-MTP-GGUF"},
		{"qwen3.6-35b-a3b-mtp-gguf", router.WouldLoad, "Qwen3.6-35B-A3B-MTP-GGUF"},
		{"Qwen3.6-35B-A3B-GGUF", router.Resident, "Qwen3.6-35B-A3B-GGUF"},
		{"qwen3.6-35b-a3b", router.Resident, "Qwen3.6-35B-A3B-GGUF"},
		{"qwen/qwen3.6-35b-a3b", router.Resident, "Qwen3.6-35B-A3B-GGUF"},
	} {
		pick, err := c.Pick(client.PickRequest{Model: tc.asked, Fresh: true})
		if err != nil {
			t.Fatal(tc.asked, err)
		}
		if d := pick.Decision; d.Verdict != tc.verdict || d.Host != "lemonade" || d.Model != tc.model {
			t.Fatalf("%s: served %+v, want %s %s", tc.asked, d, tc.verdict, tc.model)
		}
	}
}
