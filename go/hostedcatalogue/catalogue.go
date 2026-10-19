// Package hostedcatalogue owns static hosted-provider definitions used by the
// runtime's native preset selection and its Panel projection. A catalogue row
// grants no key access, spending permission or server registration.
package hostedcatalogue

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

//go:embed presets.json
var data []byte

type Preset struct {
	ID            string `json:"id"`
	Kind          string `json:"kind,omitempty"`
	Label         string `json:"label"`
	API           string `json:"api"`
	Base          string `json:"base"`
	Credential    string `json:"credential"`
	Budget        string `json:"budget"`
	KeyPage       string `json:"key_page"`
	KeyFormat     string `json:"key_format"`
	DefaultBudget int64  `json:"default_budget"`
	SignIn        bool   `json:"sign_in"`
	ReportsTokens bool   `json:"reports_tokens"`
	ReportsCost   bool   `json:"reports_cost"`
	// Profiles are the inference profiles the provider serves; a server
	// added from the preset registers only these. Empty keeps the wire's.
	Profiles []string `json:"profiles,omitempty"`
	// ModelContext is the context window, in tokens, the provider documents
	// for its models. The router uses it for a model whose /models entry
	// reports none; ModelContextSource says where the value comes from.
	ModelContext       int64  `json:"model_context,omitempty"`
	ModelContextSource string `json:"model_context_source,omitempty"`
	// Models is the provider's documented facts for each model it lists, by
	// the listing's own name; ModelsSource says where they come from. A
	// model the table does not name has no catalogue facts.
	Models       map[string]Model `json:"models,omitempty"`
	ModelsSource string           `json:"models_source,omitempty"`
}

// Model is one hosted model's documented capabilities. An absent field is a
// fact the provider does not document, and stays unknown.
type Model struct {
	Input            []string   `json:"input,omitempty"`
	Output           []string   `json:"output,omitempty"`
	Tools            *bool      `json:"tools,omitempty"`
	StructuredOutput *bool      `json:"structured_output,omitempty"`
	Reasoning        *Reasoning `json:"reasoning,omitempty"`
	Extensions       []string   `json:"extensions,omitempty"`
}

// Reasoning is how a hosted model takes abstraction.inference/chat@3
// reasoning. Parameter "reasoning_effort" sends Values[effort] as an
// OpenAI-compatible reasoning_effort; an effort whose value is another word
// is approximated. Parameter "thinking.budget_tokens" sends an Anthropic
// thinking budget of Budgets[effort] tokens, and "none" disables thinking.
// Default is the effort the provider reasons at when nothing is sent.
type Reasoning struct {
	Parameter string            `json:"parameter"`
	Values    map[string]string `json:"values,omitempty"`
	Budgets   map[string]int64  `json:"budgets,omitempty"`
	Default   string            `json:"default,omitempty"`
}

// ReasoningParameters are the wire controls a catalogue entry may name.
const (
	ReasoningEffortParameter = "reasoning_effort"
	ThinkingBudgetParameter  = "thinking.budget_tokens"
)

var efforts = []string{"none", "low", "medium", "high"}
var modalities = []string{"text", "image", "audio", "video"}

// ModelFor is the documented entry of model on the fixed preset whose address
// and wire are base and api, matched by exact name, then case-insensitively.
func ModelFor(base, api, model string) (Model, bool) {
	p, ok := PresetFor(base, api)
	if !ok {
		return Model{}, false
	}
	if m, ok := p.Models[model]; ok {
		return m, true
	}
	for name, m := range p.Models {
		if strings.EqualFold(name, model) {
			return m, true
		}
	}
	return Model{}, false
}

func validModel(api string, m Model) bool {
	for _, list := range [][]string{m.Input, m.Output} {
		for i, modality := range list {
			if !slices.Contains(modalities, modality) || slices.Contains(list[:i], modality) {
				return false
			}
		}
	}
	for i, name := range m.Extensions {
		if !extensionPattern.MatchString(name) || slices.Contains(m.Extensions[:i], name) {
			return false
		}
	}
	r := m.Reasoning
	if r == nil {
		return true
	}
	if r.Default != "" && !slices.Contains(efforts, r.Default) {
		return false
	}
	switch r.Parameter {
	case ReasoningEffortParameter:
		if len(r.Values) == 0 || len(r.Budgets) != 0 {
			return false
		}
		for effort, value := range r.Values {
			if !slices.Contains(efforts, effort) || value == "" || len(value) > 32 {
				return false
			}
		}
	case ThinkingBudgetParameter:
		if api != "anthropic-messages" || len(r.Budgets) == 0 || len(r.Values) != 0 {
			return false
		}
		for effort, budget := range r.Budgets {
			if !slices.Contains(efforts, effort) || effort == "none" || budget < 1024 || budget > 1<<20 {
				return false
			}
		}
	default:
		return false
	}
	return true
}

var extensionPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}/[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

// catalogueProfiles are the profiles a preset may declare (router SeedProfiles).
var catalogueProfiles = []string{"chat", "embed", "transcription", "speech", "image", "live"}

// PresetFor is the fixed model preset whose address and wire a hosted server
// declares, whatever its credential is named.
func PresetFor(base, api string) (Preset, bool) {
	rows, err := All()
	if err != nil {
		return Preset{}, false
	}
	for _, p := range rows {
		if p.Kind == "" && p.Base != "" && strings.TrimRight(p.Base, "/") == strings.TrimRight(base, "/") && p.API == api {
			return p, true
		}
	}
	return Preset{}, false
}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
var namePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// Data returns an independent projection. Callers cannot mutate the owner.
func Data() []byte           { return bytes.Clone(data) }
func All() ([]Preset, error) { return Load(data) }
func Lookup(id string) (Preset, bool) {
	rows, err := All()
	if err != nil {
		return Preset{}, false
	}
	for _, p := range rows {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}

// Binding is the fixed hosted-server selection. Native owners project these
// fields into existing HostEntry/ServerFacet records. Custom hosts retain
// their separate explicit configuration path.
type Binding struct{ Name, Base, Kind, Credential, Target string }

// ObservedUnits names units the fixed provider reports. Requests are counted
// by OA itself. A custom host never inherits a preset's reporting declaration.
type ObservedUnits struct{ Requests, Tokens, Micros bool }

func Observations(name, base, kind, credential string) ObservedUnits {
	units := ObservedUnits{Requests: true}
	binding, err := Select(name)
	if err != nil || binding.Base != base || binding.Kind != kind || binding.Credential != credential {
		return units
	}
	preset, _ := Lookup(name)
	units.Tokens, units.Micros = preset.ReportsTokens, preset.ReportsCost
	return units
}

func Select(id string) (Binding, error) {
	p, ok := Lookup(id)
	if !ok || p.Kind != "" || p.Base == "" || p.Credential == "" {
		return Binding{}, errors.New("hosted provider is not a fixed model preset")
	}
	target, err := Target(p.Base)
	if err != nil {
		return Binding{}, err
	}
	return Binding{Name: p.ID, Base: p.Base, Kind: p.API, Credential: p.Credential, Target: target}, nil
}

// Load validates the complete static definition; custom host registration
// retains its existing separate URL and credential owners.
func Load(raw []byte) ([]Preset, error) {
	var file struct {
		Presets []Preset `json:"presets"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&file); err != nil {
		return nil, fmt.Errorf("hosted presets: %w", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return nil, errors.New("hosted presets: trailing data")
	}
	seen := map[string]bool{}
	for _, p := range file.Presets {
		switch {
		case !idPattern.MatchString(p.ID) || seen[p.ID]:
			return nil, fmt.Errorf("hosted presets: id %q is malformed or repeated", p.ID)
		case strings.TrimSpace(p.Label) == "" || len(p.Label) > 64:
			return nil, fmt.Errorf("hosted presets: %s: label", p.ID)
		case p.Kind != "" && p.Kind != "download":
			return nil, fmt.Errorf("hosted presets: %s: kind", p.ID)
		case p.Kind == "download" && (p.API != "" || p.Budget != "" || p.Base == "" || p.Credential == ""):
			return nil, fmt.Errorf("hosted presets: %s: download credential", p.ID)
		case p.Kind != "download" && p.API != "openai-compatible" && p.API != "anthropic-messages":
			return nil, fmt.Errorf("hosted presets: %s: api", p.ID)
		case (p.Base == "") != (p.Credential == ""):
			return nil, fmt.Errorf("hosted presets: %s: address and credential must agree", p.ID)
		case p.Credential != "" && !namePattern.MatchString(p.Credential):
			return nil, fmt.Errorf("hosted presets: %s: credential", p.ID)
		case p.Kind != "download" && p.Budget != "money" && p.Budget != "tokens":
			return nil, fmt.Errorf("hosted presets: %s: budget", p.ID)
		case p.DefaultBudget < 0 || p.DefaultBudget > 1_000_000_000_000:
			return nil, fmt.Errorf("hosted presets: %s: default budget", p.ID)
		case p.KeyFormat != "" && p.KeyFormat != "opaque":
			return nil, fmt.Errorf("hosted presets: %s: key format", p.ID)
		case p.SignIn:
			return nil, fmt.Errorf("hosted presets: %s: sign-in has no implemented owner", p.ID)
		case (p.Base == "" || p.Kind == "download") && (p.ReportsTokens || p.ReportsCost):
			return nil, fmt.Errorf("hosted presets: %s: reporting requires a fixed model provider", p.ID)
		case p.ModelContext < 0 || p.ModelContext > 1<<30 || (p.ModelContext > 0) != (p.ModelContextSource != ""):
			return nil, fmt.Errorf("hosted presets: %s: model context", p.ID)
		case (len(p.Models) > 0) != (p.ModelsSource != "") || len(p.Models) > 0 && (p.Kind == "download" || p.Base == ""):
			return nil, fmt.Errorf("hosted presets: %s: models", p.ID)
		}
		for name, m := range p.Models {
			if !namePattern.MatchString(name) || !validModel(p.API, m) {
				return nil, fmt.Errorf("hosted presets: %s: model %s", p.ID, name)
			}
		}
		for i, profile := range p.Profiles {
			if p.Kind == "download" || !slices.Contains(catalogueProfiles, profile) || slices.Contains(p.Profiles[:i], profile) {
				return nil, fmt.Errorf("hosted presets: %s: profiles", p.ID)
			}
		}
		for _, address := range []string{p.Base, p.KeyPage} {
			if address != "" {
				if _, err := Target(address); err != nil {
					return nil, fmt.Errorf("hosted presets: %s: %w", p.ID, err)
				}
			}
		}
		seen[p.ID] = true
	}
	return file.Presets, nil
}

// Target resolves the credential's lowercase HTTPS host without normalizing a
// supplied address into different authority.
func Target(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(base) > 2048 || strings.ContainsFunc(u.Hostname(), unicode.IsUpper) {
		return "", errors.New("the address must be an https URL with a lowercase host name")
	}
	return u.Hostname(), nil
}

// AcceptsKey checks transport-safe opaque input only. Provider acceptance
// still requires the native Use.Check/listing operation before storage.
func (p Preset) AcceptsKey(key []byte) bool {
	return p.KeyFormat == "opaque" && len(key) > 0 && len(key) <= 16384 && utf8.Valid(key) && !strings.ContainsFunc(string(key), func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
}
