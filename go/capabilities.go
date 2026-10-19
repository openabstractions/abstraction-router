package router

import (
	"context"
	"slices"
	"strconv"
	"strings"

	wire "github.com/openabstractions/abstraction-router/go/abstraction/router"
	"github.com/openabstractions/abstraction-router/go/hostedcatalogue"
)

// ModelCapabilities is what one alias on its host takes and gives (Alias
// capabilities, architect ruling 2026-11-12). It is per host and name: the
// same family on two hosts carries two values, never a merged one.
type ModelCapabilities = wire.ModelCapabilities

// ReasoningSupport is the reasoning controls one alias takes.
type ReasoningSupport = wire.ReasoningSupport

// ReasoningEffort is one chat@3 reasoning effort word.
type ReasoningEffort = wire.ReasoningEffort

// Reasoning control kinds: how a model takes chat@3 reasoning on its wire.
const (
	// ReasoningEffortField sends Values[effort] as OpenAI-compatible
	// reasoning_effort. An effort whose value is another word is
	// approximated; an effort with no value is not sent.
	ReasoningEffortField = "reasoning_effort"
	// ReasoningThinkingBudget sends an Anthropic thinking budget of
	// Budgets[effort] tokens; none disables thinking.
	ReasoningThinkingBudget = "thinking.budget_tokens"
	// ReasoningLlamaCpp sends reasoning_effort and, for a budget,
	// reasoning_budget_tokens to a llama.cpp engine behind modelhost, which
	// turns none into thinking off and every other effort into a bounded
	// budget (modelhost reserveAnswer).
	ReasoningLlamaCpp = "llama.cpp"
)

// ReasoningControl is how one model takes chat@3 reasoning: catalogue or
// host data, never guessed. Default is the effort the model reasons at when
// a request names none.
type ReasoningControl struct {
	Kind    string
	Values  map[string]string
	Budgets map[string]int64
	Default string
}

// Extension names a llama.cpp engine behind modelhost reads natively.
const (
	ExtensionLlamaCppEnableThinking  = "llama.cpp/enable_thinking"
	ExtensionLlamaCppReasoningBudget = "llama.cpp/reasoning_budget"
)

var reasoningEfforts = []string{"none", "low", "medium", "high"}

// Support is the ReasoningSupport a control advertises: every effort it
// takes, approximated ones included, whether it takes a budget, and its
// default effort.
func (c ReasoningControl) Support() *ReasoningSupport {
	s := &ReasoningSupport{Efforts: []ReasoningEffort{}}
	for _, effort := range reasoningEfforts {
		take := false
		switch c.Kind {
		case ReasoningEffortField:
			take = c.Values[effort] != ""
		case ReasoningThinkingBudget:
			take = effort == "none" || c.Budgets[effort] > 0
		case ReasoningLlamaCpp:
			take = effort == "none" || c.Budgets[effort] > 0
		}
		if take {
			s.Efforts = append(s.Efforts, ReasoningEffort(effort))
		}
	}
	s.Budget = c.Kind == ReasoningLlamaCpp || c.Kind == ReasoningThinkingBudget
	if c.Default != "" {
		d := ReasoningEffort(c.Default)
		s.DefaultEffort = &d
	}
	return s
}

// DeclareModel states one model's capabilities and reasoning control on
// h, as the host's owner knows them (modelhost: its engine profile and the
// engine's own facts). Declared facts are this host's catalogue for that
// model and outrank anything the survey reads. Call it before the host is
// shared with a router.
func (h *Host) DeclareModel(name string, capabilities *ModelCapabilities, reasoning *ReasoningControl) {
	if h.capabilities == nil {
		h.capabilities = map[string]*ModelCapabilities{}
	}
	if h.reasoning == nil {
		h.reasoning = map[string]ReasoningControl{}
	}
	if capabilities != nil {
		c := cloneCapabilities(capabilities)
		h.capabilities[name] = c
	}
	if reasoning != nil {
		h.reasoning[name] = *reasoning
		if c := h.capabilities[name]; c != nil && c.Reasoning == nil {
			c.Reasoning = reasoning.Support()
		} else if c == nil {
			h.capabilities[name] = &ModelCapabilities{Reasoning: reasoning.Support()}
		}
	}
}

