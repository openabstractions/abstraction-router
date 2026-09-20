package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Products that declare where their local server listens, the declared_by
// word of a host a probe found. The router reads each product's own record of
// its address and never scans a port, listens for multicast or guesses a port
// the product did not record (research/inference-registration/DECISION.md §1).
const (
	DeclaredByOllama            = "ollama"
	DeclaredByLMStudio          = "lmstudio"
	DeclaredByDockerModelRunner = "docker-model-runner"
	DeclaredByFoundryLocal      = "foundry-local"
	// DeclaredByDefault marks a host at the router's built-in address for a
	// product that records none (Lemonade, ComfyUI).
	DeclaredByDefault = "default"
)

// Default addresses the products document when their record is absent.
const (
	OllamaDefaultBase            = "http://127.0.0.1:11434"
	LMStudioDefaultBase          = "http://127.0.0.1:1234"
	DockerModelRunnerDefaultBase = "http://127.0.0.1:12434"
	LemonadeDefaultBase          = "http://127.0.0.1:13305"
	ComfyUIDefaultBase           = "http://127.0.0.1:8188"
)

// ProbeEnv is what a probe may read. Every field is explicit so a test reads
// fixtures and a runtime reads the account it runs as.
type ProbeEnv struct {
	// GOOS selects per-platform paths; empty reads as linux.
	GOOS string
	// Home is the account's home directory; empty skips every file probe.
	Home string
	// AppData is %APPDATA% on Windows; empty skips Docker Desktop there.
	AppData string
	// Getenv reads an environment variable; nil reads none.
	Getenv func(string) string
	// ReadFile reads a product's record; nil reads no file.
	ReadFile func(string) ([]byte, error)
	// Stat reports whether a file exists; nil reports none.
	Stat func(string) bool
	// Status runs a product's own status command and returns its output; nil
	// runs nothing. Foundry Local records its dynamic port only there.
	Status func(ctx context.Context, program string, args ...string) ([]byte, error)
	// Log receives the reason a probe fell back to a product's default.
	Log func(product, reason string)
}

// Declaration is one address a product recorded, or its documented default.
type Declaration struct {
	// Product is the declared_by word.
	Product string
	// Base is the address the router reaches the product at.
	Base string
	// Source names where Base came from: an environment variable, a file, a
	// command, or "default".
	Source string
	// Reason is why a present record was not used, or "".
	Reason string
}

func (e ProbeEnv) getenv(name string) string {
	if e.Getenv == nil {
		return ""
	}
	return e.Getenv(name)
}

func (e ProbeEnv) log(product, reason string) {
	if e.Log != nil && reason != "" {
		e.Log(product, reason)
	}
}

