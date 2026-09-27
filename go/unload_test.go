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
