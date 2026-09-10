package router

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"
)

// A Host is a model runtime this machine already has. The router reads it and
// never writes to it: it does not load, unload, evict or hold weights, because
// a host holding three models loses all three to one failed load where three
// hosts lose one.
type Host struct {
	Name      string
	Base      string
	Chat      string
	installed func(*Host) ([]string, error)
	resident  func(*Host) ([]string, error)
}

func (h *Host) Servable() bool { return h.Chat != "" }

func (h *Host) Endpoint() string {
	if h.Chat == "" {
		return ""
	}
	return h.Base + h.Chat
}

var client = &http.Client{Timeout: 2 * time.Second}

func fetch(url string, into any) error {
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(into)
}

func Lemonade(base string) *Host {
	return &Host{Name: "lemonade", Base: base, Chat: "/api/v1/chat/completions",
		installed: func(h *Host) ([]string, error) {
			var d struct {
				Data []struct {
					ID         string `json:"id"`
					Checkpoint string `json:"checkpoint"`
					Downloaded bool   `json:"downloaded"`
				} `json:"data"`
			}
			if err := fetch(h.Base+"/api/v1/models", &d); err != nil {
				return nil, err
			}
			var out []string
			for _, m := range d.Data {
				if m.Downloaded {
					out = append(out, m.ID)
					if m.Checkpoint != "" {
						out = append(out, m.Checkpoint)
					}
				}
			}
			return out, nil
		},
		resident: func(h *Host) ([]string, error) {
			var d struct {
				ModelLoaded string `json:"model_loaded"`
				All         []struct {
					ModelName string `json:"model_name"`
					Loaded    bool   `json:"loaded"`
				} `json:"all_models_loaded"`
			}
			if err := fetch(h.Base+"/api/v1/health", &d); err != nil {
				return nil, err
			}
			var out []string
			for _, m := range d.All {
				if m.Loaded {
					out = append(out, m.ModelName)
				}
			}
			if d.ModelLoaded != "" && !has(out, d.ModelLoaded) {
				out = append(out, d.ModelLoaded)
			}
			return out, nil
		}}
}

func LMStudio(base string) *Host {
	list := func(h *Host) ([]struct {
		ID    string `json:"id"`
		Type  string `json:"type"`
		State string `json:"state"`
	}, error) {
		var d struct {
			Data []struct {
				ID    string `json:"id"`
				Type  string `json:"type"`
				State string `json:"state"`
			} `json:"data"`
		}
		err := fetch(h.Base+"/api/v0/models", &d)
		return d.Data, err
	}
	return &Host{Name: "lmstudio", Base: base, Chat: "/v1/chat/completions",
		installed: func(h *Host) ([]string, error) {
			d, err := list(h)
			if err != nil {
				return nil, err
			}
			var out []string
			for _, m := range d {
				if m.Type == "llm" || m.Type == "vlm" {
					out = append(out, m.ID)
				}
			}
			return out, nil
		},
		resident: func(h *Host) ([]string, error) {
			d, err := list(h)
			if err != nil {
				return nil, err
			}
			var out []string
			for _, m := range d {
				if m.State == "loaded" {
					out = append(out, m.ID)
				}
			}
			return out, nil
		}}
}

func Ollama(base string) *Host {
	names := func(url string) ([]string, error) {
		var d struct {
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
		}
		if err := fetch(url, &d); err != nil {
			return nil, err
		}
		var out []string
		for _, m := range d.Models {
			out = append(out, m.Name)
		}
		return out, nil
	}
	return &Host{Name: "ollama", Base: base, Chat: "/v1/chat/completions",
		installed: func(h *Host) ([]string, error) { return names(h.Base + "/api/tags") },
		resident:  func(h *Host) ([]string, error) { return names(h.Base + "/api/ps") }}
}

// ComfyUI runs a graph, not a model, so no OpenAI-compatible endpoint can serve
// what it holds and Chat stays empty. It is here because a quarter of this
// machine's weights are in its tree and an inventory that omits them is wrong.
func ComfyUI(base string) *Host {
	return &Host{Name: "comfyui", Base: base,
		installed: func(h *Host) ([]string, error) {
			var folders []string
			if err := fetch(h.Base+"/models", &folders); err != nil {
				return nil, err
			}
			var mu sync.Mutex
			var wg sync.WaitGroup
			var out []string
			for _, f := range folders {
				wg.Add(1)
				go func(f string) {
					defer wg.Done()
					var files []string
					if fetch(h.Base+"/models/"+f, &files) != nil {
						return
					}
					mu.Lock()
					defer mu.Unlock()
					for _, name := range files {
						out = append(out, f+"/"+name)
					}
				}(f)
			}
			wg.Wait()
			sort.Strings(out)
			return out, nil
		},
		resident: func(*Host) ([]string, error) { return nil, nil }}
}

func has(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}

// Installed is the four runtimes this machine has, in the order a decision
// prefers them.
func Installed() []*Host {
	return []*Host{
		Lemonade("http://127.0.0.1:13305"),
		LMStudio("http://127.0.0.1:1234"),
		Ollama("http://127.0.0.1:11434"),
		ComfyUI("http://127.0.0.1:8188"),
	}
}
