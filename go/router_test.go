package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	identity "github.com/openabstractions/abstraction-identity"
	"github.com/openabstractions/abstraction-identity/listen"
	modelid "github.com/openabstractions/abstraction-model/go/identity"
)

func serve(t *testing.T, routes map[string]string) string {
	t.Helper()
	mux := http.NewServeMux()
	for path, body := range routes {
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(body)) })
	}
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s.URL
}

func machine(t *testing.T) *Router {
	lemonade := serve(t, map[string]string{
		"/api/v1/models": `{"data":[{"id":"Qwen3.6-35B-A3B-GGUF","checkpoint":"unsloth/Qwen3.6-35B-A3B-GGUF:Qwen3.6-35B-A3B-UD-Q4_K_XL.gguf","downloaded":true}]}`,
		"/api/v1/health": `{"all_models_loaded":[{"model_name":"Qwen3.6-35B-A3B-GGUF","loaded":true}]}`,
	})
	lmstudio := serve(t, map[string]string{
		"/api/v0/models": `{"data":[{"id":"qwen/qwen3.6-35b-a3b","type":"llm","state":"not-loaded"},{"id":"google/gemma-4-26b-it","type":"llm","state":"not-loaded"}]}`,
	})
	ollama := serve(t, map[string]string{
		"/api/tags": `{"models":[{"name":"qwen2.5:0.5b"}]}`,
		"/api/ps":   `{"models":[]}`,
	})
	comfy := serve(t, map[string]string{
		"/models":                  `["diffusion_models"]`,
		"/models/diffusion_models": `["wan2.2_ti2v_5B_fp16.safetensors"]`,
	})
	r := New(Lemonade(lemonade), LMStudio(lmstudio), Ollama(ollama), ComfyUI(comfy))
	r.Survey()
	return r
}

func TestOneFamilyUnderEveryName(t *testing.T) {
	fams, _ := machine(t).Models(false)
	want := []string{"Qwen3.6-35B-A3B-GGUF",
		"unsloth/Qwen3.6-35B-A3B-GGUF:Qwen3.6-35B-A3B-UD-Q4_K_XL.gguf",
		"qwen/qwen3.6-35b-a3b"}
	for _, f := range fams {
		var got []string
		for _, a := range f.Names {
			got = append(got, a.Name)
		}
		if len(got) == len(want) && has(got, want[0]) && has(got, want[1]) && has(got, want[2]) {
			return
		}
	}
	t.Fatalf("three host names for one model did not collapse to one family: %+v", fams)
}

func TestRouteToTheHostThatAlreadyHoldsIt(t *testing.T) {
	d, _ := machine(t).Route(Request{Model: "qwen/qwen3.6-35b-a3b"})
	if d.Verdict != Resident || d.Host != "lemonade" || d.Loads != 0 {
		t.Fatalf("asked for LM Studio's name for a model Lemonade holds, got %+v", d)
	}
}

func TestWouldLoadNamesTheHostAndLoadsNothing(t *testing.T) {
	d, _ := machine(t).Route(Request{Model: "gemma-4-26b-it"})
	if d.Verdict != WouldLoad || d.Host != "lmstudio" || d.Loads != 1 {
		t.Fatalf("got %+v", d)
	}
}

func TestGraphWeightsAreRefusedRatherThanRouted(t *testing.T) {
	d, _ := machine(t).Route(Request{Model: modelid.Family("wan2.2_ti2v_5B_fp16.safetensors")})
	if d.Verdict != Unservable {
		t.Fatalf("a diffusion model was routed to an OpenAI endpoint: %+v", d)
	}
}

func TestNothingHasIt(t *testing.T) {
	d, _ := machine(t).Route(Request{Model: "llama-3-70b"})
	if d.Verdict != NotOnDisk {
		t.Fatalf("got %+v", d)
	}
}

func TestDoubledIsNamedWhileItHappens(t *testing.T) {
	both := serve(t, map[string]string{
		"/api/v0/models": `{"data":[{"id":"qwen/qwen3.6-35b-a3b","type":"llm","state":"loaded"}]}`,
	})
	r := machine(t)
	r.hosts[1] = LMStudio(both)
	r.Survey()
	_, _, _, doubled, _ := r.Residency(false)
	if len(doubled) != 1 || !strings.Contains(doubled[0], "lemonade") {
		t.Fatalf("one model loaded on two hosts was not named: %v", doubled)
	}
}

type unbindable struct {
	*strings.Reader
	out strings.Builder
}

func (u *unbindable) Write(p []byte) (int, error) { return u.out.Write(p) }
func (u *unbindable) Close() error                { return nil }
func (u *unbindable) Bind() (*identity.Binding, error) {
	return nil, identity.ErrNoBinding
}

func TestACallerTheKernelCannotIdentifyIsRefusedBeforeTheFrameIsRead(t *testing.T) {
	c := &unbindable{Reader: strings.NewReader(`{"op":"route","model":"qwen3.6-35b-a3b"} and this is not JSON` + "\n")}
	(&Service{r: machine(t)}).serve(c)
	got := c.out.String()
	if !strings.Contains(got, "refused:") || !strings.Contains(got, identity.ErrNoBinding.Error()) {
		t.Fatalf("an unidentifiable caller was not refused: %s", got)
	}
	if strings.Contains(got, "not a request") {
		t.Fatal("the frame was parsed before the caller was known")
	}
}

func TestTheWindowObservesAndCannotDecide(t *testing.T) {
	r := machine(t)
	if out := r.Answer(Request{Op: OpModels}, Unidentified); out.Error != "" || len(out.Models) == 0 {
		t.Fatalf("the read-only window could not observe: %+v", out)
	}
	out := r.Answer(Request{Op: OpRoute, Model: "qwen3.6-35b-a3b"}, Unidentified)
	if !strings.HasPrefix(out.Error, "refused:") || out.Decision != nil {
		t.Fatalf("an unidentified caller got a routing decision: %+v", out)
	}
	if len(r.Asked()) != 0 {
		t.Fatal("a refused caller was written into the audit")
	}
}

func TestTheAuditNamesTheProgramThatAsked(t *testing.T) {
	r := machine(t)
	r.Answer(Request{Op: OpRoute, Model: "qwen3.6-35b-a3b"},
		listen.Seen{Bound: true, Path: `C:\comfy\python.exe`, User: "reinis"})
	asked := r.Asked()
	if len(asked) != 1 || asked[0].Caller != `C:\comfy\python.exe` || asked[0].Host != "lemonade" {
		t.Fatalf("the routing decision was not attributed: %+v", asked)
	}
}

func TestUnknownOpSaysWhatItAnswers(t *testing.T) {
	out := machine(t).Answer(Request{Op: "chat"}, listen.Seen{Bound: true})
	if !strings.Contains(out.Error, OpModels) || !strings.Contains(out.Error, OpRoute) {
		t.Fatalf("got %q", out.Error)
	}
}

func TestADownHostIsNamedNotHidden(t *testing.T) {
	r := New(Lemonade("http://127.0.0.1:1"))
	r.Survey()
	hosts, _, _, _, _ := r.Residency(false)
	if len(hosts) != 1 || hosts[0].Up || hosts[0].Why == "" {
		t.Fatalf("an unreachable host did not say so: %+v", hosts)
	}
	if _, err := (&Client{Endpoint: "no-such-endpoint"}).Ask(Request{Op: OpModels}); err == nil {
		t.Fatal("a client reached a service that is not there")
	} else if !strings.Contains(err.Error(), "no service at") {
		t.Fatalf("got %q", err)
	}
}
