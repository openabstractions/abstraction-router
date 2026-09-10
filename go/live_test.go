package router

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"testing"
	"time"
)

// TestLive answers the three questions against the hosts actually installed on
// this machine, times every answer against the 200 ms discovery budget, and
// shows what an unidentifiable caller gets. It loads nothing: the only request
// it sends to a host is one token to a model that host already holds.
//
// Guarded by ROUTER_LIVE because it reads whatever is running.
func TestLive(t *testing.T) {
	if os.Getenv("ROUTER_LIVE") == "" {
		t.Skip("set ROUTER_LIVE=1 to run against this machine's hosts")
	}
	t.Log("STATE: " + powerState())

	r := New(Installed()...)
	endpoint := DefaultEndpoint() + "-live"
	s, err := Start(endpoint, r)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r.Survey()

	c := &Client{Endpoint: endpoint}
	models, err := c.Ask(Request{Op: OpModels})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("caller the kernel vouched for: %s", models.Caller)
	t.Logf("ANSWER 1 — %d families under %d host names", len(models.Models), aliases(models.Models))
	for _, f := range models.Models {
		if len(f.Names) > 1 {
			t.Logf("  %s  %s", f.Family, names(f))
		}
	}

	res, err := c.Ask(Request{Op: OpResidency})
	if err != nil {
		t.Fatal(err)
	}
	t.Log("ANSWER 2 — who holds what")
	for _, h := range res.Hosts {
		if h.Up {
			t.Logf("  %-9s up   %4d installed  resident %v", h.Host, h.Installed, h.Resident)
		} else {
			t.Logf("  %-9s down %s", h.Host, h.Why)
		}
	}
	if res.GPUWhy != "" {
		t.Logf("  GPU unreadable: %s", res.GPUWhy)
	}
	for _, g := range res.GPU {
		t.Logf("  GPU %-28s pid %-7d %6.2f GiB", g.Process, g.PID, g.GiB)
	}
	t.Logf("  doubled: %v", res.Doubled)

	want := os.Getenv("ROUTER_LIVE_MODEL")
	if want == "" {
		want = firstResident(res.Hosts)
	}
	if want == "" {
		t.Log("ANSWER 3 — no host holds anything right now; nothing is loaded to prove reuse against")
		return
	}
	route, err := c.Ask(Request{Op: OpRoute, Model: want})
	if err != nil {
		t.Fatal(err)
	}
	d := route.Decision
	t.Logf("ANSWER 3 — %q -> %s: %s %s (%s), loads %d", want, d.Family, d.Verdict, d.Host, d.Endpoint, d.Loads)
	t.Logf("  installed on %v; %s", d.InstalledOn, d.Why)

	for _, h := range Installed() {
		start := time.Now()
		installed, err := h.installed(h)
		t.Logf("READ    %-9s %4d names in %6.1f ms  %v", h.Name, len(installed), ms(time.Since(start)), err)
	}
	for _, op := range []Request{{Op: OpModels}, {Op: OpResidency}, {Op: OpRoute, Model: want},
		{Op: OpRoute, Model: want, Fresh: true}} {
		lo, mid, hi := latency(t, c, op)
		fresh := ""
		if op.Fresh {
			fresh = " (fresh: all four hosts read first)"
		}
		t.Logf("LATENCY %-10s min %6.1f ms  p50 %6.1f ms  max %6.1f ms%s", op.Op,
			ms(lo), ms(mid), ms(hi), fresh)
	}

	if d.Verdict == Resident {
		start, _ := c.Ask(Request{Op: OpResidency, Fresh: true})
		served, err := oneToken(d.Endpoint, d.Model)
		after, _ := c.Ask(Request{Op: OpResidency, Fresh: true})
		t.Logf("SERVED  %s answered from the copy it already had: %s (err %v)", d.Host, served, err)
		// Another task on this machine is loading weights of its own, so the
		// machine total moves for reasons that are not this request. Per process
		// is the only reading that answers "did routing here load anything".
		for _, b := range start.GPU {
			t.Logf("  GPU %-24s pid %-7d %6.2f -> %6.2f GiB", b.Process, b.PID, b.GiB, gib(after.GPU, b.PID))
		}
	}

	l, err := Window("127.0.0.1:0", r)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	t.Logf("REFUSED %s", get(t, "http://"+l.Addr().String()+"/route?model="+want))
	t.Logf("ALLOWED %.120s", get(t, "http://"+l.Addr().String()+"/models"))
}

func latency(t *testing.T, c *Client, req Request) (lo, mid, hi time.Duration) {
	t.Helper()
	var d []time.Duration
	for i := 0; i < 20; i++ {
		start := time.Now()
		if _, err := c.Ask(req); err != nil {
			t.Fatal(err)
		}
		d = append(d, time.Since(start))
	}
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	return d[0], d[len(d)/2], d[len(d)-1]
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func aliases(fs []Family) int {
	n := 0
	for _, f := range fs {
		n += len(f.Names)
	}
	return n
}

func names(f Family) string {
	out := ""
	for _, a := range f.Names {
		mark := ""
		if a.Resident {
			mark = "*"
		}
		out += fmt.Sprintf("[%s%s %s] ", a.Host, mark, a.Name)
	}
	return out
}

func firstResident(hs []HostState) string {
	for _, h := range hs {
		if h.Up && h.Servable && len(h.Resident) > 0 {
			return h.Resident[0]
		}
	}
	return ""
}

func held(g []Holder) float64 {
	t := 0.0
	for _, h := range g {
		t += h.GiB
	}
	return round(t)
}

func oneToken(endpoint, model string) (string, error) {
	body, _ := json.Marshal(map[string]any{"model": model, "max_tokens": 1, "stream": false,
		"messages": []map[string]string{{"role": "user", "content": "hi"}}})
	req, _ := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Model   string `json:"model"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content   string `json:"content"`
				Reasoning string `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			Completion int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || len(out.Choices) == 0 {
		return resp.Status, err
	}
	c := out.Choices[0]
	return fmt.Sprintf("model %q, %d completion tokens, finish %q, text %q%q",
		out.Model, out.Usage.Completion, c.FinishReason, c.Message.Content, c.Message.Reasoning), nil
}

func get(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	return resp.Status + " " + buf.String()
}

func gib(hs []Holder, pid int) float64 {
	for _, h := range hs {
		if h.PID == pid {
			return h.GiB
		}
	}
	return 0
}
