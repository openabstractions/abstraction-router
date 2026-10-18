package router

import (
	"context"
	"errors"
	"sort"
	"time"

	wire "github.com/openabstractions/abstraction-router/go/abstraction/router"
)

// profileDefaultContext is the catalogue's per-use context_default
// (research/first-run/DECISION.md §3): 32,768 tokens for chat, 8,192 for
// embed, and 0 for a profile without a context window. Its decided home is
// abstraction.model catalogue@1; the router holds the two numbers until that
// document exists.
func profileDefaultContext(profile string) int64 {
	switch profile {
	case ProfileChat:
		return 32768
	case ProfileEmbed:
		return 8192
	}
	return 0
}

// validPlacement reports whether p is a placement an offer request may name.
func validPlacement(p string) bool {
	return p == PlacementLocal || p == PlacementHosted || p == PlacementAny
}

// admits reports whether placement counts h: local counts servers on this
// machine, hosted counts hosted servers and remote runtimes, any counts both.
func admits(placement string, h *Host) bool {
	switch placement {
	case PlacementLocal:
		return !h.Hosted
	case PlacementHosted:
		return h.Hosted
	}
	return true
}

// sourceRank orders context sources for a tie between two equal bounds: a
// server's report outranks a maximum, which outranks a default.
func sourceRank(source string) int {
	switch source {
	case ContextEffective:
		return 0
	case ContextMaximum:
		return 1
	}
	return 2
}

// offerFold accumulates one family's offer.
type offerFold struct {
	names   map[string]bool
	local   map[string]bool
	remote  map[string][]string
	unknown []string
	// windowUnknown is true once a counted alias's server fixes its window
	// at load and reported none.
	windowUnknown bool
	bound         int64
	source        string
}

func (f *offerFold) count(bound int64, source string) {
	if f.source == "" || bound < f.bound || (bound == f.bound && sourceRank(source) < sourceRank(f.source)) {
		f.bound, f.source = bound, source
	}
}

// aliasBound is one alias's bound under its own server (ROUTE-D8): its valid
// effective length, else its maximum, else the profile's default. ok is
// false for an alias on a server that fixes its window at load and reported
// no valid effective length.
func aliasBound(a Alias, h *Host, fallback int64) (bound int64, source string, ok bool) {
	if effective := validEffective(a.EffectiveContextLength, a.ContextLength); effective > 0 {
		return effective, ContextEffective, true
	}
	if h.windowAtLoad {
		return 0, ContextUnknown, false
	}
	if a.ContextLength > 0 {
		return a.ContextLength, ContextMaximum, true
	}
	return fallback, ContextDefault, true
}

// Offer answers ROUTE-D8 from the latest survey, surveying first when
// req.Fresh asks.
func (r *Router) Offer(req Request) ([]Offer, time.Time, error) {
	return r.OfferContext(context.Background(), req)
}

// OfferContext lists every family a caller may ask for req.Profile on the
// servers req.Placement counts, one name each and the conservative context
// after req.Reserve (ROUTE-D8). A remote runtime's families come from its
// own router@1 Offer, folded into the local family by the minimum context;
// a remote that answers unknown_method is folded from the aliases its Models
// listed, and a remote whose Offer fails otherwise counts nothing (ROUTE-S4). The fold reads the survey in memory and asks no server
// on this machine.
func (r *Router) OfferContext(ctx context.Context, req Request) ([]Offer, time.Time, error) {
	if req.Fresh {
		if err := r.SurveyContext(ctx); err != nil {
			return nil, time.Time{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, time.Time{}, err
	}
	s := r.latest(false)
	profile := req.Profile
	if profile == "" {
		profile = ProfileChat
	}
	fallback := profileDefaultContext(profile)
	windowed := fallback > 0
	hosts := r.Hosts()
	byName := make(map[string]*Host, len(hosts))
	for _, h := range hosts {
		byName[h.Name] = h
	}

	folds := map[string]*offerFold{}
	fold := func(family string) *offerFold {
		f := folds[family]
		if f == nil {
			f = &offerFold{names: map[string]bool{}, local: map[string]bool{}, remote: map[string][]string{}}
			folds[family] = f
		}
		return f
	}

	answered := map[string]bool{}
	for _, h := range hosts {
		if h.remote == nil || !h.Servable() || !admits(req.Placement, h) {
			continue
		}
		remoteCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		snapshot, err := h.remoteOffer(remoteCtx, wire.OfferRequest{Profile: req.Profile, Placement: wire.PlacementAny, Reserve: req.Reserve, Fresh: req.Fresh})
		cancel()
		var refused *wire.ServiceError
		if errors.As(err, &refused) && refused.Code == wire.ServiceErrorCodeUnknownMethod {
			// A remote older than Offer is folded from its listed aliases.
			continue
		}
		answered[h.Name] = true
		if err != nil || snapshot.Outcome != wire.OfferOutcomeOffered {
			// A remote that fails Offer otherwise counts nothing, as a
			// server whose listing fails is left out.
			continue
		}
		for _, o := range snapshot.Offers {
			f := fold(o.Family)
			for _, name := range o.Names {
				f.names[name] = true
			}
			for _, server := range o.Servers {
				if !has(f.remote[h.Name], server) {
					f.remote[h.Name] = append(f.remote[h.Name], server)
				}
			}
			for _, alias := range o.Unknown {
				f.unknown = append(f.unknown, h.Name+"/"+alias)
			}
			switch source := string(o.ContextSource); source {
			case ContextEffective, ContextMaximum, ContextDefault:
				f.count(o.Context, source)
			case ContextNone:
			default:
				f.windowUnknown = true
			}
		}
	}

	for _, family := range s.families {
		for _, a := range family.Names {
			h := byName[a.Host]
			if h == nil || !a.Servable || !h.Servable() || answered[h.Name] || !admits(req.Placement, h) || !s.serves(h, a.Name, profile) {
				continue
			}
			f := fold(family.Family)
			f.names[a.Name] = true
			f.local[h.Name] = true
			if !windowed {
				continue
			}
			bound, source, ok := aliasBound(a, h, fallback)
			if !ok {
				f.windowUnknown = true
				f.unknown = append(f.unknown, h.Name+"/"+a.Name)
				continue
			}
			f.count(bound, source)
		}
	}

	offers := make([]Offer, 0, len(folds))
	for family, f := range folds {
		o := Offer{Family: family}
		for name := range f.names {
			o.Names = append(o.Names, name)
		}
		sort.Strings(o.Names)
		for _, h := range hosts {
			if f.local[h.Name] {
				o.Servers = append(o.Servers, h.Name)
			}
			for _, server := range f.remote[h.Name] {
				o.Servers = append(o.Servers, h.Name+"/"+server)
			}
		}
		switch {
		case !windowed || (!f.windowUnknown && f.source == ""):
			o.ContextSource = ContextNone
		case f.windowUnknown:
			o.ContextSource = ContextUnknown
			o.Unknown = append([]string(nil), f.unknown...)
			sort.Strings(o.Unknown)
		default:
			o.Context, o.ContextSource = f.bound, f.source
			o.Input = max(o.Context-req.Reserve, 0)
		}
		offers = append(offers, o)
	}
	sort.Slice(offers, func(i, j int) bool { return offers[i].Family < offers[j].Family })
	return offers, s.at, nil
}
