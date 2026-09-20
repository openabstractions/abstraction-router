package router

import (
	"sync"
	"testing"
)

// A service that changes its host configuration while it runs routes only to
// the new hosts from the next survey on, and concurrent reads stay consistent.
func TestSetHostsReplacesWhatTheRouterReads(t *testing.T) {
	first := serve(t, map[string]string{"/api/tags": `{"models":[{"name":"qwen2.5:0.5b"}]}`, "/api/ps": `{"models":[]}`})
	second := serve(t, map[string]string{"/api/v0/models": `{"data":[{"id":"google/gemma-4-26b-it","type":"llm","state":"loaded"}]}`})
	r := New(Ollama(first))
	r.Survey()
	if d, _ := r.Route(Request{Model: "qwen2.5:0.5b"}); d.Host != "ollama" {
		t.Fatalf("before: %+v", d)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			r.Route(Request{Model: "qwen2.5:0.5b"})
			r.Hosts()
		}
	}()
	r.SetHosts(LMStudio(second))
	wg.Wait()
	if d, _ := r.Route(Request{Model: "qwen2.5:0.5b"}); d.Host != "" {
		t.Fatalf("a removed host still served before the next survey: %+v", d)
	}
	r.Survey()
	if d, _ := r.Route(Request{Model: "gemma-4-26b-it"}); d.Verdict != Resident || d.Host != "lmstudio" {
		t.Fatalf("after: %+v", d)
	}
	if hosts := r.Hosts(); len(hosts) != 1 || hosts[0].Name != "lmstudio" {
		t.Fatalf("hosts %v", hosts)
	}
}
