package router

import (
	"time"

	identity "github.com/openabstractions/abstraction-identity"
	"github.com/openabstractions/abstraction-identity/listen"
)

const (
	OpModels    = "models"
	OpResidency = "residency"
	OpRoute     = "route"
)

// Bound is what this service requires of a caller before it will answer for it.
// Path is only ProofBound anywhere: Windows cannot verify the code a running
// process is executing, and Linux has no answer at all below 6.5.
var Bound = identity.Need{
	User:    identity.ProofKernel,
	Process: identity.ProofKernel,
	Path:    identity.ProofBound,
}

type Request struct {
	Op    string `json:"op"`
	Model string `json:"model,omitempty"`
	Fresh bool   `json:"fresh,omitempty"`
	// Hosts is the set of hosts this request authorises to serve it. The key
	// absent means every servable host; an empty list means none, and refuses.
	// The pointer is what keeps those two apart: a plain slice reads an empty
	// authorisation as no authorisation at all, which turns a caller's hard
	// requirement into a preference nothing observes.
	Hosts *[]string `json:"hosts,omitempty"`
}

type Response struct {
	Code     string      `json:"code,omitempty"`
	Error    string      `json:"error,omitempty"`
	Caller   listen.Seen `json:"caller"`
	TookMS   int64       `json:"took_ms"`
	AgeMS    int64       `json:"cache_age_ms"`
	Models   []Family    `json:"models,omitempty"`
	Hosts    []HostState `json:"hosts,omitempty"`
	GPU      []Holder    `json:"gpu,omitempty"`
	GPUWhy   string      `json:"gpu_unreadable,omitempty"`
	Doubled  []string    `json:"doubled,omitempty"`
	Asked    []Ask       `json:"asked,omitempty"`
	Decision *Decision   `json:"decision,omitempty"`
}

// Family is one model under every name this machine knows it by.
type Family struct {
	Family string  `json:"family"`
	Names  []Alias `json:"names"`
}

type Alias struct {
	Host     string `json:"host"`
	Name     string `json:"name"`
	Resident bool   `json:"resident"`
	Servable bool   `json:"servable"`
}

type HostState struct {
	Host      string   `json:"host"`
	Base      string   `json:"base"`
	Up        bool     `json:"up"`
	Why       string   `json:"why,omitempty"`
	Installed int      `json:"installed"`
	Resident  []string `json:"resident,omitempty"`
	Servable  bool     `json:"servable"`
}

// Holder is per-process, which is as fine as this machine can attribute GPU
// memory: Lemonade spawns one llama-server child per model and holds its pid,
// so a host's own number is neither the model's nor the host's.
type Holder struct {
	Process string  `json:"process"`
	PID     int     `json:"pid"`
	GiB     float64 `json:"gib"`
}

type Ask struct {
	At      time.Time `json:"at"`
	Caller  string    `json:"caller"`
	User    string    `json:"user"`
	Model   string    `json:"model"`
	Family  string    `json:"family"`
	Verdict string    `json:"verdict"`
	Host    string    `json:"host,omitempty"`
}

const (
	Resident     = "resident"
	WouldLoad    = "would-load"
	Unservable   = "unservable"
	NotOnDisk    = "not-here"
	EmptyFamily  = "unparseable"
	Unauthorised = "unauthorised"
)

type Decision struct {
	Asked       string    `json:"asked"`
	Family      string    `json:"family"`
	Verdict     string    `json:"verdict"`
	Host        string    `json:"host,omitempty"`
	Model       string    `json:"model,omitempty"`
	Endpoint    string    `json:"endpoint,omitempty"`
	InstalledOn []string  `json:"installed_on,omitempty"`
	Authorised  *[]string `json:"authorised,omitempty"`
	Withheld    []string  `json:"withheld,omitempty"`
	Loads       int       `json:"loads"`
	Why         string    `json:"why"`
}
