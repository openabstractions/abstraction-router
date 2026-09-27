package router

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/openabstractions/abstraction-resource/go/instrument"
)

// A Host is a model runtime this machine already has. The router reads it and
// never writes to it: it does not load, unload, evict or hold weights, because
// a host holding three models loses all three to one failed load where three
// hosts lose one.
//
// A hosted host is a provider endpoint off this machine. Hosted is true, Wire
// names the protocol it speaks (WireOpenAICompatible, WireAnthropicMessages or
// <owner>/<name>@<n>) and Credential names the abstraction.credentials entry
// the service applies to every request it sends there. Its models come from
// the provider's listing, read with that credential; none is ever resident.
type Host struct {
	Name string
	// BindingID is the service-owned registration identity. It remains stable
	// across restart and changes when the declaration is replaced. Product
	// configurations without registration still bind their endpoint and wire.
	BindingID  string
	Base       string
	Chat       string
	Hosted     bool
	Wire       string
	Credential string
	// DeclaredBy names what registered the host: operator for a person's
	// configuration, a product's name for a host its own record declared
	// (declared.go), or default for a built-in address.
	DeclaredBy string
	// Profiles are what the host serves (profiles.go); empty is the default
	// for its wire. A model whose host reports its own profiles is judged by
	// those instead.
	Profiles []string
	// Domain names the remote runtime an oa-remote@1 host is (remote.go).
	Domain    string
	remote    *remoteLink
	native    *nativeLink
	installed func(*Host) ([]string, error)
	resident  func(*Host) ([]string, error)
	listing   func(ctx context.Context, h *Host, headers map[string]string) ([]string, error)
	// modelProfiles reads the profiles of installed models from the host's own
	// metadata; nil when the host reports none.
	modelProfiles func(h *Host, names []string) map[string][]string
	// modelContextLength reads each installed model's context window in
	// tokens from the host's own metadata, without loading anything; nil when
	// the host reports none, and a name it does carry but reports no length
	// for is simply absent from the map.
	modelContextLength func(h *Host, names []string) map[string]int64
	// unload is the host's own mechanism for letting go of what it holds
	// (unload.go); nil when it has none. The router never calls it: a route,
	// an inventory read and a residency read all leave the host's weights
	// where they are, and only a lease asked back reaches this field.
	unload func(ctx context.Context, h *Host) error
}

func (h *Host) Servable() bool { return h.Chat != "" }

// Endpoint is the address a caller may send to. A hosted host has none: the
// service performs hosted calls, and no endpoint or key reaches the caller.
func (h *Host) Endpoint() string {
	if h.Chat == "" || h.Hosted || h.native != nil {
		return ""
	}
	return h.Base + h.Chat
}

// ChatURL is the chat address the service itself sends to, for either class.
func (h *Host) ChatURL() string {
	if h.Chat == "" {
		return ""
	}
	return h.Base + h.Chat
}

// EmbedURL is the embeddings address beside an OpenAI-compatible chat path
// (<api>/chat/completions becomes <api>/embeddings), or "" when the chat path
// has no such sibling.
func (h *Host) EmbedURL() string {
	api, ok := strings.CutSuffix(h.Chat, "/chat/completions")
	if h.Chat == "" || !ok {
		return ""
	}
	return h.Base + api + "/embeddings"
}

// TranscriptionURL is the prerecorded speech-to-text address for a host that
// declares the transcription profile.
func (h *Host) TranscriptionURL() string {
	if !has(h.HostProfiles(), ProfileTranscription) {
		return ""
	}
	if h.Wire == WireRemote {
		return "abstraction.inference/transcription@1"
	}
	if h.Wire == WireDeepgramPrerecorded || h.Name == "whispercpp" {
		return h.Base + h.Chat
	}
	api, ok := strings.CutSuffix(h.Chat, "/chat/completions")
	if h.Chat == "" || !ok {
		return ""
	}
	return h.Base + api + "/audio/transcriptions"
}

// SpeechURL is the streaming text-to-speech address for a host that declares
// speech. ElevenLabs appends the escaped voice id and /stream.
func (h *Host) SpeechURL() string {
	if !has(h.HostProfiles(), ProfileSpeech) {
		return ""
	}
	if h.Wire == WireRemote {
		return "abstraction.inference/speech@1"
	}
	if h.Wire == WireElevenLabsStream || h.Name == "piper" {
		return h.Base + h.Chat
	}
	api, ok := strings.CutSuffix(h.Chat, "/chat/completions")
	if h.Chat == "" || !ok {
		return ""
	}
	return h.Base + api + "/audio/speech"
}

// SupportsSpeechFormat reports formats the selected backend can actually
// produce. Piper's official server emits WAV; ElevenLabs mappings here are
// limited to codecs expressible by speech@1.
func (h *Host) SupportsSpeechFormat(format string) bool {
	if h.SpeechURL() == "" {
		return false
	}
	switch {
	case h.Wire == WireRemote:
		return true
	case h.Name == "piper":
		return format == "wav"
	case h.Wire == WireElevenLabsStream:
		return format == "mp3" || format == "opus" || format == "pcm"
	default:
		return format == "mp3" || format == "opus" || format == "aac" || format == "flac" || format == "wav" || format == "pcm"
	}
}

