package router

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openabstractions/abstraction-identity/listen"
)

// counted is fitted to every http.Client in this process — the router's own
// host reads and the caller's requests both — so "nothing was sent" is a
// property of the process rather than of one client somebody remembered to
// watch.
type counted struct {
	mu   sync.Mutex
	sent []string
}

func (c *counted) RoundTrip(req *http.Request) (*http.Response, error) {
	c.mu.Lock()
	c.sent = append(c.sent, req.Method+" "+req.URL.String())
	c.mu.Unlock()
	return http.DefaultTransport.RoundTrip(req)
}

func (c *counted) since(n int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.sent[n:]...)
}

func (c *counted) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.sent)
}

// chats is the subset that asks a host to answer rather than to describe
// itself. A survey reads; only these spend a machine.
func (c *counted) chats() []string {
	var out []string
	for _, s := range c.since(0) {
		if strings.HasPrefix(s, "POST ") {
			out = append(out, s)
		}
	}
	return out
}

func watchEveryRequest(t *testing.T) *counted {
	t.Helper()
	tr := &counted{}
	was := client.Transport
	client.Transport = tr
	t.Cleanup(func() { client.Transport = was })
	return tr
}

type completion struct {
	Host  string
	Model string
	Text  string
}

// caller is the half the router does not do. It asks for a verdict, sends to
// the endpoint that verdict named and to no other, and when a host will not
// serve it asks again without that host. It stops when the router names no
// endpoint, which is the only way a refusal can be enforced: the thing that
// would send the request is the thing that reads the refusal.
type caller struct {
	ask  func(Request) (Response, error)
	http *http.Client
}

func (cl *caller) serve(req Request, authorised []string) (Decision, completion, error) {
	allow := append([]string(nil), authorised...)
	for {
		req.Op, req.Hosts = OpRoute, &allow
		out, err := cl.ask(req)
		if err != nil {
			return Decision{}, completion{}, err
		}
		d := *out.Decision
		if d.Endpoint == "" {
			return d, completion{}, nil
		}
		r, err := cl.chat(d)
		if err == nil {
			return d, r, nil
		}
		allow = without(allow, d.Host)
	}
}

