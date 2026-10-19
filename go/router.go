// Package router answers three questions about the model hosts already running
// on one machine, and nothing else.
//
//	what models exist here, under every name they are known by
//	which host holds which, and what that costs
//	given a request, which host should serve it
//
// It hosts no model, ships no chat client and renders no interface. The three
// measurements it is built on say why. Consolidating three models into one host
// saved 0.30 GB of 42.87 and turned one failed load from a 33% loss into a 100%
// one; not loading the same model twice saved 22.52 GB with every host left
// exactly where it was; and four hosts name the same weights four ways with no
// overlap, so the resolution cannot live in any of them.
package router

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	programidentity "github.com/openabstractions/abstraction-identity"
	"unicode/utf8"

	"github.com/openabstractions/abstraction-model/identity"
)

type snapshot struct {
	at       time.Time
	hosts    []HostState
	families []Family
	pick     map[string]map[string]string
	// profiles holds, per host and model name, the profiles the host's own
	// metadata reports; a name absent here is judged by its host's profiles.
	profiles map[string]map[string][]string
	// holders is who holds this machine's accelerator memory, read from the
	// resource table (residency.go); holdersWhy says why it is empty.
	holders    []Holder
	holdersWhy string
	// aliasFamily is the family each host name resolved to at survey time,
	// by descriptor or by the alias itself. A request naming one of those
	// names reaches the same family it was catalogued under.
	aliasFamily map[string]string
	// unstaged contains native recipe models whose storage evidence is incomplete.
	unstaged map[string]map[string]bool
}

func (s snapshot) installed(host, model string) bool { return !s.unstaged[host][model] }

// familyOf is the family a request's model name belongs to: the one this survey
// catalogued that name under, or what the name itself says.
func (s snapshot) familyOf(name string) string {
	if fam := s.aliasFamily[strings.ToLower(name)]; fam != "" {
		return fam
	}
	return identity.Family(name)
}

type Router struct {
	// surveyClient is immutable after construction and applies only to this
	// router's local host reads. Nil retains the ordinary shared client.
	surveyClient  *http.Client
	hostsMu       sync.Mutex
	hosts         []*Host
	modalityEpoch string
	heldMu        sync.Mutex
	// held is what the storage inventory says this machine holds, whatever any
	// host serves (held.go).
	held        []HeldObject
	residencyMu sync.Mutex
	// residency is where this router reads who holds the card (residency.go).
	residency         ResidencySource
	mu                sync.Mutex
	snap              snapshot
	familyTitleLookup func(string) string
	// flight is the survey in progress, shared by every caller that arrives
	// while it runs and needs no more than it reads; nil when none runs.
	// Guarded by mu.
	flight *surveyFlight
	// surveyGen numbers each survey as it starts and snapGen is the number of
	// the survey whose snapshot is committed: an older survey that finishes
	// later never replaces a newer snapshot. hostsGen counts SetHosts calls;
	// a survey started before the latest one is never joined. Guarded by mu.
	surveyGen, snapGen, hostsGen uint64
	// cadence is each host's last read and its failures, for the clock's
	// per-kind cadence (survey.go).
	cadence cadence
	asked   []Ask
	apply   Applier
	// standing ranks competing hosted servers for one caller
	// (hosted_standing.go). Guarded by mu.
	standing HostedStandingReader
	// observers are ObserveOffer's shared readings and renewal (observe.go).
	observers offerObservers
}

// Hosts are the hosts this router reads, in preference order.
func (r *Router) Hosts() []*Host {
	r.hostsMu.Lock()
	defer r.hostsMu.Unlock()
	return append([]*Host(nil), r.hosts...)
}

// SetHosts replaces the hosts this router reads, for a service whose host
// configuration changed while it runs. The last survey's inventory stays
// readable until the next Survey, and Route considers only the new hosts.
func (r *Router) SetHosts(hosts ...*Host) {
	r.hostsMu.Lock()
	r.hosts = append([]*Host(nil), hosts...)
	r.hostsMu.Unlock()
	r.mu.Lock()
	r.hostsGen++
	hostsGen := r.hostsGen
	r.mu.Unlock()
	// A pairing or declaration change resets every host's cadence.
	r.cadence.reset(hostsGen)
	r.renew()
}

func New(hosts ...*Host) *Router { return &Router{hosts: hosts} }

