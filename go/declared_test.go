package router

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// fixture is a machine made of files, variables and command output only.
type fixture struct {
	files  map[string]string
	env    map[string]string
	status string
	logged []string
	ran    []string
}

func (f *fixture) probeEnv(goos string) ProbeEnv {
	return ProbeEnv{GOOS: goos, Home: "/home/person", AppData: `C:\Users\person\AppData\Roaming`,
		Getenv: func(name string) string { return f.env[name] },
		ReadFile: func(path string) ([]byte, error) {
			body, ok := f.files[path]
			if !ok {
				return nil, &fs.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
			}
			if body == "<denied>" {
				return nil, &fs.PathError{Op: "open", Path: path, Err: fs.ErrPermission}
			}
			return []byte(body), nil
		},
		Stat: func(path string) bool { _, ok := f.files[path]; return ok },
		Status: func(_ context.Context, program string, args ...string) ([]byte, error) {
			f.ran = append(f.ran, program+" "+strings.Join(args, " "))
			if f.status == "" {
				return nil, errors.New("executable file not found")
			}
			return []byte(f.status), nil
		},
		Log: func(product, reason string) { f.logged = append(f.logged, product+": "+reason) }}
}

// Each product's fixture record yields the base the product recorded.
func TestEachProductRecordYieldsItsBase(t *testing.T) {
	for _, c := range []struct {
		name  string
		goos  string
		f     fixture
		probe func(ProbeEnv) (Declaration, bool)
		base  string
	}{
		{"ollama host and port", "linux", fixture{env: map[string]string{"OLLAMA_HOST": "127.0.0.1:11500"}}, wrap(ProbeOllama), "http://127.0.0.1:11500"},
		{"ollama bind all", "linux", fixture{env: map[string]string{"OLLAMA_HOST": "0.0.0.0"}}, wrap(ProbeOllama), "http://127.0.0.1:11434"},
		{"ollama scheme without port", "linux", fixture{env: map[string]string{"OLLAMA_HOST": "https://models.lan"}}, wrap(ProbeOllama), "https://models.lan:443"},
		{"ollama ipv6 unspecified", "linux", fixture{env: map[string]string{"OLLAMA_HOST": "[::]:9000"}}, wrap(ProbeOllama), "http://[::1]:9000"},
		{"lm studio new layout", "linux", fixture{files: map[string]string{"/home/person/.lmstudio/.internal/http-server-config.json": `{"port":41343,"networkInterface":"0.0.0.0","cors":false}`}}, wrap(ProbeLMStudio), "http://127.0.0.1:41343"},
		{"lm studio old layout", "darwin", fixture{files: map[string]string{"/home/person/.cache/lm-studio/.internal/http-server-config.json": `{"port":2345}`}}, wrap(ProbeLMStudio), "http://127.0.0.1:2345"},
		{"lm studio windows", "windows", fixture{files: map[string]string{`/home/person\.lmstudio\.internal\http-server-config.json`: `{"port":1300,"networkInterface":"127.0.0.1"}`}}, wrap(ProbeLMStudio), "http://127.0.0.1:1300"},
		{"docker desktop windows", "windows", fixture{files: map[string]string{`C:\Users\person\AppData\Roaming\Docker\settings-store.json`: `{"EnableDockerAI":true,"EnableInference":true,"EnableInferenceTCP":true}`}}, ProbeDockerModelRunner, "http://127.0.0.1:12434"},
		{"docker desktop macos", "darwin", fixture{files: map[string]string{"/home/person/Library/Group Containers/group.com.docker/settings-store.json": `{"EnableInferenceTCP":true}`}}, ProbeDockerModelRunner, "http://127.0.0.1:12434"},
		{"docker engine plugin", "linux", fixture{files: map[string]string{"/usr/libexec/docker/cli-plugins/docker-model": ""}}, ProbeDockerModelRunner, "http://127.0.0.1:12434"},
		{"foundry local", "windows", fixture{status: "🟢 Model management service is running on http://127.0.0.1:52403/openai/status\n"}, ProbeFoundryLocal, "http://127.0.0.1:52403"},
		{"foundry local by name", "darwin", fixture{status: "🟢 Model management service is running on http://localhost:50270/openai/status\n"}, ProbeFoundryLocal, "http://127.0.0.1:50270"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, ok := c.probe(c.f.probeEnv(c.goos))
			if !ok || d.Base != c.base || d.Source == "default" || d.Reason != "" || len(c.f.logged) != 0 {
				t.Fatalf("declaration %+v declared %v, logged %v; want %s", d, ok, c.f.logged, c.base)
			}
		})
	}
}

func wrap(probe func(ProbeEnv) Declaration) func(ProbeEnv) (Declaration, bool) {
	return func(env ProbeEnv) (Declaration, bool) { return probe(env), true }
}

