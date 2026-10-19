package router

import (
	"context"
	"sync"
	"time"
)

// The clock's cadence per kind of host (CONTRACT.md ROUTE-D12). The runtime's
// clock (Poll) reads a local engine at every tick. A hosted listing that
// answered costs a provider request and a credential audit entry, so the
// clock reads it again at most every HostedListingEvery; a fresh call, a host
// change or a stored credential still reads it at the next survey. A paired
// runtime is read at every tick while it answers. A hosted listing or a paired
// runtime that failed is read again after 2, 4, 8 and then 15 minutes
// (FailedReadBackoffMax), until it answers or the hosts change.
const (
	HostedListingEvery   = 15 * time.Minute
	FailedReadBackoffMax = 15 * time.Minute
)

// failedReadBackoff is how long the clock leaves a hosted listing or a paired
// runtime alone after failures consecutive failed reads: 2, 4, 8, then 15
// minutes.
func failedReadBackoff(failures int) time.Duration {
	if failures <= 0 {
		return 0
	}
	if failures >= 4 {
		return FailedReadBackoffMax
	}
	return time.Minute << failures
}

type cachedRead struct {
	gen      uint64
	read     hostRead
	at       time.Time
	failures int
}

// cadence keeps each host's last read, keyed by name, for the clock.
// hostsGen is the host-change count the records belong to: a survey started
// under an earlier host list records nothing.
type cadence struct {
	mu       sync.Mutex
	hostsGen uint64
	reads    map[string]cachedRead
}

// reset forgets every host's read for the host list numbered hostsGen.
func (c *cadence) reset(hostsGen uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if hostsGen > c.hostsGen {
		c.hostsGen = hostsGen
	}
	c.reads = nil
}

// advance moves to the host list numbered hostsGen, so a survey started
// before it records nothing, and forgets the named hosts; every other host
// keeps its last reading.
func (c *cadence) advance(hostsGen uint64, names ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if hostsGen > c.hostsGen {
		c.hostsGen = hostsGen
	}
	for _, name := range names {
		delete(c.reads, name)
	}
}

// kept answers h's last read when the clock is not due to read h at now.
func (c *cadence) kept(h *Host, now time.Time) (hostRead, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	last, ok := c.reads[h.Name]
	if !ok || !h.Hosted {
		return hostRead{}, false
	}
	switch {
	case last.failures > 0:
		if now.Sub(last.at) >= failedReadBackoff(last.failures) {
			return hostRead{}, false
		}
	case h.remote != nil:
		return hostRead{}, false
	case now.Sub(last.at) >= HostedListingEvery:
		return hostRead{}, false
	}
	return last.read, true
}

// backingOff says the clock is waiting out h's failures at now.
func (c *cadence) backingOff(h *Host, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	last, ok := c.reads[h.Name]
	return ok && last.failures > 0 && now.Sub(last.at) < failedReadBackoff(last.failures)
}

// record keeps read as h's last unless a later survey already recorded one
// or the survey started under an earlier host list.
func (c *cadence) record(h *Host, read hostRead, gen, hostsGen uint64, at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if hostsGen < c.hostsGen {
		return
	}
	last, ok := c.reads[h.Name]
	if ok && last.gen > gen {
		return
	}
	failures := 0
	if !read.state.Up {
		failures = last.failures + 1
	}
	if c.reads == nil {
		c.reads = map[string]cachedRead{}
	}
	c.reads[h.Name] = cachedRead{gen: gen, read: read, at: at, failures: failures}
}

// RereadHosts reads the named hosts now, as after a credential for them is
// stored, and treats the change as a host change: a survey already in flight
// records nothing, is joined no more and loses to this one by start order, so
// a listing it read before the credential cannot keep the host down. One
// survey runs, reading the named hosts and the hosts the clock is due to read;
// every other host keeps its last reading (CONTRACT.md ROUTE-D12).
func (r *Router) RereadHosts(ctx context.Context, names ...string) error {
	r.mu.Lock()
	r.hostsGen++
	hostsGen := r.hostsGen
	r.mu.Unlock()
	r.cadence.advance(hostsGen, names...)
	_, err := r.joinSurvey(ctx, false)
	return err
}