// NewWithHTTPClient constructs a router whose local listing, residency and
// metadata reads use the caller's client. It preserves that client's transport,
// redirect policy and the survey's request context. Configure only this
// instance; New and the package's default client retain their behavior.
// A supplied-client flight also keeps the creating caller's earlier deadline.
func NewWithHTTPClient(client *http.Client, hosts ...*Host) *Router {
	r := New(hosts...)
	r.surveyClient = client
	return r
}

// SetFamilyTitleLookup supplies catalogue display titles. Configure it before
// the first survey. An empty or unsafe title leaves the family name available
// as the consumer's fallback. The lookup never participates in routing.
func (r *Router) SetFamilyTitleLookup(lookup func(string) string) {
	r.mu.Lock()
	r.familyTitleLookup = lookup
	r.mu.Unlock()
}

func (r *Router) familyTitle(family string) string {
	r.mu.Lock()
	lookup := r.familyTitleLookup
	r.mu.Unlock()
	if lookup == nil {
		return ""
	}
	title := strings.TrimSpace(lookup(family))
	if len(title) > 128 || !utf8.ValidString(title) || strings.IndexFunc(title, func(r rune) bool { return !unicode.IsPrint(r) }) >= 0 || titleDigest.MatchString(title) {
		return ""
	}
	return title
}

// A title is display text within router.thrift's 128-byte wire bound.
// Content hashes and commits retain the consumer's plain family fallback.
var titleDigest = regexp.MustCompile(`(?i)[0-9a-f]{16,}|(?:^|[\\/:])sha256[-:]`)

// hostRead is what one survey read from one host.
type hostRead struct {
	state     HostState
	installed []string
	resident  []string
	profiles  map[string][]string
	contexts  map[string]modelContext
	// capabilities holds each name's capabilities in source order
	// (surveyedCapabilities).
	capabilities map[string]*ModelCapabilities
	// domain holds the hosts a remote runtime reports.
	domain []HostState
}

// Survey reads every host once, in parallel, through the shared survey
// flight (freshSurvey). It is the only thing here that touches a host, and it
// only reads.
func (r *Router) Survey() {
	_ = r.SurveyContext(context.Background())
}

// SurveyContext reads every host once for one caller, joining a survey of
// every host already in flight. Cancellation reaches each listing request and
// leaves the previous snapshot intact.
func (r *Router) SurveyContext(ctx context.Context) error {
	return r.freshSurvey(ctx)
}