// The deadline is a load budget, not a wait: a host asked for a model it does
// not hold answers in milliseconds, and one that has to read weights off disk
// takes as long as the weights are large.
func (cl *caller) chat(d Decision) (completion, error) {
	body, err := json.Marshal(map[string]any{"model": d.Model, "max_tokens": 8, "stream": false,
		"messages": []map[string]string{{"role": "user", "content": "hi"}}})
	if err != nil {
		return completion{}, err
	}
	req, err := http.NewRequest("POST", d.Endpoint, bytes.NewReader(body))
	if err != nil {
		return completion{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := cl.http.Do(req)
	if err != nil {
		return completion{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return completion{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return completion{}, fmt.Errorf("%s said %s: %.200s", d.Host, resp.Status, raw)
	}
	var out struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return completion{}, err
	}
	if len(out.Choices) == 0 {
		return completion{}, fmt.Errorf("%s answered with no completion: %.200s", d.Host, raw)
	}
	return completion{Host: d.Host, Model: out.Model, Text: out.Choices[0].Message.Content}, nil
}

func without(xs []string, x string) []string {
	out := []string{}
	for _, s := range xs {
		if s != x {
			out = append(out, s)
		}
	}
	return out
}

// present splits a survey into the hosts that could serve a request and a
// sentence about every host that could not. A run with nothing to talk to says
// which hosts were missing and skips; it never goes green on an empty machine.
func present(hs []HostState) (up, absent []string) {
	for _, h := range hs {
		switch {
		case h.Up && h.Servable && h.Installed > 0:
			up = append(up, h.Host)
		case !h.Up:
			absent = append(absent, h.Host+" at "+h.Base+" is not answering ("+h.Why+")")
		case !h.Servable:
			absent = append(absent, h.Host+" at "+h.Base+" serves no OpenAI-compatible endpoint")
		default:
			absent = append(absent, h.Host+" at "+h.Base+" is up and has no model installed")
		}
	}
	return up, absent
}

// guard is the whole of this file's answer to a machine with nothing running:
// every missing host is named and the run stops. It is a function so that the
// skip itself can be proven, rather than the predicate underneath it.
func guard(t *testing.T, hosts []HostState) []string {
	t.Helper()
	up, absent := present(hosts)
	for _, a := range absent {
		t.Log("ABSENT  " + a)
	}
	if len(up) == 0 {
		t.Skip("no model host is running, so this proves nothing: " + strings.Join(absent, "; "))
	}
	return up
}

// TestOneRequestRoutedThroughThreeOutcomes takes one model request through a
// refusal, a fallback and a reuse against the hosts running on this machine.
// The refusal is asked for first, while nothing is loaded, so that "nothing was
// sent" is measured before anything could have been.
func TestOneRequestRoutedThroughThreeOutcomes(t *testing.T) {
	if os.Getenv("ROUTER_LIVE") == "" {
		t.Skip("set ROUTER_LIVE=1: this sends a real request to a model host on this machine")
	}
	t.Logf("STATE: a laptop, %d logical CPUs, %s", runtime.NumCPU(), powerState())
	tr := watchEveryRequest(t)

	r := New(Installed()...)
	s, err := Start(DefaultEndpoint()+"-route58", r)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r.Survey()

	c := &Client{Endpoint: DefaultEndpoint() + "-route58"}
	cl := &caller{ask: c.Ask, http: &http.Client{Transport: tr, Timeout: 5 * time.Minute}}

	res, err := c.Ask(Request{Op: OpResidency})
	if err != nil {
		t.Fatal(err)
	}
	up := guard(t, res.Hosts)
	t.Logf("PRESENT %v", up)

	want := os.Getenv("ROUTER_LIVE_MODEL")
	if want == "" {
		want = firstResident(res.Hosts)
	}
	if want == "" {
		t.Skip("no host holds anything and this test will not pick weights to load on your behalf: set ROUTER_LIVE_MODEL")
	}

	first, err := c.Ask(Request{Op: OpRoute, Model: want})
	if err != nil {
		t.Fatal(err)
	}
	holder := first.Decision.Host
	if holder == "" {
		t.Skipf("no host on this machine can serve %q: %s", want, first.Decision.Why)
	}
	others := without(up, holder)
	if len(others) == 0 {
		t.Skipf("only %s can serve %q here, and a fallback needs a second servable host", holder, want)
	}
	t.Logf("WANTED  %q -> family %q, servable by %s; the caller prefers %v", want, first.Decision.Family, holder, others)

	before := tr.count()
	d, got, err := cl.serve(Request{Model: want}, others)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("REFUSED authorised %v -> %s: %s", others, d.Verdict, d.Why)
	if d.Verdict != Unauthorised {
		t.Fatalf("a host the request did not authorise was not refused: %+v", d)
	}
	if d.Host != "" || d.Endpoint != "" || got.Host != "" {
		t.Fatalf("a refusal named somewhere to send: %+v", d)
	}
	if !has(d.Withheld, holder) {
		t.Fatalf("the refusal did not name the host it withheld: %+v", d)
	}
	if sent := tr.since(before); len(sent) != 0 {
		t.Fatalf("the refusal sent %d requests: %v", len(sent), sent)
	}
	t.Logf("        %d requests left this process while it was refused, from a snapshot %d ms old",
		len(tr.since(before)), first.AgeMS)

	allow := append(append([]string(nil), others...), holder)
	before = tr.count()
	d, got, err = cl.serve(Request{Model: want}, allow)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("FALLBACK authorised %v -> %s on %s (%s), loads %d", allow, d.Verdict, d.Host, d.Endpoint, d.Loads)
	t.Logf("        %s answered as %q: %.60q", got.Host, got.Model, got.Text)
	t.Logf("        sent: %v", tr.since(before))
	if d.Host == allow[0] {
		t.Fatalf("this proves no fallback: the caller's first choice served it (%+v)", d)
	}
	if got.Host != d.Host || got.Model == "" {
		t.Fatalf("the answer does not say which host served it: %+v from %+v", got, d)
	}

	before = tr.count()
	d, got, err = cl.serve(Request{Model: want, Fresh: true}, allow)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("RESIDENT authorised %v -> %s on %s (%s), loads %d", allow, d.Verdict, d.Host, d.Endpoint, d.Loads)
	t.Logf("        %s answered as %q: %.60q", got.Host, got.Model, got.Text)
	if d.Verdict != Resident || d.Loads != 0 {
		t.Fatalf("the host that just served it was not reported as holding it: %+v", d)
	}
	if got.Host != holder {
		t.Fatalf("a second copy was routed to %s while %s held it", got.Host, holder)
	}

	for _, a := range r.Asked() {
		t.Logf("AUDIT   %s %s asked %q -> %s %s", a.At.Format(time.TimeOnly),
			filepath.Base(a.Caller), a.Model, a.Verdict, a.Host)
	}
	t.Logf("SENT    %d requests to hosts, of which %d asked one to answer: %v",
		tr.count(), len(tr.chats()), tr.chats())
}

// TestTheCallerMovesOnWhenTheNamedHostWillNotServe is the branch the live run
// cannot produce without stopping a host somebody is using: a host that holds
// the model, is named for it, and then refuses the request.
func TestTheCallerMovesOnWhenTheNamedHostWillNotServe(t *testing.T) {
	var refusals, answers int
	holds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/api/v0/models":
			io.WriteString(w, `{"data":[{"id":"qwen2.5-0.5b","type":"llm","state":"loaded"}]}`)
		case "/v1/chat/completions":
			refusals++
			http.Error(w, `{"error":"model runner exited"}`, http.StatusServiceUnavailable)
		default:
			http.NotFound(w, req)
		}
	}))
	defer holds.Close()
	serves := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/api/tags":
			io.WriteString(w, `{"models":[{"name":"qwen2.5:0.5b"}]}`)
		case "/api/ps":
			io.WriteString(w, `{"models":[]}`)
		case "/v1/chat/completions":
			answers++
			io.WriteString(w, `{"model":"qwen2.5:0.5b","choices":[{"message":{"content":"hi"}}]}`)
		default:
			http.NotFound(w, req)
		}
	}))
	defer serves.Close()

	r := New(LMStudio(holds.URL), Ollama(serves.URL))
	r.Survey()
	cl := &caller{http: holds.Client(), ask: func(req Request) (Response, error) {
		out := r.Answer(req, listen.Seen{Bound: true, Path: "route58.test", User: "test"})
		if out.Error != "" {
			return out, errors.New(out.Error)
		}
		return out, nil
	}}

	d, got, err := cl.serve(Request{Model: "qwen2.5:0.5b"}, []string{"lmstudio", "ollama"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Host != "ollama" || got.Host != "ollama" || got.Text != "hi" {
		t.Fatalf("the caller did not move on from a host that refused it: %+v %+v", d, got)
	}
	if refusals != 1 || answers != 1 {
		t.Fatalf("%d refusals and %d answers: the fallback sent the wrong number of requests", refusals, answers)
	}

	d, got, err = cl.serve(Request{Model: "qwen2.5:0.5b"}, []string{"lmstudio"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Verdict != Unauthorised || d.Endpoint != "" || got.Host != "" {
		t.Fatalf("a request authorising only a host that refused it was served anyway: %+v %+v", d, got)
	}
	if refusals != 2 {
		t.Fatalf("the only authorised host was asked %d times, expected twice", refusals)
	}
	if answers != 1 {
		t.Fatalf("an authorisation emptied by a failing host was read as authorising everything: ollama was asked %d times", answers)
	}
}

func TestAnAbsentHostIsNamedAndNotPassed(t *testing.T) {
	dead := []*Host{Lemonade("http://127.0.0.1:1"), LMStudio("http://127.0.0.1:2"),
		Ollama("http://127.0.0.1:3"), ComfyUI("http://127.0.0.1:4")}
	r := New(dead...)
	r.Survey()
	hosts, _, _, _, _ := r.Residency(false)
	up, absent := present(hosts)
	if len(up) != 0 {
		t.Fatalf("a machine with no host running reported %v as present", up)
	}
	for _, name := range []string{"lemonade", "lmstudio", "ollama", "comfyui"} {
		if !strings.Contains(strings.Join(absent, "; "), name) {
			t.Fatalf("%s was absent and was not named: %v", name, absent)
		}
	}

	continued := false
	t.Run("a machine with nothing running stops rather than passes", func(t *testing.T) {
		guard(t, hosts)
		continued = true
	})
	if continued {
		t.Fatal("the run went past four absent hosts instead of skipping")
	}

	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		io.WriteString(w, `{"models":[{"name":"qwen2.5:0.5b"}]}`)
	}))
	defer live.Close()
	r = New(dead[0], dead[1], Ollama(live.URL), dead[3])
	r.Survey()
	hosts, _, _, _, _ = r.Residency(false)
	up, absent = present(hosts)
	if got := guard(t, hosts); len(got) != 1 {
		t.Fatalf("one host running was not enough to continue: %v", got)
	}
	if len(up) != 1 || up[0] != "ollama" {
		t.Fatalf("the one host that was running was not the one reported present: %v", up)
	}
	if len(absent) != 3 {
		t.Fatalf("a run that proceeds still names what was missing, got %v", absent)
	}
}

