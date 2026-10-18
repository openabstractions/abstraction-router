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
	OpOffer     = "offer"
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
	// Profile is what the caller asks the host to serve (profiles.go); empty is
	// chat.
	Profile string `json:"profile,omitempty"`
	// Placement is which servers an offer counts (offer.go): PlacementLocal,
	// PlacementHosted or PlacementAny. Only OpOffer reads it.
	Placement string `json:"placement,omitempty"`
	// Reserve is the output budget in tokens an offer's caller keeps for the
	// answer. Only OpOffer reads it.
	Reserve int64 `json:"reserve,omitempty"`
}

type Response struct {
	Code       string      `json:"code,omitempty"`
	Error      string      `json:"error,omitempty"`
	Caller     listen.Seen `json:"caller"`
	TookMS     int64       `json:"took_ms"`
	AgeMS      int64       `json:"cache_age_ms"`
	Models     []Family    `json:"models,omitempty"`
	Hosts      []HostState `json:"hosts,omitempty"`
	Holders    []Holder    `json:"holders,omitempty"`
	HoldersWhy string      `json:"holders_unreadable,omitempty"`
	Doubled    []string    `json:"doubled,omitempty"`
	Asked      []Ask       `json:"asked,omitempty"`
	Decision   *Decision   `json:"decision,omitempty"`
	Offers     []Offer     `json:"offers,omitempty"`
}

// Offer is one family a caller may ask for a profile (ROUTE-D8): the name to
// ask for, the servers counted and the conservative context.
type Offer struct {
	Family string `json:"family"`
	// Names are the counted aliases in UTF-8 byte order; the first is the
	// name to ask for.
	Names []string `json:"names"`
	// Servers are the servers counted, in preference order; a remote
	// runtime's own servers read <remote>/<server>.
	Servers []string `json:"servers"`
	// Context is the smallest bound across the counted aliases, 0 when
	// ContextSource is ContextUnknown or ContextNone.
	Context       int64  `json:"context"`
	ContextSource string `json:"context_source"`
	// Input is Context minus the request's reserve, floored at 0.
	Input int64 `json:"input"`
	// Unknown names each counted alias whose server fixes its window at load
	// and reported no valid request limit, as <server>/<name>.
	Unknown []string `json:"unknown,omitempty"`
}

// Placements an offer request names.
const (
	PlacementLocal  = "local"
	PlacementHosted = "hosted"
	PlacementAny    = "any"
)

// Context sources an offer names.
const (
	ContextEffective = "effective"
	ContextMaximum   = "maximum"
	ContextDefault   = "default"
	ContextUnknown   = "unknown"
	ContextNone      = "none"
)

// Family is one model under every name this machine knows it by.
type Family struct {
	Family string  `json:"family"`
	Names  []Alias `json:"names"`
	// HeldIn names the storage inventory stores holding an object of this
	// family, whether or not a host serves it.
	HeldIn []string `json:"held_in,omitempty"`
	// FamilySource says where this family came from: FromDescriptor when the
	// storage inventory published an abstraction.model/descriptor@1 naming it,
	// FromAlias when nothing did and the name a program uses stands for the
	// model itself.
	FamilySource string `json:"family_source,omitempty"`
	// Components lists a projector, VAE or other role-bearing object a store
	// holds and this family's descriptor named as its base
	// (abstraction.model/descriptor@1 MODEL-C3), or, when no descriptor could
	// derive a base for it, the one component this family stands in for. A
	// component never appears as a family of its own; this is where it shows
	// up instead.
	Components []Component `json:"components,omitempty"`
}

// Component is one role-bearing object a store holds, folded into a
// family's detail rather than listed as a family of its own.
type Component struct {
	Store string `json:"store"`
	Role  string `json:"role"`
	// Name is the store's own name for the object, empty for an object no
	// index of that store named.
	Name string `json:"name,omitempty"`
}

const (
	// FromDescriptor: a store named this family in a model descriptor.
	FromDescriptor = "descriptor"
	// FromAlias: no descriptor names this model, so a program's own name for
	// it is the family.
	FromAlias = "alias"
)

type Alias struct {
	// Host is empty for a name no host serves: a store holds it and nothing
	// on this machine answers for it.
	Host     string `json:"host"`
	Name     string `json:"name"`
	Resident bool   `json:"resident"`
	Servable bool   `json:"servable"`
	Hosted   bool   `json:"hosted,omitempty"`
	// Profiles are the profiles the host's own metadata reports for this name;
	// empty when it reports none.
	Profiles []string `json:"profiles,omitempty"`
	// HeldIn names the stores holding an object this name or its digest
	// matches.
	HeldIn []string `json:"held_in,omitempty"`
	// ContextLength is the model's context window in tokens, read from the
	// host's own metadata without loading it; zero when the host reports
	// none.
	ContextLength int64 `json:"context_length,omitempty"`
	// EffectiveContextLength is the host-reported request limit: the loaded
	// value when resident or configured value otherwise. Zero means unknown.
	EffectiveContextLength int64 `json:"effective_context_length,omitempty"`
}

// HostState names a hosted host's wire and credential name; it never carries a
// header value.
type HostState struct {
	Host              string            `json:"host"`
	Base              string            `json:"base"`
	Up                bool              `json:"up"`
	Why               string            `json:"why,omitempty"`
	Installed         int               `json:"installed"`
	Resident          []string          `json:"resident,omitempty"`
	Servable          bool              `json:"servable"`
	Hosted            bool              `json:"hosted,omitempty"`
	Wire              string            `json:"wire,omitempty"`
	Credential        string            `json:"credential,omitempty"`
	DeclaredBy        string            `json:"declared_by,omitempty"`
	Profiles          []string          `json:"profiles,omitempty"`
	Domain            string            `json:"domain,omitempty"`
	AvailabilityCause AvailabilityCause `json:"availability_cause,omitempty"`
}

// AvailabilityCause classifies a failed listing observation without interpreting
// its diagnostic text. An empty value means no cause was established.
type AvailabilityCause string

const (
	AvailabilityTransportUnavailable AvailabilityCause = "transport_unavailable"
	AvailabilityCredential           AvailabilityCause = "credential"
	AvailabilityRights               AvailabilityCause = "rights"
	AvailabilityConfiguration        AvailabilityCause = "configuration"
	AvailabilityMalformed            AvailabilityCause = "malformed"
	AvailabilityUnknown              AvailabilityCause = "unknown"
	AvailabilityOther                AvailabilityCause = "other"
)

// Holder is one hold of this machine's accelerator memory, as
// abstraction.resource/table@1 reports it. The router measures nothing:
// residency.go says where these rows come from.
//
// The row is per program rather than per host, which is as fine as this
// machine can attribute the memory and finer than a host can: Lemonade spawns
// one llama-server child per model, so a host's own number is neither the
// model's nor the host's. One program with two processes holding the card is
// two rows.
type Holder struct {
	Program string  `json:"program"`
	Account string  `json:"account,omitempty"`
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
	Hosted       = "hosted"
	Unservable   = "unservable"
	NotOnDisk    = "not-here"
	EmptyFamily  = "unparseable"
	Unauthorised = "unauthorised"
	// NoHost is a request for a profile no servable host serves, or for a
	// model its hosts hold and serve only for other profiles.
	NoHost = "no-host"
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