// readHost reads one host: its listing, residency, profiles and contexts,
// or why it could not.
func (r *Router) readHost(ctx context.Context, h *Host) hostRead {
	if r.surveyClient != nil && !h.Hosted {
		ctx = context.WithValue(ctx, surveyHTTPClientKey{}, r.surveyClient)
	}
	s := HostState{Host: h.Name, Base: h.Base, Servable: h.Servable(), Hosted: h.Hosted, Wire: h.Wire, Credential: h.Credential, DeclaredBy: h.DeclaredBy,
		Profiles: h.HostProfiles(), Domain: h.Domain, Engines: h.Engines}
	if h.Hosted {
		// A paired runtime's listing and its host list share one 15 s budget.
		remoteDeadline := time.Now().Add(15 * time.Second)
		installed, listed, why, cause := r.readHosted(ctx, h)
		if why != "" {
			s.Why = why
			s.AvailabilityCause = cause
			return hostRead{state: s}
		}
		s.Up, s.Installed = true, len(installed)
		out := hostRead{state: s, installed: installed}
		// A provider's listing carries the context window and media it
		// reports for each model, completed from the hosted catalogue.
		out.contexts, out.profiles = hostedMetadata(h, installed, listed)
		out.capabilities = surveyedCapabilities(h, installed, h.reportedCapabilities(ctx, installed), listed.capabilities)
		// A remote runtime's listing carries the context its own
		// router reports for each name (remote.go).
		if h.modelContexts != nil {
			out.contexts = h.modelContexts(ctx, h, installed)
		}
		// A remote runtime's listing carries the profiles its own
		// router reports for each name (ROUTE-D9).
		if h.modelProfiles != nil {
			out.profiles = h.modelProfiles(ctx, h, installed)
		}
		if h.remote != nil {
			remoteCtx, cancel := context.WithDeadline(ctx, remoteDeadline)
			defer cancel()
			if domain, err := h.remoteHosts(remoteCtx); err == nil {
				out.domain = domain
			}
		}
		return out
	}
	installed, err := h.installed(ctx, h)
	if err != nil {
		s.Why = err.Error()
		s.AvailabilityCause = causeOf(err)
		if h.Wire != WireNative && h.Wire != WireRemote && errors.Is(err, programidentity.ErrNotProven) {
			s.Why = "engine:unverified:" + h.Name
			s.AvailabilityCause = AvailabilityConfiguration
		}
		return hostRead{state: s}
	}
	if why := h.UnavailableReason(); why != "" {
		// The host's models stay listed, not servable, with its reason; it
		// is not asked for residency.
		s.Servable, s.Why, s.AvailabilityCause, s.Installed = false, why, AvailabilityConfiguration, len(installed)
		return hostRead{state: s, installed: installed, profiles: h.declaredProfiles(installed),
			capabilities: surveyedCapabilities(h, installed, nil, nil)}
	}
	s.Up, s.Installed = true, len(installed)
	if h.surveyReady != nil && !h.surveyReady() {
		return hostRead{state: s, installed: installed, profiles: h.declaredProfiles(installed),
			capabilities: surveyedCapabilities(h, installed, nil, nil)}
	}
	// Residency is read separately: a host without a residency route,
	// or one that is briefly unreachable on that route alone, still has
	// its installed list. Resident stays unset and Why names what
	// failed; the installed list is not discarded for it.
	resident, err := h.resident(ctx, h)
	if err != nil {
		s.Why = err.Error()
		read := hostRead{state: s, installed: installed, profiles: h.declaredProfiles(installed),
			capabilities: surveyedCapabilities(h, installed, h.reportedCapabilities(ctx, installed), nil)}
		// A native provider reports the context each conversation gets
		// without proving residency (ReportContexts).
		if h.native != nil && h.modelContexts != nil {
			read.contexts = h.modelContexts(ctx, h, installed)
		}
		return read
	}
	s.Resident = resident
	var profiles map[string][]string
	var contexts map[string]modelContext
	modelNames := append(slices.Clone(installed), resident...)
	if h.modelMetadata != nil {
		metadata := h.modelMetadata(ctx, h, modelNames)
		profiles = make(map[string][]string, len(metadata))
		contexts = make(map[string]modelContext, len(metadata))
		for name, observed := range metadata {
			profiles[name] = observed.profiles
			contexts[name] = observed.context
		}
	} else {
		if h.modelProfiles != nil {
			profiles = h.modelProfiles(ctx, h, modelNames)
		}
		if h.modelContexts != nil {
			contexts = h.modelContexts(ctx, h, modelNames)
		}
	}
	// A declaration that names its models per profile outranks what
	// the host's own metadata reports (ROUTE-D9).
	if declared := h.declaredProfiles(modelNames); declared != nil {
		profiles = declared
	}
	return hostRead{state: s, installed: installed, resident: resident, profiles: profiles, contexts: contexts,
		capabilities: surveyedCapabilities(h, modelNames, h.reportedCapabilities(ctx, modelNames), nil)}
}

// reportedCapabilities are the facts h's owner reports for names, or none.
func (h *Host) reportedCapabilities(ctx context.Context, names []string) map[string]*ModelCapabilities {
	if h.modelCapabilities == nil {
		return nil
	}
	return h.modelCapabilities(ctx, h, names)
}

