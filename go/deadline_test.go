package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	identity "github.com/openabstractions/abstraction-identity"
	"github.com/openabstractions/abstraction-identity/listen"
)

// TestConcurrentFreshCallsShareOneSurvey (router P0-3): ten concurrent fresh
// Models calls cause one listing request per server.
func TestConcurrentFreshCallsShareOneSurvey(t *testing.T) {
	var listings atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			listings.Add(1)
			time.Sleep(300 * time.Millisecond)
		}
		w.Write([]byte(`{"data":[{"id":"anthropic/claude-sonnet-5"}]}`))
	}))
	defer upstream.Close()
	r := New(NewHosted("openrouter", upstream.URL+"/api/v1", WireOpenAICompatible, ""))
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if out := r.Answer(Request{Op: OpModels, Fresh: true}, Unidentified); out.Err() != nil || len(out.Models) != 1 {
				t.Errorf("fresh models: %+v", out)
			}
		}()
	}
	close(start)
	wg.Wait()
	if n := listings.Load(); n != 1 {
		t.Fatalf("ten concurrent fresh calls made %d listing requests, want 1", n)
	}
}

// TestDecisionCarriesNoEndpoint (router P0-1): a decision names its server and
// never an address, and no router source assigns one into a decision.
func TestDecisionCarriesNoEndpoint(t *testing.T) {
	base := serve(t, map[string]string{
		"/api/v1/models": `{"data":[{"id":"Qwen3.6-35B-A3B-GGUF","downloaded":true},{"id":"Gemma-4-E4B-GGUF","downloaded":true}]}`,
		"/api/v1/health": `{"all_models_loaded":[{"model_name":"Qwen3.6-35B-A3B-GGUF","loaded":true}]}`,
	})
	hosted := serve(t, map[string]string{
		"/api/v1/models": `{"data":[{"id":"anthropic/claude-sonnet-5"}]}`,
	})
	r := New(Lemonade(base), NewHosted("openrouter", hosted+"/api/v1", WireOpenAICompatible, ""))
	r.Survey()
	seen := map[string]bool{}
	for _, req := range []Request{
		{Model: "Qwen3.6-35B-A3B-GGUF"},
		{Model: "Gemma-4-E4B-GGUF"},
		{Model: "anthropic/claude-sonnet-5"},
		{Model: "Qwen3.6-35B-A3B-GGUF", Hosts: &[]string{"elsewhere"}},
	} {
		d, _ := r.Route(req)
		if d.Endpoint != "" {
			t.Fatalf("decision for %+v carries an endpoint: %+v", req, d)
		}
		seen[d.Verdict] = true
	}
	for _, v := range []string{Resident, WouldLoad, Hosted, Unauthorised} {
		if !seen[v] {
			t.Errorf("fixtures produced no %s decision: %v", v, seen)
		}
	}
	// A router.Decision literal with an Endpoint key, or an assignment into a
	// decision's Endpoint, would put an address back into a decision.
	assign := regexp.MustCompile(`(?s)(?:^|[^.\w])Decision\{[^}]*\bEndpoint:|\b(?:d|dec|decision)\.Endpoint\s*=[^=]`)
	for _, dir := range []string{".", "service"} {
		files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			if m := assign.Find(src); m != nil {
				t.Errorf("%s assigns an address into a decision: %q", f, m)
			}
		}
	}
}

// TestSurveyStartsNoOnDemandProvider (VISION.md:2184-2187): a survey that
// reaches a stopped on-demand native provider lists its declared models and
// opens no connection that would start it.
func TestSurveyStartsNoOnDemandProvider(t *testing.T) {
	endpoint := listen.Endpoint("router-ondemand-" + time.Now().Format("150405.000000000"))
	l, err := listen.Listen(endpoint)
	if err != nil {
		t.Skip(err)
	}
	defer l.Close()
	var accepted atomic.Int32
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			c.Close()
		}
	}()
	expect := listen.ServerExpectation{Principal: identity.User{Kind: "posix", UID: 1}, Program: "/provider"}
	model := "qwen/qwen3.6-35b-a3b"
	ready := false
	h, err := NewNative("provider", endpoint, expect, []string{model}, []string{ProfileChat}, WithSurveyReady(func() bool { return ready }))
	if err != nil {
		t.Fatal(err)
	}
	r := New(h)
	if out := r.Answer(Request{Op: OpModels, Fresh: true}, Unidentified); out.Err() != nil || len(out.Models) != 1 {
		t.Fatalf("declared models: %+v", out)
	}
	time.Sleep(100 * time.Millisecond)
	if n := accepted.Load(); n != 0 {
		t.Fatalf("survey connected to the stopped provider %d times", n)
	}
	decision, _ := r.Route(Request{Op: OpRoute, Model: model})
	if decision == nil || decision.Host != h.Name {
		t.Fatalf("idle declaration did not remain routable: %+v", decision)
	}
	ready = true
	// Windows may reject the deliberately mismatched endpoint identity before
	// the listener records Accept. Observe the existing description seam too.
	var surveys atomic.Int32
	original := describeResidents
	describeResidents = func(ctx context.Context, transport listen.FrameClient, chat string) ([]string, map[string]int64, error) {
		surveys.Add(1)
		return original(ctx, transport, chat)
	}
	t.Cleanup(func() { describeResidents = original })
	if err := r.SurveyContext(t.Context()); err != nil || surveys.Load() == 0 {
		t.Fatalf("ready provider was not surveyed: %v", err)
	}
}