// The one field where a language getting it wrong turns a hard requirement into
// a preference: no authorised host has to survive the wire as no authorised
// host, and not as the absent key that authorises every one of them.
func TestAnEmptyAuthorisationIsNotAnAbsentOne(t *testing.T) {
	none := []string{}
	raw, err := json.Marshal(Request{Op: OpRoute, Model: "qwen2.5:0.5b", Hosts: &none})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"hosts":[]`) {
		t.Fatalf("an empty authorisation did not reach the wire: %s", raw)
	}
	var back Request
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.Hosts == nil || len(*back.Hosts) != 0 {
		t.Fatalf("an empty authorisation came back as %v", back.Hosts)
	}
	var absent Request
	if err := json.Unmarshal([]byte(`{"op":"route","model":"qwen2.5:0.5b"}`), &absent); err != nil {
		t.Fatal(err)
	}
	if absent.Hosts != nil {
		t.Fatalf("a request that authorised nothing in particular came back constrained to %v", *absent.Hosts)
	}

	m := machine(t)
	if d, _ := m.Route(Request{Model: "qwen/qwen3.6-35b-a3b", Hosts: &none}); d.Verdict != Unauthorised {
		t.Fatalf("an empty authorisation was served: %+v", d)
	}
	if d, _ := m.Route(Request{Model: "qwen/qwen3.6-35b-a3b"}); d.Verdict != Resident {
		t.Fatalf("an absent authorisation was treated as a constraint: %+v", d)
	}
}