// survey reads the hosts in parallel and commits the snapshot when no newer
// survey committed first. full reads every host; the clock's survey (full
// false) reads only the hosts its cadence says are due and keeps each other
// host's last read (survey.go).
func (r *Router) survey(ctx context.Context, full bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	r.surveyGen++
	gen, hostsGen := r.surveyGen, r.hostsGen
	r.mu.Unlock()
	now := time.Now()
	hosts := r.Hosts()
	reads := make([]hostRead, len(hosts))
	fresh := make([]bool, len(hosts))
	var wg sync.WaitGroup
	for i, h := range hosts {
		if !full {
			if kept, ok := r.cadence.kept(h, now); ok {
				reads[i] = kept
				continue
			}
		}
		fresh[i] = true
		wg.Add(1)
		go func(i int, h *Host) {
			defer wg.Done()
			if ctx.Err() != nil {
				return
			}
			reads[i] = r.readHost(ctx, h)
		}(i, h)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	for i, h := range hosts {
		if fresh[i] {
			r.cadence.record(h, reads[i], gen, hostsGen, now)
		}
	}

	// The storage inventory's descriptors are the model identity. A host's
	// model is matched to one by the store's own naming (held.go); a model no
	// descriptor names keeps the host's alias as its family, and the family
	// says so.
	held := r.Held()
	prov := index(held)
	// A recipe that names its files by hash is installed only while storage
	// holds hashed evidence of every one of them, whoever declared it. A
	// recipe naming inventory objects (store and object) keeps its own owner's
	// account of installation. A declared native host with hash-named recipes
	// and none installed has no engine here: it is listed unavailable and
	// offers nothing.
	complete := completeEngineModels(held, hosts, true)
	unstaged := map[string]map[string]bool{}
	recipeHost := map[string]bool{}
	for _, host := range hosts {
		if host.Wire != WireNative || host.Hosted {
			continue
		}
		unstaged[host.Name] = map[string]bool{}
		for _, object := range host.EngineObjects {
			if object.Digest == "" || object.Object != "" {
				continue
			}
			recipeHost[host.Name] = true
			for _, engine := range object.Engines {
				if !complete[host][engine.Model] {
					unstaged[host.Name][engine.Model] = true
				}
			}
		}
	}
	for i, h := range hosts {
		if !recipeHost[h.Name] {
			continue
		}
		keep := func(names []string) []string {
			return slices.DeleteFunc(slices.Clone(names), func(name string) bool { return unstaged[h.Name][name] })
		}
		reads[i].installed, reads[i].resident = keep(reads[i].installed), keep(reads[i].resident)
		reads[i].state.Installed = len(reads[i].installed)
		if len(reads[i].installed) == 0 && len(reads[i].resident) == 0 {
			reads[i].state.Up, reads[i].state.Servable, reads[i].state.Resident = false, false, nil
			reads[i].state.Why = "engine:not_installed:" + h.Name
			reads[i].state.AvailabilityCause = AvailabilityConfiguration
		}
	}
	// A family lists one alias per server and name. Two servers serving the
	// same name are two entries, each carrying its own server's residency and
	// context; a request naming that name still reaches the family once.
	type served struct {
		host int
		name string
	}
	byFamily := map[string]map[served]*Alias{}
	pick := map[string]map[string]string{}
	profiles := map[string]map[string][]string{}
	aliasFamily := map[string]string{}
	for i, h := range hosts {
		if reads[i].profiles != nil {
			profiles[h.Name] = reads[i].profiles
		}
		note := func(name string, resident bool) {
			// The host's declared object association outranks a match by
			// name: it says which stored object the name is.
			fam, declared := prov.servedFamily(h, name)
			if !declared {
				fam, declared = prov.familyOf(name)
			}
			if !declared {
				fam = identity.Family(name)
			}
			if fam == "" {
				return
			}
			aliasFamily[strings.ToLower(name)] = fam
			names := byFamily[fam]
			if names == nil {
				names = map[served]*Alias{}
				byFamily[fam] = names
			}
			key := served{host: i, name: name}
			if a, seen := names[key]; seen {
				// The same server lists a resident name again as installed.
				a.Resident = a.Resident || resident
			} else {
				context := reads[i].contexts[name]
				names[key] = &Alias{Host: h.Name, Name: name, Resident: resident, Servable: reads[i].state.Servable, Hosted: h.Hosted, Profiles: reads[i].profiles[name], ContextLength: context.maximum, EffectiveContextLength: context.effective,
					Capabilities: cloneCapabilities(reads[i].capabilities[name])}
			}
			if pick[fam] == nil {
				pick[fam] = map[string]string{}
			}
			// A host carries several builds of one family — llama.cpp, an MTP
			// variant, an NPU recipe. A build it already holds wins, because
			// loading a second build of a model the host has in memory is the
			// waste this exists to stop; otherwise the host's own order decides.
			if _, taken := pick[fam][h.Name]; !taken || resident {
				pick[fam][h.Name] = name
			}
		}
		for _, name := range reads[i].resident {
			note(name, true)
		}
		for _, name := range reads[i].installed {
			note(name, false)
		}
	}

	families := make([]Family, 0, len(byFamily))
	for fam, names := range byFamily {
		keys := make([]served, 0, len(names))
		for key := range names {
			keys = append(keys, key)
		}
		// By name, then by the servers' preference order.
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].name != keys[j].name {
				return keys[i].name < keys[j].name
			}
			return keys[i].host < keys[j].host
		})
		f := Family{Family: fam}
		for _, key := range keys {
			f.Names = append(f.Names, *names[key])
		}
		families = append(families, f)
	}
	sort.Slice(families, func(i, j int) bool { return families[i].Family < families[j].Family })
	// What the machine holds is the other half of the answer: a store's model
	// that no host serves is still on this machine, and an application asking
	// what exists here is owed it.
	families = prov.compose(families)
	families = composeEngineGuide(families, held, hosts)
	for i := range families {
		families[i].Title = r.familyTitle(families[i].Family)
	}

	holders, why := r.readHolders(false)
	if err := ctx.Err(); err != nil {
		return err
	}
	states := make([]HostState, 0, len(reads))
	for i := range reads {
		states = append(states, reads[i].state)
		states = append(states, reads[i].domain...)
	}

	r.mu.Lock()
	committed := gen > r.snapGen
	if committed {
		r.snap = snapshot{at: time.Now(), hosts: states, families: families, pick: pick, profiles: profiles,
			aliasFamily: aliasFamily, holders: holders, holdersWhy: why, unstaged: unstaged}
		r.snapGen = gen
	}
	r.mu.Unlock()
	if committed {
		r.renew()
	}
	return nil
}