func (e ProbeEnv) join(parts ...string) string {
	if e.GOOS == "windows" {
		return strings.Join(parts, `\`)
	}
	return filepath.ToSlash(filepath.Join(parts...))
}

// ProbeOllama reads OLLAMA_HOST the way Ollama's own client does
// (github.com/ollama/ollama envconfig/config.go Host and ConnectableHost):
// scheme defaults to http, host to 127.0.0.1, and port to 11434, or 80 or 443
// when a scheme is given without a port. An unspecified bind address (0.0.0.0,
// ::) is reached at loopback. An unset variable is Ollama's default address; a
// value that does not parse is the default with a reason.
func ProbeOllama(env ProbeEnv) Declaration {
	d := Declaration{Product: DeclaredByOllama, Base: OllamaDefaultBase, Source: "default"}
	raw := strings.TrimSpace(env.getenv("OLLAMA_HOST"))
	if raw == "" {
		return d
	}
	base, err := ollamaHost(raw)
	if err != nil {
		d.Reason = "OLLAMA_HOST " + strconv.Quote(raw) + ": " + err.Error()
		env.log(d.Product, d.Reason)
		return d
	}
	d.Base, d.Source = base, "env:OLLAMA_HOST"
	return d
}

func ollamaHost(raw string) (string, error) {
	scheme, hostport, found := strings.Cut(raw, "://")
	defaultPort := "11434"
	if !found {
		scheme, hostport = "http", raw
	} else {
		switch scheme {
		case "http":
			defaultPort = "80"
		case "https":
			defaultPort = "443"
		default:
			return "", fmt.Errorf("scheme %q is neither http nor https", scheme)
		}
	}
	hostport, path, _ := strings.Cut(hostport, "/")
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		host, port = strings.Trim(hostport, "[]"), defaultPort
		if host == "" {
			host = "127.0.0.1"
		}
	}
	if host == "" {
		host = "127.0.0.1"
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("port %q is not 1..65535", port)
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		host = "127.0.0.1"
		if ip.To4() == nil {
			host = "::1"
		}
	} else if ip == nil && strings.ContainsAny(host, " /?#@") {
		return "", fmt.Errorf("host %q is not a host name", host)
	}
	u := url.URL{Scheme: scheme, Host: net.JoinHostPort(host, port)}
	if path != "" {
		u.Path = "/" + strings.TrimRight(path, "/")
	}
	return u.String(), nil
}

// LMStudioServerConfigs are the files LM Studio records its server's port and
// interface in, newest layout first: ~/.lmstudio/.internal/http-server-config.json
// (lmstudio-ai/lms#314) and ~/.cache/lm-studio/.internal/http-server-config.json
// (lmstudio-ai/lms#73), under the home directory on every platform.
func LMStudioServerConfigs(env ProbeEnv) []string {
	if env.Home == "" {
		return nil
	}
	return []string{
		env.join(env.Home, ".lmstudio", ".internal", "http-server-config.json"),
		env.join(env.Home, ".cache", "lm-studio", ".internal", "http-server-config.json"),
	}
}

// ProbeLMStudio reads the first LM Studio server configuration present: its
// port, and its networkInterface when that names one address other than an
// unspecified one. A missing file is LM Studio's default port 1234; a
// malformed one is the default with a reason.
func ProbeLMStudio(env ProbeEnv) Declaration {
	d := Declaration{Product: DeclaredByLMStudio, Base: LMStudioDefaultBase, Source: "default"}
	if env.ReadFile == nil {
		return d
	}
	for _, path := range LMStudioServerConfigs(env) {
		raw, err := env.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			d.Reason = path + ": " + err.Error()
			env.log(d.Product, d.Reason)
			return d
		}
		var config struct {
			Port             *int   `json:"port"`
			NetworkInterface string `json:"networkInterface"`
		}
		if err := json.Unmarshal(raw, &config); err != nil {
			d.Reason = path + ": " + err.Error()
			env.log(d.Product, d.Reason)
			return d
		}
		if config.Port == nil || *config.Port < 1 || *config.Port > 65535 {
			d.Reason = path + ": no port 1..65535"
			env.log(d.Product, d.Reason)
			return d
		}
		host := "127.0.0.1"
		if ip := net.ParseIP(config.NetworkInterface); ip != nil && !ip.IsUnspecified() {
			host = ip.String()
		}
		d.Base, d.Source = "http://"+net.JoinHostPort(host, strconv.Itoa(*config.Port)), path
		return d
	}
	return d
}

// DockerDesktopSettings is Docker Desktop's settings store, where its "Enable
// host-side TCP support" switch for Docker Model Runner is kept as
// EnableInferenceTCP (docs.docker.com/ai/model-runner; the key names are
// quoted in docker/desktop-feedback#460): %APPDATA%\Docker on Windows,
// ~/Library/Group Containers/group.com.docker on macOS, ~/.docker/desktop on
// Linux.
func DockerDesktopSettings(env ProbeEnv) string {
	switch env.GOOS {
	case "windows":
		if env.AppData == "" {
			return ""
		}
		return env.join(env.AppData, "Docker", "settings-store.json")
	case "darwin":
		if env.Home == "" {
			return ""
		}
		return env.join(env.Home, "Library", "Group Containers", "group.com.docker", "settings-store.json")
	}
	if env.Home == "" {
		return ""
	}
	return env.join(env.Home, ".docker", "desktop", "settings-store.json")
}

// DockerModelPlugins are the Docker CLI plugin files of Docker Engine's
// docker-model-plugin package, which serves Model Runner on TCP 12434 by
// default on Linux (docs.docker.com/ai/model-runner).
func DockerModelPlugins(env ProbeEnv) []string {
	if env.GOOS != "" && env.GOOS != "linux" {
		return nil
	}
	paths := []string{"/usr/libexec/docker/cli-plugins/docker-model", "/usr/lib/docker/cli-plugins/docker-model"}
	if env.Home != "" {
		paths = append(paths, env.join(env.Home, ".docker", "cli-plugins", "docker-model"))
	}
	return paths
}

// ProbeDockerModelRunner declares Model Runner on host TCP 12434 when Docker
// Desktop's settings store enables host-side TCP, or, on Linux without Docker
// Desktop, when Docker Engine's model plugin is installed. Without either
// record nothing is declared, because Desktop serves no host port unless the
// person enabled it. The settings store names no port key, so the port is the
// documented 12434.
func ProbeDockerModelRunner(env ProbeEnv) (Declaration, bool) {
	d := Declaration{Product: DeclaredByDockerModelRunner, Base: DockerModelRunnerDefaultBase}
	if path := DockerDesktopSettings(env); path != "" && env.ReadFile != nil {
		raw, err := env.ReadFile(path)
		switch {
		case err == nil:
			var settings struct {
				EnableInferenceTCP *bool `json:"EnableInferenceTCP"`
				EnableDockerAI     *bool `json:"EnableDockerAI"`
			}
			if err := json.Unmarshal(raw, &settings); err != nil {
				d.Reason = path + ": " + err.Error()
				env.log(d.Product, d.Reason)
				return d, false
			}
			if settings.EnableDockerAI != nil && !*settings.EnableDockerAI || settings.EnableInferenceTCP == nil || !*settings.EnableInferenceTCP {
				return d, false
			}
			d.Source = path
			return d, true
		case !errors.Is(err, fs.ErrNotExist):
			d.Reason = path + ": " + err.Error()
			env.log(d.Product, d.Reason)
			return d, false
		}
	}
	if env.Stat != nil {
		for _, path := range DockerModelPlugins(env) {
			if env.Stat(path) {
				d.Source = path
				return d, true
			}
		}
	}
	return d, false
}

// foundryStatus matches the service line of `foundry service status`, for
// example "Model management service is running on
// http://127.0.0.1:52403/openai/status" (microsoft/Foundry-Local#286, #424).
var foundryStatus = regexp.MustCompile(`service is running on (https?)://(127\.0\.0\.1|localhost|\[::1\]):([0-9]{1,5})/`)

// ProbeFoundryLocal reads the address Foundry Local's own status command
// reports. The service picks a new port at each start and records it in no
// file a client reads: its SDK asks the native service library, and the CLI
// prints it (learn.microsoft.com/azure/foundry-local/reference/reference-rest).
// A stopped service, a missing command or unrecognised output declares
// nothing.
func ProbeFoundryLocal(env ProbeEnv) (Declaration, bool) {
	d := Declaration{Product: DeclaredByFoundryLocal}
	if env.Status == nil {
		return d, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := env.Status(ctx, "foundry", "service", "status")
	if err != nil && len(out) == 0 {
		return d, false
	}
	m := foundryStatus.FindSubmatch(out)
	if m == nil {
		if strings.Contains(string(out), "is running") {
			d.Reason = "foundry service status: no loopback address in its output"
			env.log(d.Product, d.Reason)
		}
		return d, false
	}
	port, _ := strconv.Atoi(string(m[3]))
	if port < 1 || port > 65535 {
		d.Reason = "foundry service status: port out of range"
		env.log(d.Product, d.Reason)
		return d, false
	}
	d.Base, d.Source = string(m[1])+"://127.0.0.1:"+strconv.Itoa(port), "foundry service status"
	return d, true
}

// Probe runs every product probe once and returns what each product declared,
// in the order a decision prefers them.
func Probe(env ProbeEnv) []Declaration {
	out := []Declaration{
		{Product: DeclaredByDefault, Base: LemonadeDefaultBase, Source: "default"},
		ProbeLMStudio(env),
		ProbeOllama(env),
	}
	if d, ok := ProbeDockerModelRunner(env); ok {
		out = append(out, d)
	}
	if d, ok := ProbeFoundryLocal(env); ok {
		out = append(out, d)
	}
	return out
}

// Declared is the hosts the products on this machine declare, in preference
// order: Lemonade and ComfyUI at their built-in addresses, and LM Studio,
// Ollama, Docker Model Runner and Foundry Local where their records say.
func Declared(env ProbeEnv) []*Host {
	var hosts []*Host
	for i, d := range Probe(env) {
		var h *Host
		switch d.Product {
		case DeclaredByLMStudio:
			h = LMStudio(d.Base)
		case DeclaredByOllama:
			h = Ollama(d.Base)
		case DeclaredByDockerModelRunner:
			h = DockerModelRunner(d.Base)
		case DeclaredByFoundryLocal:
			h = FoundryLocal(d.Base)
		default:
			if i != 0 {
				continue
			}
			h = Lemonade(d.Base)
		}
		h.DeclaredBy = d.Product
		hosts = append(hosts, h)
	}
	comfy := ComfyUI(ComfyUIDefaultBase)
	comfy.DeclaredBy = DeclaredByDefault
	return append(hosts, comfy)
}