// ReasoningFor is how model takes reasoning on h: the host's own
// declaration, else the hosted catalogue entry for the model. False when
// neither states one; such a model gets no reasoning parameter.
func (h *Host) ReasoningFor(model string) (ReasoningControl, bool) {
	if h == nil {
		return ReasoningControl{}, false
	}
	if c, ok := h.reasoning[model]; ok {
		return c, true
	}
	m, ok := h.catalogueModel(model)
	if !ok || m.Reasoning == nil {
		return ReasoningControl{}, false
	}
	return controlOf(*m.Reasoning), true
}

// CapabilitiesFor is model's catalogue capabilities on h: the host's own
// declaration, else the hosted catalogue entry. Nil when neither states any.
func (h *Host) CapabilitiesFor(model string) *ModelCapabilities {
	if h == nil {
		return nil
	}
	if c := h.capabilities[model]; c != nil {
		return cloneCapabilities(c)
	}
	m, ok := h.catalogueModel(model)
	if !ok {
		return nil
	}
	return catalogueCapabilities(m)
}

func (h *Host) catalogueModel(model string) (hostedcatalogue.Model, bool) {
	if !h.Hosted || h.Base == "" {
		return hostedcatalogue.Model{}, false
	}
	return hostedcatalogue.ModelFor(h.Base, h.Wire, model)
}

func controlOf(r hostedcatalogue.Reasoning) ReasoningControl {
	c := ReasoningControl{Values: r.Values, Budgets: r.Budgets, Default: r.Default}
	switch r.Parameter {
	case hostedcatalogue.ReasoningEffortParameter:
		c.Kind = ReasoningEffortField
	case hostedcatalogue.ThinkingBudgetParameter:
		c.Kind = ReasoningThinkingBudget
	}
	return c
}

func catalogueCapabilities(m hostedcatalogue.Model) *ModelCapabilities {
	c := &ModelCapabilities{Input: slices.Clone(m.Input), Output: slices.Clone(m.Output), Extensions: slices.Clone(m.Extensions)}
	if m.Tools != nil {
		v := *m.Tools
		c.Tools = &v
	}
	if m.StructuredOutput != nil {
		v := *m.StructuredOutput
		c.StructuredOutput = &v
	}
	if m.Reasoning != nil {
		c.Reasoning = controlOf(*m.Reasoning).Support()
	}
	return c
}

func cloneCapabilities(c *ModelCapabilities) *ModelCapabilities {
	if c == nil {
		return nil
	}
	out := &ModelCapabilities{Input: slices.Clone(c.Input), Output: slices.Clone(c.Output), Extensions: slices.Clone(c.Extensions)}
	if c.Tools != nil {
		v := *c.Tools
		out.Tools = &v
	}
	if c.StructuredOutput != nil {
		v := *c.StructuredOutput
		out.StructuredOutput = &v
	}
	if r := c.Reasoning; r != nil {
		out.Reasoning = &ReasoningSupport{Efforts: slices.Clone(r.Efforts), Budget: r.Budget}
		if r.DefaultEffort != nil {
			d := *r.DefaultEffort
			out.Reasoning.DefaultEffort = &d
		}
	}
	return out
}

// mergeCapabilities takes each fact from the first source that states it, in
// the ruling's order: catalogue, engine facts, then the hosted listing. Nil
// when no source states anything.
func mergeCapabilities(sources ...*ModelCapabilities) *ModelCapabilities {
	var out *ModelCapabilities
	for _, s := range sources {
		if s == nil {
			continue
		}
		if out == nil {
			out = &ModelCapabilities{}
		}
		if len(out.Input) == 0 {
			out.Input = slices.Clone(s.Input)
		}
		if len(out.Output) == 0 {
			out.Output = slices.Clone(s.Output)
		}
		if out.Tools == nil && s.Tools != nil {
			v := *s.Tools
			out.Tools = &v
		}
		if out.StructuredOutput == nil && s.StructuredOutput != nil {
			v := *s.StructuredOutput
			out.StructuredOutput = &v
		}
		if out.Reasoning == nil && s.Reasoning != nil {
			out.Reasoning = cloneCapabilities(&ModelCapabilities{Reasoning: s.Reasoning}).Reasoning
		}
		if len(out.Extensions) == 0 {
			out.Extensions = slices.Clone(s.Extensions)
		}
	}
	if out != nil && len(out.Input) == 0 && len(out.Output) == 0 && out.Tools == nil && out.StructuredOutput == nil && out.Reasoning == nil && len(out.Extensions) == 0 {
		return nil
	}
	return out
}