// freshSurveyBound limits one shared survey, the clock's included. Each
// caller waits only as long as its own context allows; the survey itself
// finishes within this bound and its result serves every caller that joined
// it.
const freshSurveyBound = 30 * time.Second

type surveyFlight struct {
	done    chan struct{}
	err     error
	waiters int // callers still waiting; guarded by Router.mu
	cancel  context.CancelFunc
	// full says the flight reads every host; hostsGen is the SetHosts count
	// it started under.
	full     bool
	hostsGen uint64
}

// freshSurvey refreshes the snapshot from every host for a caller that asked
// for fresh data (joinSurvey).
func (r *Router) freshSurvey(ctx context.Context) error {
	_, err := r.joinSurvey(ctx, true)
	return err
}

// joinSurvey runs one survey for every caller that arrives while it runs. A
// caller joins the flight in progress when it started under the current hosts
// and reads at least what the caller needs; otherwise it starts its own,
// whose later start number orders its snapshot after the older flight's.
// Joining is fresh in this sense: the listings come from a survey that
// finished after the request arrived, possibly started before it. A caller
// whose ctx ends first returns ctx's error and the flight it joined, and the
// previous snapshot stays readable. When the last waiting caller leaves, the
// survey is cancelled and the cancellation reaches each listing request.
func (r *Router) joinSurvey(ctx context.Context, full bool) (*surveyFlight, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	f := r.flight
	if f == nil || f.hostsGen != r.hostsGen || (full && !f.full) {
		deadline := time.Now().Add(freshSurveyBound)
		if r.surveyClient != nil {
			if callerDeadline, ok := ctx.Deadline(); ok && callerDeadline.Before(deadline) {
				deadline = callerDeadline
			}
		}
		surveyCtx, cancel := context.WithDeadline(context.Background(), deadline)
		f = &surveyFlight{done: make(chan struct{}), cancel: cancel, full: full, hostsGen: r.hostsGen}
		r.flight = f
		go func() {
			err := r.survey(surveyCtx, full)
			cancel()
			r.mu.Lock()
			if r.flight == f {
				r.flight = nil
			}
			f.err = err
			r.mu.Unlock()
			close(f.done)
		}()
	}
	f.waiters++
	r.mu.Unlock()
	select {
	case <-f.done:
		return f, f.err
	case <-ctx.Done():
		r.mu.Lock()
		f.waiters--
		if f.waiters == 0 {
			f.cancel()
			if r.flight == f {
				r.flight = nil
			}
		}
		r.mu.Unlock()
		return f, ctx.Err()
	}
}

func (r *Router) latest(fresh bool) snapshot {
	if fresh {
		_ = r.freshSurvey(context.Background())
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snap
}

// Poll re-surveys until stop is closed. A host publishes no notification when a
// model is loaded, so the only honest options are to ask on every request or to
// ask on a clock; asking on a clock is what keeps an answer inside the budget,
// and every answer carries the age of what it was read from. Each tick joins
// the shared survey and reads only the hosts the per-kind cadence says are due
// (ROUTE-D12). Poll returns once stop is closed and the survey it joined has
// ended, so no read outlives its caller's shutdown by more than
// freshSurveyBound.
func (r *Router) Poll(every time.Duration, stop <-chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		f, _ := r.joinSurvey(ctx, false)
		if ctx.Err() != nil {
			if f != nil {
				<-f.done
			}
			return
		}
		select {
		case <-stop:
			return
		case <-t.C:
		}
	}
}

