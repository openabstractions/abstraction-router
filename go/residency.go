package router

// Who holds the machine's accelerator memory is a resource question, and it
// moved out of this package on 2026-09-22 into abstraction.resource/table@1
// (research/architecture-review-2026-09-22.md R4). The router measured the
// card because nothing else did; the table measures it now, attributes each
// hold to a program and an account through the evidence rights uses, and
// carries claimed rows the router never had. The router reads it.

// A ResidencySource says who holds this machine's accelerator memory. The
// runtime satisfies it from the resource table; a router with no source
// reports no holders and says why, which is what routerd does on its own.
type ResidencySource interface {
	// Holders returns the current rows. fresh asks the source to measure
	// again rather than stand on the sample it holds.
	Holders(fresh bool) ([]Holder, error)
}

// SetResidency replaces where this router reads holders. A nil source leaves
// the residency answer empty with a reason, and never an invented zero.
func (r *Router) SetResidency(source ResidencySource) {
	r.residencyMu.Lock()
	defer r.residencyMu.Unlock()
	r.residency = source
}

func (r *Router) residencySource() ResidencySource {
	r.residencyMu.Lock()
	defer r.residencyMu.Unlock()
	return r.residency
}

// noResidencySource is what a residency answer says when nothing reads the
// table for this router.
const noResidencySource = "no resource table is wired to this router; who holds the card is not read here"

// readHolders is the Survey's one call into the source.
func (r *Router) readHolders(fresh bool) ([]Holder, string) {
	source := r.residencySource()
	if source == nil {
		return nil, noResidencySource
	}
	holders, err := source.Holders(fresh)
	if err != nil {
		return nil, err.Error()
	}
	return holders, ""
}
