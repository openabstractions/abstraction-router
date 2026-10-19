package router

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ObserveOffer (router@1) waits for an Offer answer to change. A server
// announces nothing, so what a wait reads is renewed by the router's surveys:
// its own clock (Poll), a declaration change (SetHosts) or a fresh call. Each
// renewal starts a new generation. One reading per request and generation is
// shared by every observer, so a paired runtime's Offer is asked once per
// survey however many callers wait (ROUTE-S4).

// OfferReading is one Offer answer with its cursor, a digest of the offers.
type OfferReading struct {
	Offers []Offer
	At     time.Time
	Cursor string
}

// MaxOfferWait bounds one ObserveOffer wait (router.thrift ObserveOffer).
const MaxOfferWait = 30 * time.Second

// observeReplyMargin is the part of a caller's deadline a wait leaves for the
// reply.
const observeReplyMargin = time.Second

// ErrInvalidOffer is an Offer request whose placement or reserve the router
// cannot answer.
var ErrInvalidOffer = errors.New("router: invalid offer request")

// observed is one request's reading for a generation. ready closes when the
// first observer to ask has computed it; the others wait for that reading.
type observed struct {
	gen     uint64
	ready   chan struct{}
	reading OfferReading
	err     error
}

type offerObservers struct {
	mu       sync.Mutex
	gen      uint64
	renewed  chan struct{}
	readings map[string]*observed
}

// renew starts a new generation: every waiter reads again.
func (r *Router) renew() {
	o := &r.observers
	o.mu.Lock()
	defer o.mu.Unlock()
	o.gen++
	o.readings = nil
	if o.renewed != nil {
		close(o.renewed)
		o.renewed = nil
	}
}

func validOffer(req Request) error {
	if !validPlacement(req.Placement) {
		return fmt.Errorf("%w: placement %q is none of %q, %q and %q", ErrInvalidOffer, req.Placement, PlacementLocal, PlacementHosted, PlacementAny)
	}
	if req.Reserve < 0 {
		return fmt.Errorf("%w: reserve %d is negative", ErrInvalidOffer, req.Reserve)
	}
	return nil
}

// offerCursor digests the offers a reading answers; equal offers give an
// equal cursor across generations and runtime starts.
func offerCursor(offers []Offer) string {
	data, err := json.Marshal(offers)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:16])
}

// reading answers req from the current generation, shared by its observers.
func (r *Router) reading(ctx context.Context, req Request) (OfferReading, <-chan struct{}, error) {
	key := fmt.Sprintf("%s\x00%s\x00%d", req.Profile, req.Placement, req.Reserve)
	o := &r.observers
	o.mu.Lock()
	if o.renewed == nil {
		o.renewed = make(chan struct{})
	}
	gen, renewed := o.gen, o.renewed
	entry := o.readings[key]
	owner := entry == nil || entry.gen != gen
	if owner {
		entry = &observed{gen: gen, ready: make(chan struct{})}
		if o.readings == nil {
			o.readings = map[string]*observed{}
		}
		o.readings[key] = entry
	}
	o.mu.Unlock()
	if !owner {
		select {
		case <-entry.ready:
			if entry.err != nil {
				// The reader's own failure, such as its cancellation, is not
				// this observer's: read again.
				return r.reading(ctx, req)
			}
			return entry.reading, renewed, nil
		case <-ctx.Done():
			return OfferReading{}, nil, ctx.Err()
		}
	}
	offers, at, err := r.OfferContext(ctx, Request{Op: OpOffer, Profile: req.Profile, Placement: req.Placement, Reserve: req.Reserve})
	if err == nil {
		entry.reading = OfferReading{Offers: offers, At: at, Cursor: offerCursor(offers)}
	}
	entry.err = err
	if err != nil {
		// A failed reading is not shared: the next observer reads again.
		o.mu.Lock()
		if o.readings[key] == entry {
			delete(o.readings, key)
		}
		o.mu.Unlock()
	}
	close(entry.ready)
	return entry.reading, renewed, err
}

// ObserveOffer answers req's offers once their cursor differs from cursor, or
// when wait ends. An empty cursor answers at once; req.Fresh surveys before
// the first reading only.
func (r *Router) ObserveOffer(ctx context.Context, req Request, cursor string, wait time.Duration) (OfferReading, error) {
	if err := validOffer(req); err != nil {
		return OfferReading{}, err
	}
	if wait < 0 || wait > MaxOfferWait {
		return OfferReading{}, fmt.Errorf("%w: wait %v is outside 0..%v", ErrInvalidOffer, wait, MaxOfferWait)
	}
	if req.Fresh && cursor == "" {
		if err := r.freshSurvey(ctx); err != nil {
			return OfferReading{}, err
		}
	}
	// A caller's deadline shortens the wait: it gets the unchanged reading
	// with time left to receive it.
	if deadline, ok := ctx.Deadline(); ok {
		if left := time.Until(deadline) - observeReplyMargin; left < wait {
			wait = max(left, 0)
		}
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		reading, renewed, err := r.reading(ctx, req)
		if err != nil {
			return OfferReading{}, err
		}
		if cursor == "" || reading.Cursor != cursor {
			return reading, nil
		}
		select {
		case <-renewed:
		case <-timer.C:
			return reading, nil
		case <-ctx.Done():
			return OfferReading{}, ctx.Err()
		}
	}
}