// ImageURL is the operation root for a host that explicitly serves image.
// The adapter adds its mode- and model-specific path where the wire requires it.
func (h *Host) ImageURL() string {
	if !has(h.HostProfiles(), ProfileImage) {
		return ""
	}
	if h.Wire == WireRemote {
		return "abstraction.inference/image@1"
	}
	if h.Name == "comfyui" || h.Name == "swarmui" || h.Wire == WireStabilityV2Beta || h.Wire == WireFalQueue || h.Wire == WireReplicatePredictions {
		return h.Base + h.Chat
	}
	api, ok := strings.CutSuffix(h.Chat, "/chat/completions")
	if h.Chat == "" || !ok {
		return ""
	}
	return h.Base + api + "/images"
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

// lemonadeModel is one entry of Lemonade's GET /api/v1/models listing.
type lemonadeModel struct {
	ID               string `json:"id"`
	Checkpoint       string `json:"checkpoint"`
	Downloaded       bool   `json:"downloaded"`
	MaxContextWindow int64  `json:"max_context_window"`
}

func lemonadeList(h *Host) ([]lemonadeModel, error) {
	var d struct {
		Data []lemonadeModel `json:"data"`
	}
	if err := fetch(h.Base+"/api/v1/models", &d); err != nil {
		return nil, err
	}
	return d.Data, nil
}

func Lemonade(base string) *Host {
	return &Host{Name: "lemonade", Base: base, Chat: "/api/v1/chat/completions",
		installed: func(h *Host) ([]string, error) {
			d, err := lemonadeList(h)
			if err != nil {
				return nil, err
			}
			var out []string
			for _, m := range d {
				if m.Downloaded {
					out = append(out, m.ID)
					if m.Checkpoint != "" {
						out = append(out, m.Checkpoint)
					}
				}
			}
			return out, nil
		},
		modelContextLength: func(h *Host, names []string) map[string]int64 {
			d, err := lemonadeList(h)
			if err != nil {
				return nil
			}
			out := map[string]int64{}
			for _, m := range d {
				if m.MaxContextWindow <= 0 {
					continue
				}
				if has(names, m.ID) {
					out[m.ID] = m.MaxContextWindow
				}
				if m.Checkpoint != "" && has(names, m.Checkpoint) {
					out[m.Checkpoint] = m.MaxContextWindow
				}
			}
			return out
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
				return nil, fmt.Errorf("health:%s", err)
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

// lmStudioModel is one entry of LM Studio's GET /api/v0/models listing
// (lmstudio.ai/docs/developer/rest/endpoints).
type lmStudioModel struct {
	ID               string `json:"id"`
	Type             string `json:"type"`
	State            string `json:"state"`
	MaxContextLength int64  `json:"max_context_length"`
}

func LMStudio(base string) *Host {
	list := func(h *Host) ([]lmStudioModel, error) {
		var d struct {
			Data []lmStudioModel `json:"data"`
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
				if lmStudioProfiles(m.Type) != nil {
					out = append(out, m.ID)
				}
			}
			return out, nil
		},
		modelProfiles: func(h *Host, names []string) map[string][]string {
			d, err := list(h)
			if err != nil {
				return nil
			}
			out := map[string][]string{}
			for _, m := range d {
				if p := lmStudioProfiles(m.Type); p != nil && has(names, m.ID) {
					out[m.ID] = p
				}
			}
			return out
		},
		modelContextLength: func(h *Host, names []string) map[string]int64 {
			d, err := list(h)
			if err != nil {
				return nil
			}
			out := map[string]int64{}
			for _, m := range d {
				if m.MaxContextLength > 0 && has(names, m.ID) {
					out[m.ID] = m.MaxContextLength
				}
			}
			return out
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
		},
		unload: unloadLMStudio}
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
	show := &ollamaShow{}
	return &Host{Name: "ollama", Base: base, Chat: "/v1/chat/completions",
		installed:          func(h *Host) ([]string, error) { return names(h.Base + "/api/tags") },
		resident:           func(h *Host) ([]string, error) { return names(h.Base + "/api/ps") },
		modelProfiles:      show.profiles,
		modelContextLength: show.contextLength,
		unload:             unloadOllama}
}

// WhisperCPP is one explicitly configured whisper.cpp server. Its process
// owns one model, addressed as whisper, and accepts WAV at /inference.
func WhisperCPP(base string) *Host {
	return &Host{Name: "whispercpp", Base: strings.TrimRight(base, "/"), Chat: "/inference", Profiles: []string{ProfileTranscription},
		installed: func(*Host) ([]string, error) { return []string{"whisper"}, nil },
		resident:  func(*Host) ([]string, error) { return []string{"whisper"}, nil }}
}

// Piper is one explicitly configured official Piper HTTP server. It owns one
// synthesis model and emits WAV from POST /synthesize.
func Piper(base string) *Host {
	return &Host{Name: "piper", Base: strings.TrimRight(base, "/"), Chat: "/synthesize", Profiles: []string{ProfileSpeech},
		installed: func(*Host) ([]string, error) { return []string{"piper"}, nil },
		resident:  func(*Host) ([]string, error) { return []string{"piper"}, nil }}
}

// ComfyUI runs a graph, not a model, so no OpenAI-compatible endpoint can serve
// what it holds and Chat stays empty. It is here because a quarter of this
// machine's weights are in its tree and an inventory that omits them is wrong.
func ComfyUI(base string) *Host {
	return &Host{Name: "comfyui", Base: strings.TrimRight(base, "/"), Chat: "/prompt", Profiles: []string{ProfileImage},
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
		resident: comfyResident,
		unload:   unloadComfyUI}
}

// comfySystemStats is the shape of GET /system_stats (ComfyUI's own
// server.py, routes.get("/system_stats")): one entry per torch device it
// enumerates, primary device first, with vram_total and vram_free in bytes.
// No stock route names the checkpoint behind a hold — /system_stats reports
// devices, not weights, and neither does /prompt or /queue — so a render
// underway is a hold with an amount and no name to give it.
type comfySystemStats struct {
	Devices []struct {
		VRAMTotal int64 `json:"vram_total"`
		VRAMFree  int64 `json:"vram_free"`
	} `json:"devices"`
}

// comfyResident sums vram_total-vram_free over every device /system_stats
// reports and reads that as ComfyUI's hold, the way the instrument reads a
// process (research/resources/COMFY-CARD-2026-09-22.md section 1: the same
// figure a render's own weights and allocator cache show up as). Below
// instrument.Threshold the reading is the machine's own idle baseline, the
// same floor the instrument applies to every other process, and reporting it
// here would make attachedHost.Holding (serve/runtime_resources.go) find
// ComfyUI a holder worth asking every time it is merely up. At or above it,
// resident carries one entry, named loaded since no route gives the
// checkpoint a name, with the amount /system_stats measured: the one figure
// this host's reading actually carries, unlike LM Studio's and Ollama's own
// resident model lists (CONTRACT.md RES-T3 is about those; this reading has
// no model name to report in its place).
func comfyResident(h *Host) ([]string, error) {
	var stats comfySystemStats
	if err := fetch(h.Base+"/system_stats", &stats); err != nil {
		return nil, err
	}
	var held int64
	for _, d := range stats.Devices {
		if used := d.VRAMTotal - d.VRAMFree; used > 0 {
			held += used
		}
	}
	if held < instrument.Threshold {
		return nil, nil
	}
	return []string{fmt.Sprintf("loaded@%d", held)}, nil
}

// SwarmUI is one local generation host using its documented T2I API.
func SwarmUI(base string) *Host {
	return &Host{Name: "swarmui", Base: strings.TrimRight(base, "/"), Chat: "/API/GenerateText2Image", Profiles: []string{ProfileImage},
		installed: func(*Host) ([]string, error) { return []string{"swarmui"}, nil },
		resident:  func(*Host) ([]string, error) { return []string{"swarmui"}, nil }}
}

func has(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}

// openAILocal is a local server that speaks OpenAI chat completions under
// api and lists its models at api+"/models".
func openAILocal(name, base, api string) *Host {
	return &Host{Name: name, Base: strings.TrimRight(base, "/"), Chat: api + "/chat/completions",
		installed: func(h *Host) ([]string, error) {
			var d struct {
				Data []struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			if err := fetch(h.Base+api+"/models", &d); err != nil {
				return nil, err
			}
			var out []string
			for _, m := range d.Data {
				if m.ID != "" {
					out = append(out, m.ID)
				}
			}
			return out, nil
		},
		resident: func(*Host) ([]string, error) { return nil, nil }}
}

// OpenAICompatible is a local server speaking OpenAI chat completions under
// api. models is what it serves, for a server whose list is known before it
// answers — a process another program starts for one file and stops again, so
// asking it what it holds would start it. An empty models reads the list from
// api+"/models" like any other local server.
func OpenAICompatible(name, base, api string, models []string) *Host {
	h := openAILocal(name, base, api)
	if len(models) > 0 {
		listed := append([]string(nil), models...)
		h.installed = func(*Host) ([]string, error) { return append([]string(nil), listed...), nil }
	}
	return h
}

// DockerModelRunner is Docker Model Runner on its host TCP port; its
// OpenAI-compatible API is under /engines/v1.
func DockerModelRunner(base string) *Host {
	return openAILocal("docker-model-runner", base, "/engines/v1")
}

// FoundryLocal is Microsoft Foundry Local at the address its service reports;
// its OpenAI-compatible API is under /v1.
func FoundryLocal(base string) *Host {
	return openAILocal("foundry-local", base, "/v1")
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