// listedCapabilities are the facts an OpenAI-compatible model listing
// publishes for one model: its input and output modalities, and tools and
// structured output from its supported parameters (OpenRouter's listing).
func (m listedModel) capabilities() *ModelCapabilities {
	c := &ModelCapabilities{}
	known := func(list []string) []string {
		out := []string{}
		for _, modality := range list {
			if slices.Contains(capabilityModalities, modality) && !slices.Contains(out, modality) {
				out = append(out, modality)
			}
		}
		return out
	}
	input, output := m.InputModalities, m.OutputModalities
	if m.Architecture != nil {
		if len(input) == 0 {
			input = m.Architecture.InputModalities
		}
		if len(output) == 0 {
			output = m.Architecture.OutputModalities
		}
	}
	c.Input, c.Output = known(input), known(output)
	if len(m.SupportedParameters) > 0 {
		tools := slices.Contains(m.SupportedParameters, "tools")
		structured := slices.Contains(m.SupportedParameters, "structured_outputs")
		c.Tools, c.StructuredOutput = &tools, &structured
	}
	return mergeCapabilities(c)
}

var capabilityModalities = []string{"text", "image", "audio", "video"}

// Capability facts travel in a native provider's chat@3 endpoint description
// as display facts: "model.<n>" names a model and "model.<n>.capabilities"
// its facts, n from 1, at most capabilityFactLimit models. The value is
// ";"-separated fields: in=<modality>+..., out=..., tools=0|1, json=0|1,
// reasoning=<effort>,..., budget=1, default=<effort>, ext=<name>,... A
// field absent is a fact unstated.
const (
	capabilityFactPrefix = "model."
	capabilityFactSuffix = ".capabilities"
	capabilityFactLimit  = 15
	capabilityValueLimit = 256
)

// CapabilityFacts encodes caps for names, in order, as description facts.
// A model whose value would not fit one display value is left out.
func CapabilityFacts(names []string, caps map[string]*ModelCapabilities) map[string]string {
	out := map[string]string{}
	n := 0
	for _, name := range names {
		c := caps[name]
		if c == nil || name == "" || len(name) > capabilityValueLimit {
			continue
		}
		value := encodeCapabilities(c)
		if value == "" || len(value) > capabilityValueLimit {
			continue
		}
		if n == capabilityFactLimit {
			break
		}
		n++
		key := capabilityFactPrefix + strconv.Itoa(n)
		out[key] = name
		out[key+capabilityFactSuffix] = value
	}
	return out
}

// ParseCapabilityFacts reads CapabilityFacts back, by model name. A malformed
// field is dropped, never guessed (fail open for display data).
func ParseCapabilityFacts(facts map[string]string) map[string]*ModelCapabilities {
	out := map[string]*ModelCapabilities{}
	for n := 1; n <= capabilityFactLimit; n++ {
		key := capabilityFactPrefix + strconv.Itoa(n)
		name, value := facts[key], facts[key+capabilityFactSuffix]
		if name == "" || value == "" {
			continue
		}
		if c := decodeCapabilities(value); c != nil {
			out[name] = c
		}
	}
	return out
}

