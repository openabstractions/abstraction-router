package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Every host on this machine already has a mechanism for letting go of what it
// holds; none of them can see another engine's hold
// (research/resources/PROPOSAL.md §2). These are those mechanisms, addressed
// by the same adapters that read each host, so the resource service can yield
// an attached holder on its behalf through the host's own API
// (abstraction-resource CONTRACT.md RES-L3).
//
// The router itself still writes to no host: nothing here is called by a
// route, an inventory read or a residency read. A caller has to ask for an
// unload by name, and only the lease service does.

// unloadClient is separate from the router's read client: an unload takes as
// long as the host takes to free the weights, and the two-second read timeout
// would abandon the call while the host was still working.
var unloadClient = &http.Client{Timeout: 60 * time.Second}

// ErrNoUnload is a host with no mechanism for letting go.
var ErrNoUnload = errors.New("router: this host has no unload mechanism")

// Unloadable reports whether this host can be asked to let go of what it
// holds.
func (h *Host) Unloadable() bool { return h.unload != nil }

// Unload asks the host's own API to release every model it holds. It returns
// when the host answered, which is not when the bytes are gone: the caller
// reads the instrument for that.
func (h *Host) Unload(ctx context.Context) error {
	if h.unload == nil {
		return ErrNoUnload
	}
	return h.unload(ctx, h)
}

// postJSON sends one JSON body and reads the reply's status. The body is bounded
// on the way back because an error page is not a reply.
func postJSON(ctx context.Context, url string, body any) error {
	var encoded []byte
	if body != nil {
		var err error
		if encoded, err = json.Marshal(body); err != nil {
			return err
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	resp, err := unloadClient.Do(request)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	//unchecked: reply is best-effort diagnostic text for the error message below; a partial read on failure still shows useful content
	reply, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%s: %s: %s", url, resp.Status, bytes.TrimSpace(reply))
	}
	return nil
}

// fetchContext is fetch with a caller's deadline, for the listings an unload
// has to read first.
func fetchContext(ctx context.Context, url string, into any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := unloadClient.Do(request)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(into)
}

// lmStudioInstances is the loaded instances LM Studio's own REST API reports.
// Its /api/v1/models listing carries a loaded_instances array per model, and
// the unload endpoint takes one instance identifier, which is what the
// 2026-09-22 measurement unloaded by.
//
// The build on this machine names that identifier id inside loaded_instances
// and instance_id in the load reply. Both are read, because one LM Studio
// build's field name is not the API, and a model whose instance carries
// neither is unloaded by its own key, which that build accepts.
func lmStudioInstances(ctx context.Context, base string) ([]string, error) {
	var listing struct {
		Models []struct {
			Key       string `json:"key"`
			Instances []struct {
				ID         string `json:"id"`
				InstanceID string `json:"instance_id"`
				Identifier string `json:"identifier"`
			} `json:"loaded_instances"`
		} `json:"models"`
	}
	if err := fetchContext(ctx, base+"/api/v1/models", &listing); err != nil {
		return nil, err
	}
	var out []string
	for _, model := range listing.Models {
		for _, instance := range model.Instances {
			switch {
			case instance.ID != "":
				out = append(out, instance.ID)
			case instance.InstanceID != "":
				out = append(out, instance.InstanceID)
			case instance.Identifier != "":
				out = append(out, instance.Identifier)
			default:
				out = append(out, model.Key)
			}
		}
	}
	return out, nil
}

// unloadLMStudio unloads every loaded instance. A host holding two models
// gives back both: the asker asked for bytes, not for a particular model, and
// the service reports what the instrument then shows.
func unloadLMStudio(ctx context.Context, h *Host) error {
	instances, err := lmStudioInstances(ctx, h.Base)
	if err != nil {
		return err
	}
	if len(instances) == 0 {
		return nil
	}
	var failures []error
	for _, instance := range instances {
		if err := postJSON(ctx, h.Base+"/api/v1/models/unload", map[string]string{"instance_id": instance}); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// unloadOllama asks Ollama to let go of each resident model the way Ollama
// itself documents: a request naming the model with keep_alive 0 and nothing
// to generate.
func unloadOllama(ctx context.Context, h *Host) error {
	var running struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := fetchContext(ctx, h.Base+"/api/ps", &running); err != nil {
		return err
	}
	var failures []error
	for _, model := range running.Models {
		body := map[string]any{"model": model.Name, "keep_alive": 0}
		if err := postJSON(ctx, h.Base+"/api/generate", body); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// unloadComfyUI frees both the models ComfyUI holds and the memory behind
// them, which is what its own /api/free takes.
func unloadComfyUI(ctx context.Context, h *Host) error {
	return postJSON(ctx, h.Base+"/api/free", map[string]bool{"unload_models": true, "free_memory": true})
}