func (r *Router) Models(fresh bool) ([]Family, time.Time) {
	s := r.latest(fresh)
	return s.families, s.at
}

// Residency is every host this router reads, who holds the machine's
// accelerator memory as the resource table reports it, why that is empty when
// it is, the families loaded more than once, and when this was read.
func (r *Router) Residency(fresh bool) ([]HostState, []Holder, string, []string, time.Time) {
	s := r.latest(fresh)
	return s.hosts, s.holders, s.holdersWhy, doubled(s), s.at
}

// doubled names a family held by more than one host while it is happening. The
// identity judgement will be wrong eventually; when it is, the machine holds two
// copies and nothing else on it says so.
func doubled(s snapshot) []string {
	count := map[string]int{}
	for _, f := range s.families {
		for _, a := range f.Names {
			if a.Resident {
				count[f.Family+"\x00"+a.Host]++
			}
		}
	}
	hosts := map[string][]string{}
	for k := range count {
		fam, host, _ := cut(k)
		hosts[fam] = append(hosts[fam], host)
	}
	var out []string
	for fam, hs := range hosts {
		if len(hs) > 1 {
			sort.Strings(hs)
			out = append(out, fmt.Sprintf("%s is loaded %d times: %v", fam, len(hs), hs))
		}
	}
	sort.Strings(out)
	return out
}

func cut(k string) (string, string, bool) {
	for i := 0; i < len(k); i++ {
		if k[i] == 0 {
			return k[:i], k[i+1:], true
		}
	}
	return k, "", false
}

// Route decides. It never loads: a verdict of would-load names the host that
// should be asked and leaves the asking to the caller, so the router cannot
// become the thing that holds weights.
func (r *Router) Route(req Request) (*Decision, time.Time) {
	s := r.latest(req.Fresh)
	return r.routeSnapshot(context.Background(), req, s)
}