func encodeCapabilities(c *ModelCapabilities) string {
	var fields []string
	bit := func(v bool) string {
		if v {
			return "1"
		}
		return "0"
	}
	if len(c.Input) > 0 {
		fields = append(fields, "in="+strings.Join(c.Input, "+"))
	}
	if len(c.Output) > 0 {
		fields = append(fields, "out="+strings.Join(c.Output, "+"))
	}
	if c.Tools != nil {
		fields = append(fields, "tools="+bit(*c.Tools))
	}
	if c.StructuredOutput != nil {
		fields = append(fields, "json="+bit(*c.StructuredOutput))
	}
	if r := c.Reasoning; r != nil {
		words := make([]string, 0, len(r.Efforts))
		for _, e := range r.Efforts {
			words = append(words, string(e))
		}
		fields = append(fields, "reasoning="+strings.Join(words, ","))
		if r.Budget {
			fields = append(fields, "budget=1")
		}
		if r.DefaultEffort != nil {
			fields = append(fields, "default="+string(*r.DefaultEffort))
		}
	}
	if len(c.Extensions) > 0 {
		fields = append(fields, "ext="+strings.Join(c.Extensions, ","))
	}
	return strings.Join(fields, ";")
}

func decodeCapabilities(value string) *ModelCapabilities {
	c := &ModelCapabilities{}
	var reasoning *ReasoningSupport
	modalities := func(s string) []string {
		var out []string
		for _, m := range strings.Split(s, "+") {
			if slices.Contains(capabilityModalities, m) && !slices.Contains(out, m) {
				out = append(out, m)
			}
		}
		return out
	}
	for _, field := range strings.Split(value, ";") {
		key, v, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch key {
		case "in":
			c.Input = modalities(v)
		case "out":
			c.Output = modalities(v)
		case "tools", "json":
			if v != "0" && v != "1" {
				continue
			}
			b := v == "1"
			if key == "tools" {
				c.Tools = &b
			} else {
				c.StructuredOutput = &b
			}
		case "reasoning":
			reasoning = &ReasoningSupport{Efforts: []ReasoningEffort{}}
			for _, e := range strings.Split(v, ",") {
				if slices.Contains(reasoningEfforts, e) && !slices.Contains(reasoning.Efforts, ReasoningEffort(e)) {
					reasoning.Efforts = append(reasoning.Efforts, ReasoningEffort(e))
				}
			}
		case "ext":
			for _, name := range strings.Split(v, ",") {
				if name != "" && len(name) <= 128 && strings.Contains(name, "/") && !slices.Contains(c.Extensions, name) {
					c.Extensions = append(c.Extensions, name)
				}
			}
		}
	}
	if reasoning != nil {
		for _, field := range strings.Split(value, ";") {
			switch key, v, _ := strings.Cut(field, "="); key {
			case "budget":
				reasoning.Budget = v == "1"
			case "default":
				if slices.Contains(reasoningEfforts, v) {
					d := ReasoningEffort(v)
					reasoning.DefaultEffort = &d
				}
			}
		}
		c.Reasoning = reasoning
	}
	return mergeCapabilities(c)
}

// AliasCapabilities is the capabilities the latest survey states for name
// on host, without surveying; nil when it states none.
func (r *Router) AliasCapabilities(host, name string) *ModelCapabilities {
	families, _ := r.Models(false)
	for _, family := range families {
		for _, alias := range family.Names {
			if alias.Host == host && alias.Name == name {
				return cloneCapabilities(alias.Capabilities)
			}
		}
	}
	return nil
}

// ReportCapabilities sets how the survey reads the capability facts of h's
// names that its owner states outside the declaration, such as a native
// provider's chat@3 description. Call it before the host is shared with a
// router.
func (h *Host) ReportCapabilities(read func(ctx context.Context, names []string) map[string]*ModelCapabilities) {
	if read == nil {
		h.modelCapabilities = nil
		return
	}
	h.modelCapabilities = func(ctx context.Context, _ *Host, names []string) map[string]*ModelCapabilities {
		out := map[string]*ModelCapabilities{}
		for name, c := range read(ctx, names) {
			if c != nil && has(names, name) {
				out[name] = c
			}
		}
		return out
	}
}

// surveyedCapabilities are the capabilities of each of names on h, in the
// ruling's source order: h's declaration or the hosted catalogue, then the
// facts its owner reports, then the hosted listing.
func surveyedCapabilities(h *Host, names []string, reported map[string]*ModelCapabilities, listed map[string]*ModelCapabilities) map[string]*ModelCapabilities {
	out := map[string]*ModelCapabilities{}
	for _, name := range names {
		if c := mergeCapabilities(h.CapabilitiesFor(name), reported[name], listed[name]); c != nil {
			out[name] = c
		}
	}
	return out
}
