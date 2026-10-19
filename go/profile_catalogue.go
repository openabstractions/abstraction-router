package router

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ModalityPage is the bounded catalogue result used by the framed service.
type ModalityPage struct {
	Outcome    string
	Modalities []string
	Next       string
	Complete   bool
}

// The registered-host grammar also governs custom catalogue modalities.
var ownedModality = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}/[a-z0-9][a-z0-9_.-]{0,62}@[1-9][0-9]{0,5}$`)

// ValidModality reports seed membership or a valid owned modality identifier.
func ValidModality(modality string) bool {
	return len(modality) <= 256 && (slices.Contains(SeedProfiles, modality) || ownedModality.MatchString(modality))
}

// Modalities reads current declarations without consulting host model listings.
// scope is supplied by the receiving service from its admitted caller.
func (r *Router) Modalities(cursor string, limit int64, scope string) ModalityPage {
	refusal := func(outcome string) ModalityPage { return ModalityPage{Outcome: outcome, Modalities: []string{}} }
	if limit < 1 || limit > 64 || len(cursor) > 256 || !utf8.ValidString(cursor) {
		return refusal("invalid")
	}
	r.hostsMu.Lock()
	defer r.hostsMu.Unlock()
	if r.modalityEpoch == "" {
		var epoch [16]byte
		if _, err := rand.Read(epoch[:]); err != nil {
			return refusal("unavailable")
		}
		r.modalityEpoch = hex.EncodeToString(epoch[:])
	}
	modalities := slices.Clone(SeedProfiles)
	for _, host := range r.hosts {
		if host == nil {
			continue
		}
		for _, modality := range host.HostProfiles() {
			if ValidModality(modality) {
				modalities = append(modalities, modality)
			}
		}
	}
	sort.Strings(modalities)
	modalities = slices.Compact(modalities)
	// One digest binds the runtime epoch, admitted scope and complete ordered
	// catalogue. A changed declaration cannot silently continue an old listing.
	body, _ := json.Marshal([]any{r.modalityEpoch, scope, modalities})
	sum := sha256.Sum256(body)
	prefix := "modalities:" + hex.EncodeToString(sum[:]) + ":"
	offset := 0
	if cursor != "" {
		word, ok := strings.CutPrefix(cursor, prefix)
		n, err := strconv.Atoi(word)
		if !ok || err != nil || n < 0 || n > len(modalities) || strconv.Itoa(n) != word {
			return refusal("gap")
		}
		offset = n
	}
	end := min(offset+int(limit), len(modalities))
	page := ModalityPage{Outcome: "page", Modalities: slices.Clone(modalities[offset:end]), Complete: end == len(modalities)}
	if !page.Complete {
		page.Next = prefix + strconv.Itoa(end)
	}
	return page
}
