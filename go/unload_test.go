package router

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A host with no mechanism says so rather than pretending it let go.
func TestHostWithoutAnUnloadMechanismSaysSo(t *testing.T) {
	h := WhisperCPP("http://127.0.0.1:1")
	if h.Unloadable() {
		t.Fatal("whisper.cpp claims an unload mechanism it does not have")
	}
	if err := h.Unload(context.Background()); !errors.Is(err, ErrNoUnload) {
		t.Fatalf("unload of a host with no mechanism returned %v", err)
	}
}

// LM Studio is unloaded by instance, which is what the 2026-09-22 measurement
// unloaded by, and every loaded instance is asked.
func TestLMStudioUnloadsEveryLoadedInstance(t *testing.T) {
	var unloaded []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/models":
			// The build on this machine names the identifier id; the load
			// reply names it instance_id. Both are read.
			io.WriteString(w, `{"models":[
				{"key":"gemma","loaded_instances":[{"id":"gemma:1"}]},
				{"key":"qwen","loaded_instances":[{"instance_id":"qwen:2"}]},
				{"key":"idle","loaded_instances":[]}]}`)
		case "/api/v1/models/unload":
			var body struct {
				InstanceID string `json:"instance_id"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.InstanceID == "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			unloaded = append(unloaded, body.InstanceID)
			io.WriteString(w, `{"instance_id":"`+body.InstanceID+`"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	h := LMStudio(server.URL)
	if !h.Unloadable() {
		t.Fatal("LM Studio has an unload endpoint and the adapter does not offer it")
	}
	if err := h.Unload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(unloaded, ",") != "gemma:1,qwen:2" {
		t.Fatalf("unloaded %v; a host holding two models must give back both", unloaded)
	}
}

// A host that refuses carries its own words back, so the refusal reaches the
// record with a reason.
func TestLMStudioRefusalCarriesItsReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/models" {
			io.WriteString(w, `{"models":[{"key":"gemma","loaded_instances":[{"id":"gemma:1"}]}]}`)
			return
		}
		w.WriteHeader(http.StatusConflict)
		io.WriteString(w, `{"error":"model is generating"}`)
	}))
	defer server.Close()
	err := LMStudio(server.URL).Unload(context.Background())
	if err == nil || !strings.Contains(err.Error(), "model is generating") {
		t.Fatalf("the refusal lost its reason: %v", err)
	}
}

// Lemonade's native empty-body unload requests all models to be unloaded.
// This test verifies only the adapter request shape; the resource instrument
// decides whether the request actually freed memory.
func TestLemonadeRequestsNativeUnloadAll(t *testing.T) {
	type requestInfo struct{ method, path, body string }
	requests := make(chan requestInfo, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		requests <- requestInfo{method: r.Method, path: r.URL.Path, body: string(data)}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	h := Lemonade(server.URL)
	if !h.Unloadable() {
		t.Fatal("Lemonade has a native unload endpoint, but the adapter does not expose it")
	}
	if err := h.Unload(context.Background()); err != nil {
		t.Fatal(err)
	}
	request := <-requests
	if request.method != http.MethodPost || request.path != "/api/v1/unload" || request.body != "" {
		t.Fatalf("native unload request was %s %s with body %q", request.method, request.path, request.body)
	}
}

func TestLemonadeUnloadPropagatesNativeRefusal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		io.WriteString(w, `{"error":"model is busy"}`)
	}))
	defer server.Close()

	err := Lemonade(server.URL).Unload(context.Background())
	if err == nil || !strings.Contains(err.Error(), "model is busy") {
		t.Fatalf("native refusal lost its reason: %v", err)
	}
}

func TestLemonadeUnloadHonorsCancellation(t *testing.T) {
	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Lemonade(server.URL).Unload(ctx) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Lemonade unload request did not reach the fixture")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled native unload returned success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Lemonade unload did not return after cancellation")
	}
}

// Ollama lets go on an empty request with keep_alive 0, one per resident
// model.
func TestOllamaUnloadsWithKeepAliveZero(t *testing.T) {
	var asked []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/ps":
			io.WriteString(w, `{"models":[{"name":"llama3:8b"}]}`)
		case "/api/generate":
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if body["keep_alive"] != float64(0) {
				t.Errorf("keep_alive is %v, not 0", body["keep_alive"])
			}
			if _, has := body["prompt"]; has {
				t.Error("the unload request carries something to generate")
			}
			asked = append(asked, body["model"].(string))
			io.WriteString(w, `{"done":true}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	if err := Ollama(server.URL).Unload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || asked[0] != "llama3:8b" {
		t.Fatalf("asked %v", asked)
	}
}

// ComfyUI frees both the models and the memory behind them.
func TestComfyUIFreesModelsAndMemory(t *testing.T) {
	var body map[string]bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/free" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewDecoder(r.Body).Decode(&body)
		io.WriteString(w, `{}`)
	}))
	defer server.Close()
	if err := ComfyUI(server.URL).Unload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !body["unload_models"] || !body["free_memory"] {
		t.Fatalf("/api/free was asked for %+v", body)
	}
}