// A missing record yields the product's documented default, or no host for a
// product that serves no host port unless its record says so.
func TestAMissingRecordYieldsTheDefault(t *testing.T) {
	f := &fixture{}
	env := f.probeEnv("linux")
	if d := ProbeOllama(env); d.Base != OllamaDefaultBase || d.Source != "default" || d.Reason != "" {
		t.Fatalf("ollama %+v", d)
	}
	if d := ProbeLMStudio(env); d.Base != LMStudioDefaultBase || d.Source != "default" || d.Reason != "" {
		t.Fatalf("lm studio %+v", d)
	}
	if d, ok := ProbeDockerModelRunner(env); ok {
		t.Fatalf("docker model runner declared without a record: %+v", d)
	}
	if d, ok := ProbeFoundryLocal(env); ok || len(f.ran) != 1 || f.ran[0] != "foundry service status" {
		t.Fatalf("foundry local declared %+v %v; ran %v", d, ok, f.ran)
	}
	f.files = map[string]string{`C:\Users\person\AppData\Roaming\Docker\settings-store.json`: `{"EnableDockerAI":true,"EnableInferenceTCP":false}`}
	if d, ok := ProbeDockerModelRunner(f.probeEnv("windows")); ok {
		t.Fatalf("host-side TCP off still declared %+v", d)
	}
	f.status = "🔴 Model management service is not running!\n"
	if d, ok := ProbeFoundryLocal(f.probeEnv("windows")); ok {
		t.Fatalf("a stopped Foundry Local declared %+v", d)
	}
	if len(f.logged) != 0 {
		t.Fatalf("absent records logged %v", f.logged)
	}
	hosts := Declared(f.probeEnv("linux"))
	var names []string
	for _, h := range hosts {
		names = append(names, h.Name+"="+h.Base+"/"+h.DeclaredBy)
	}
	if strings.Join(names, " ") != "lemonade=http://127.0.0.1:13305/default lmstudio=http://127.0.0.1:1234/lmstudio ollama=http://127.0.0.1:11434/ollama comfyui=http://127.0.0.1:8188/default" {
		t.Fatalf("declared hosts %v", names)
	}
}

// A malformed record yields the default and a logged reason.
func TestAMalformedRecordYieldsTheDefaultAndAReason(t *testing.T) {
	for _, c := range []struct {
		name  string
		f     fixture
		probe func(ProbeEnv) (Declaration, bool)
		base  string
	}{
		{"ollama port", fixture{env: map[string]string{"OLLAMA_HOST": "127.0.0.1:99999"}}, wrap(ProbeOllama), OllamaDefaultBase},
		{"ollama scheme", fixture{env: map[string]string{"OLLAMA_HOST": "ftp://127.0.0.1:1"}}, wrap(ProbeOllama), OllamaDefaultBase},
		{"lm studio json", fixture{files: map[string]string{"/home/person/.lmstudio/.internal/http-server-config.json": `{"port":`}}, wrap(ProbeLMStudio), LMStudioDefaultBase},
		{"lm studio port", fixture{files: map[string]string{"/home/person/.lmstudio/.internal/http-server-config.json": `{"port":0}`}}, wrap(ProbeLMStudio), LMStudioDefaultBase},
		{"lm studio unreadable", fixture{files: map[string]string{"/home/person/.lmstudio/.internal/http-server-config.json": `<denied>`}}, wrap(ProbeLMStudio), LMStudioDefaultBase},
		{"docker settings", fixture{files: map[string]string{"/home/person/.docker/desktop/settings-store.json": `[`}}, ProbeDockerModelRunner, ""},
		{"foundry output", fixture{status: "Model management service is running on http://10.0.0.5:5273/openai/status"}, ProbeFoundryLocal, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, ok := c.probe(c.f.probeEnv("linux"))
			if c.base == "" && ok || c.base != "" && d.Base != c.base || d.Reason == "" || len(c.f.logged) != 1 || !strings.Contains(c.f.logged[0], d.Reason) {
				t.Fatalf("declaration %+v declared %v, logged %v", d, ok, c.f.logged)
			}
		})
	}
}

// Probing opens no connection, and a router over the declared hosts surveys
// only the addresses the products recorded: a server on a port nobody
// recorded sees nothing.
func TestNothingOpensAPortTheProductDidNotRecord(t *testing.T) {
	var recorded, unrecorded atomic.Int64
	count := func(n *atomic.Int64) *httptest.Server {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			n.Add(1)
			w.Write([]byte(`{"models":[],"data":[]}`))
		}))
		t.Cleanup(s.Close)
		return s
	}
	lmstudio := count(&recorded)
	stray := count(&unrecorded)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(lmstudio.URL, "http://"))
	f := &fixture{files: map[string]string{"/home/person/.lmstudio/.internal/http-server-config.json": `{"port":` + port + `}`},
		env: map[string]string{"OLLAMA_HOST": strings.TrimPrefix(lmstudio.URL, "http://")}}
	hosts := Declared(f.probeEnv("linux"))
	if recorded.Load() != 0 || unrecorded.Load() != 0 {
		t.Fatal("probing opened a connection")
	}
	bases := map[string]bool{}
	for _, h := range hosts {
		bases[h.Base] = true
	}
	if bases[stray.URL] || !bases[lmstudio.URL] {
		t.Fatalf("declared bases %v", bases)
	}
	New(hosts[1], hosts[2]).Survey()
	if recorded.Load() == 0 || unrecorded.Load() != 0 {
		t.Fatalf("survey reached recorded %d, unrecorded %d", recorded.Load(), unrecorded.Load())
	}
}

// A declared host lists its declared_by through the router's host states.
func TestHostsCarryDeclaredBy(t *testing.T) {
	base := serve(t, map[string]string{"/engines/v1/models": `{"data":[{"id":"ai/smollm2"}]}`})
	h := DockerModelRunner(base)
	h.DeclaredBy = DeclaredByDockerModelRunner
	r := New(h)
	r.Survey()
	states, _, _, _, _ := r.Residency(false)
	if len(states) != 1 || states[0].DeclaredBy != DeclaredByDockerModelRunner || !states[0].Up || states[0].Installed != 1 {
		t.Fatalf("states %+v", states)
	}
	if d, _ := r.Route(Request{Model: "ai/smollm2"}); d.Verdict != WouldLoad || d.Endpoint != base+"/engines/v1/chat/completions" {
		t.Fatalf("route %+v", d)
	}
}