// RouteContext uses a cancelable fresh survey when requested. It never
// converts a caller cancellation into a host availability verdict.
func (r *Router) RouteContext(ctx context.Context, req Request) (*Decision, time.Time, error) {
	if req.Fresh {
		if err := r.freshSurvey(ctx); err != nil {
			return nil, time.Time{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, time.Time{}, err
	}
	s := r.latest(false)
	d, at := r.routeSnapshot(ctx, req, s)
	return d, at, nil
}

func (r *Router) routeSnapshot(ctx context.Context, req Request, s snapshot) (*Decision, time.Time) {
	fam := s.familyOf(req.Model)
	d := &Decision{Asked: req.Model, Family: fam, Authorised: req.Hosts}
	if fam == "" {
		d.Verdict, d.Why = EmptyFamily, "nothing in "+quote(req.Model)+" names a model"
		return d, s.at
	}
	exact := exactAlias(req.Model, s.lists(req.Model))
	var installedOn []string
	for _, f := range s.families {
		if f.Family != fam {
			continue
		}
		for _, a := range f.Names {
			// A name no host serves has no host to install it on.
			if a.Host != "" && exact.serves(a.Name) && s.installed(a.Host, a.Name) && !has(installedOn, a.Host) {
				installedOn = append(installedOn, a.Host)
			}
		}
	}
	d.InstalledOn = installedOn
	profile := req.Profile
	if profile == "" {
		profile = ProfileChat
	}
	if !r.anyServes(s, profile) {
		d.Verdict, d.Why = NoHost, "no host serves the "+profile+" profile"
		return d, s.at
	}

	for _, c := range r.rankHosted(ctx, d, r.candidates(s, fam, profile, exact)) {
		if !d.permits(c.host.Name) {
			d.Withheld = append(d.Withheld, c.host.Name)
			continue
		}
		d.Host, d.Model = c.host.Name, c.name
		if c.host.Hosted {
			d.Verdict, d.Loads = Hosted, 0
			d.Why = "no permitted host on this machine has it; " + c.host.Name + " is a hosted host, and the service performs the call"
		} else if c.held {
			d.Verdict, d.Loads = Resident, 0
			d.Why = c.host.Name + " already holds it; routing anywhere else loads a second copy"
		} else {
			d.Verdict, d.Loads = WouldLoad, 1
			d.Why = "no host holds it; " + c.host.Name + " has it installed. The router does not load it"
		}
		if exact == nil && !strings.EqualFold(c.name, req.Model) {
			d.Why += "; the request named the family, and " + quote(c.name) + " serves it"
		}
		return d, s.at
	}
	refuse := func(verdict, why string) (*Decision, time.Time) {
		d.Verdict, d.Why = verdict, why
		if exact != nil {
			d.Why += exact.unsubstituted(r.candidates(s, fam, profile, nil))
		}
		return d, s.at
	}
	if len(d.Withheld) > 0 {
		return refuse(Unauthorised, fmt.Sprintf("%v can serve it and the request authorised %v; the router does not go outside what it was given",
			d.Withheld, *d.Authorised))
	}
	for _, h := range r.Hosts() {
		if h.Servable() && has(installedOn, h.Name) {
			if reason := h.UnavailableReason(); reason != "" {
				return refuse(NoHost, fmt.Sprintf("%v has it, and %s cannot serve now (%s)", installedOn, h.Name, reason))
			}
			return refuse(NoHost, fmt.Sprintf("%v has it, and no host serves it for the %s profile", installedOn, profile))
		}
	}
	if len(installedOn) > 0 {
		return refuse(Unservable, fmt.Sprintf("only %v has it, and what that runs is a graph, not a model an OpenAI-compatible endpoint can serve", installedOn))
	}
	return refuse(NotOnDisk, "no host on this machine has it installed")
}

// exactName is how a request names one file of its family (ROUTE-D3): by a
// quant, which every alias stating that quant serves; or by a model file, or
// a server's full listed alias, which only that name serves. A nil exactName
// is a request naming the family, which any alias of the family serves.
type exactName struct {
	asked, quant string
}

// exactAlias reads a request; listed says a server lists the asked name.
func exactAlias(asked string, listed bool) *exactName {
	if quant := identity.Parse(asked).Quant; quant != "" {
		return &exactName{asked: asked, quant: quant}
	}
	if namesFile(asked) || (listed && !bareFamily(asked)) {
		return &exactName{asked: asked}
	}
	return nil
}

// lists reports whether a server lists name as one of its aliases.
func (s snapshot) lists(name string) bool {
	for _, f := range s.families {
		for _, a := range f.Names {
			if strings.EqualFold(a.Name, name) {
				return true
			}
		}
	}
	return false
}

// bareFamily reports whether a name says its family and nothing more: after
// its publisher, it reads as the family once separators are made one. A
// llama.cpp file-derived name such as "Qwen3.6-35B-A3B-MTP-GGUF" carries a
// build and packaging beyond "qwen3.6-35b-a3b"; "qwen/qwen3.6-35b-a3b" and
// "qwen3:8b" carry none.
func bareFamily(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if i := strings.LastIndexByte(n, '/'); i >= 0 {
		n = n[i+1:]
	}
	n = strings.Map(func(r rune) rune {
		switch r {
		case ':', '_', ' ', '+':
			return '-'
		}
		return r
	}, n)
	return n == identity.Family(name)
}

// serves reports whether an alias is the file the request named.
func (e *exactName) serves(name string) bool {
	switch {
	case e == nil:
		return true
	case e.quant != "":
		return identity.Parse(name).Quant == e.quant
	default:
		return strings.EqualFold(name, e.asked)
	}
}

// unsubstituted names the family's servable sibling an exact request refused,
// so the caller reads why a model it can see did not answer.
func (e *exactName) unsubstituted(siblings []candidate) string {
	if len(siblings) == 0 {
		return ""
	}
	c := siblings[0]
	state := "has"
	if c.held {
		state = "holds"
	}
	return fmt.Sprintf("; %s %s %s of the same family, and an exact name is never answered by another file", c.host.Name, state, quote(c.name))
}

// modelFileExtensions are the file suffixes abstraction-model's identity reads
// as packaging. A request ending in one names that file.
var modelFileExtensions = []string{".gguf", ".safetensors", ".sft", ".bin", ".pt", ".pt2", ".pth", ".ckpt", ".onnx", ".npz", ".pkl"}

func namesFile(asked string) bool {
	lower := strings.ToLower(strings.TrimSpace(asked))
	for _, ext := range modelFileExtensions {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

func (d *Decision) permits(host string) bool {
	return d.Authorised == nil || has(*d.Authorised, host)
}

type candidate struct {
	host *Host
	name string
	held bool
}

// candidates is every host that could serve the family, best first: each host
// that already holds it, then each host that has it on disk, then each hosted
// host that lists it. A host that holds it beats one that would load it however
// the request orders them, because a second copy of a model already in memory
// is the waste this exists to stop; a hosted host comes last, because it spends.
// Only names the host serves for profile count.
//
// An exact request (exactName) keeps only the aliases that are the file it
// named: a resident sibling of another quant or file never answers it.
func (r *Router) candidates(s snapshot, fam, profile string, exact *exactName) []candidate {
	var out []candidate
	// A host that cannot serve now takes no route (Host.Unavailable).
	hosts := slices.DeleteFunc(r.Hosts(), func(h *Host) bool { return h.UnavailableReason() != "" })
	serving := func(h *Host, names []string) []string {
		var kept []string
		for _, name := range names {
			if exact.serves(name) && s.serves(h, name, profile) {
				kept = append(kept, name)
			}
		}
		return kept
	}
	for _, h := range hosts {
		if !h.Servable() || h.Hosted {
			continue
		}
		if names := serving(h, residentOf(s, fam, h.Name)); len(names) > 0 {
			out = append(out, candidate{host: h, name: names[0], held: true})
		}
	}
	for _, h := range hosts {
		if !h.Servable() || h.Hosted || len(serving(h, residentOf(s, fam, h.Name))) > 0 {
			continue
		}
		if name, ok := s.pickServing(fam, h, profile, exact); ok {
			out = append(out, candidate{host: h, name: name})
		}
	}
	for _, h := range hosts {
		if !h.Servable() || !h.Hosted {
			continue
		}
		if name, ok := s.pickServing(fam, h, profile, exact); ok {
			out = append(out, candidate{host: h, name: name})
		}
	}
	return out
}

// pickServing is the host's own choice of name for the family when it serves
// profile, or else the first of its names for the family that does.
func (s snapshot) pickServing(fam string, h *Host, profile string, exact *exactName) (string, bool) {
	if name, ok := s.pick[fam][h.Name]; ok && exact.serves(name) && s.serves(h, name, profile) && s.installed(h.Name, name) {
		return name, true
	}
	for _, f := range s.families {
		if f.Family != fam {
			continue
		}
		for _, a := range f.Names {
			if a.Host == h.Name && exact.serves(a.Name) && s.serves(h, a.Name, profile) && s.installed(h.Name, a.Name) {
				return a.Name, true
			}
		}
	}
	return "", false
}

// anyServes reports whether a servable host serves profile: for a host with
// models, one of them, each judged by its own profiles or, when it reports
// none, its host's (snapshot.serves); for a host with none, its own
// profiles. A router with no servable host answers true, and its decisions
// keep reading not-here.
func (r *Router) anyServes(s snapshot, profile string) bool {
	servable := false
	for _, h := range r.Hosts() {
		if !h.Servable() {
			continue
		}
		servable = true
		aliases := 0
		for _, f := range s.families {
			for _, a := range f.Names {
				if a.Host != h.Name {
					continue
				}
				aliases++
				if s.serves(h, a.Name, profile) {
					return true
				}
			}
		}
		if aliases == 0 && has(h.HostProfiles(), profile) {
			return true
		}
	}
	return !servable
}

func residentOf(s snapshot, fam, host string) []string {
	var out []string
	for _, f := range s.families {
		if f.Family != fam {
			continue
		}
		for _, a := range f.Names {
			if a.Resident && a.Host == host {
				out = append(out, a.Name)
			}
		}
	}
	return out
}

func quote(s string) string { return fmt.Sprintf("%q", s) }

func (r *Router) record(a Ask) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.asked = append([]Ask{a}, r.asked...)
	if len(r.asked) > 50 {
		r.asked = r.asked[:50]
	}
}

// Asked is the audit no host can produce: which program asked for which model,
// and what it was sent to. A host sees a request; only something above all of
// them sees who caused a load.
func (r *Router) Asked() []Ask {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Ask(nil), r.asked...)
}
