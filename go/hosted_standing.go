package router

import (
	"context"
	"slices"
	"time"
)

// HostedStanding is a hosted server's standing for the caller of one route:
// whether that caller may already spend through it. A route that may reach
// several hosted servers serving the same model (two accounts) takes one the
// caller may already use before one that would ask the person, and one that
// would ask before one the caller is refused.
type HostedStanding int

const (
	// HostedUndecided keeps the server's place in the ordinary ranking.
	HostedUndecided HostedStanding = iota
	// HostedPermitted puts the server ahead of the undecided ones.
	HostedPermitted
	// HostedRefused puts the server after every other one.
	HostedRefused
)

// HostedStandingReader reads a hosted server's standing for the caller ctx
// carries. It decides quietly: reading a standing asks nobody.
type HostedStandingReader func(ctx context.Context, host string) HostedStanding

// UseHostedStanding gives the router the reader its routes rank competing
// hosted servers by. Route reads it with a context that carries no caller.
// Without a reader, hosted servers keep their ordinary order.
func (r *Router) UseHostedStanding(read HostedStandingReader) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.standing = read
}

func (r *Router) standingReader() HostedStandingReader {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.standing
}

// RouteFor is Route for the caller ctx carries: when more than one permitted
// hosted server could serve the request, they are ranked by that caller's
// standing (UseHostedStanding). ctx carries the caller only; a fresh survey
// runs as Route's does.
func (r *Router) RouteFor(ctx context.Context, req Request) (*Decision, time.Time) {
	s := r.latest(req.Fresh)
	return r.routeSnapshot(ctx, req, s)
}

// rankHosted reorders the hosted servers among out that d permits by the
// caller's standing, permitted first and refused last, each group in its
// ordinary order. The reader runs only when two or more such servers compete,
// so a route with one hosted choice reads no decision.
func (r *Router) rankHosted(ctx context.Context, d *Decision, out []candidate) []candidate {
	read := r.standingReader()
	if read == nil {
		return out
	}
	first := -1
	competing := 0
	for i, c := range out {
		if !c.host.Hosted {
			continue
		}
		if first < 0 {
			first = i
		}
		if d.permits(c.host.Name) {
			competing++
		}
	}
	if competing < 2 {
		return out
	}
	rank := map[string]int{}
	for _, c := range out[first:] {
		if !c.host.Hosted || !d.permits(c.host.Name) {
			continue
		}
		switch read(ctx, c.host.Name) {
		case HostedPermitted:
			rank[c.host.Name] = -1
		case HostedRefused:
			rank[c.host.Name] = 1
		}
	}
	ranked := append([]candidate(nil), out...)
	slices.SortStableFunc(ranked[first:], func(a, b candidate) int {
		return rank[a.host.Name] - rank[b.host.Name]
	})
	return ranked
}
