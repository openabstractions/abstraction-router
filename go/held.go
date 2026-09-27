package router

import (
	"sort"
	"strings"

	"github.com/openabstractions/abstraction-model/identity"
)

// HeldObject is one thing a store holds, in that store's own words, as the
// storage inventory reports it. Names are the store's names for it, empty for
// an object no index of that store names. Digest is what the store published,
// empty when it published none. Nothing here is hashed.
//
// Descriptor is the abstraction.model/descriptor@1 the store published for it,
// nil for an object no store could name. Its "family" key is this machine's
// model identity: the router reads it and invents none of its own.
type HeldObject struct {
	Store      string
	Names      []string
	Digest     string
	Descriptor map[string]string
}

// family is what the store's descriptor calls this object, empty when the
// store published no descriptor or a descriptor that names no family.
func (o HeldObject) family() string { return o.Descriptor["family"] }

// role is the store's descriptor's role for this object, empty for a
// descriptor that named none. componentRole is empty for a plain weights
// file and the role word otherwise: a component (abstraction.model/
// descriptor@1 MODEL-C3), never a family of its own.
func (o HeldObject) role() string { return o.Descriptor["role"] }
func (o HeldObject) componentRole() string {
	if r := o.role(); r != "" && r != roleWeights {
		return r
	}
	return ""
}

// base is the family a component's descriptor said it belongs to, empty
// when its own name derived none.
func (o HeldObject) base() string { return o.Descriptor["base"] }

// roleWeights is the descriptor role value a plain weights file carries,
// mirrored from abstraction-storage-over-local-stores/go/source.RoleWeights
// rather than imported: the router reads the convention, not that module.
const roleWeights = "weights"

// SetHeld replaces what the storage inventory says this machine holds. The
// next Survey composes it into the model catalogue as held_in, and adds a
// family for every held model no host serves. A nil list clears provenance
// and leaves the catalogue the hosts alone produce.
func (r *Router) SetHeld(held []HeldObject) {
	r.heldMu.Lock()
	defer r.heldMu.Unlock()
	r.held = append([]HeldObject(nil), held...)
}

// Held is what the router was last told this machine holds.
func (r *Router) Held() []HeldObject {
	r.heldMu.Lock()
	defer r.heldMu.Unlock()
	return append([]HeldObject(nil), r.held...)
}

// provenance indexes the stores holding each family and each name match
// reaches, and the family each store's descriptor names for the content it
// holds.
type provenance struct {
	byFamily map[string]map[string]bool
	byName   map[string]map[string]bool
	// unnamed keeps one store name per family for a family no host names.
	spelling map[string]string
	// declared is the descriptor family for every key match produces from a
	// store's own name. A name two stores describe as two families is
	// removed rather than guessed at.
	declared map[string]string
	// declaredByDigest is the descriptor family for a digest a store
	// published, which is how an object no index names reaches an identity.
	declaredByDigest map[string]string
	// families are the families a descriptor declared, whatever else names
	// them; a family absent here is one a program's own name produced.
	families map[string]bool
	// components collects, per family, the role-bearing objects folded into
	// it: a projector or VAE the family's own descriptor named base, or, for
	// a component whose base could not be derived, the family the component
	// stands in for by itself (MODEL-C3). Neither kind ever also populates
	// families, so a component-only family always reads family_source alias.
	components map[string][]Component
	// nameOwner is the one store name that has claimed each key match
	// produces, lower-cased; a second, different name at the same key
	// contests it (see declareName).
	nameOwner map[string]string
}

// source says whether a descriptor named this family or a program's name did.
func (p *provenance) source(fam string) string {
	if p != nil && p.families[fam] {
		return FromDescriptor
	}
	return FromAlias
}

// declare records fam for key unless another store already declared a
// different family for it: a contested name names nothing.
func (p *provenance) declare(m map[string]string, key, fam string) {
	if key == "" || fam == "" {
		return
	}
	if seen, ok := m[key]; ok && seen != fam {
		m[key] = ""
		return
	}
	if _, ok := m[key]; !ok {
		m[key] = fam
	}
}

// match is the one rule by which a host's shorter alias reaches a store's own
// longer name: the name itself, and every tail of it that starts after a
// slash, so Ollama's inventory name library/tiny:1b reaches its server's
// tiny:1b. Both the descriptor match (declared, familyOf) and the alias match
// (byName, Alias.HeldIn) index a store's name under match's keys, so the two
// meet a host's alias the same way.
func match(name string) []string {
	out := []string{name}
	for i := 0; i < len(name); i++ {
		if name[i] == '/' && i+1 < len(name) {
			out = append(out, name[i+1:])
		}
	}
	return out
}

// familyOf is the family a store's descriptor names for a model a host serves
// under name, matched by that store's own naming. It answers false when no
// descriptor names it, and the router then keeps the host's alias.
func (p *provenance) familyOf(name string) (string, bool) {
	if p == nil {
		return "", false
	}
	if fam := p.declared[strings.ToLower(name)]; fam != "" {
		return fam, true
	}
	return "", false
}

func add(m map[string]map[string]bool, key, store string) {
	if key == "" || store == "" {
		return
	}
	if m[key] == nil {
		m[key] = map[string]bool{}
	}
	m[key][store] = true
}

// declareName folds store's held name into byName under key, the same way
// declare folds a descriptor's family into declared: key's first name claims
// it, a second store repeating that same name keeps it (two stores naming
// literally the same content), and a second, different name contests it —
// the key reaches neither store's object, the same refusal a contested
// family gets, so two objects that only coincide on a shortened alias are
// reported as unresolved rather than picked between silently.
func (p *provenance) declareName(key, name, store string) {
	if key == "" || name == "" || store == "" {
		return
	}
	if seen, ok := p.nameOwner[key]; ok && seen != name {
		p.nameOwner[key] = ""
		delete(p.byName, key)
		return
	}
	if _, ok := p.nameOwner[key]; !ok {
		p.nameOwner[key] = name
	}
	if p.nameOwner[key] == "" {
		return
	}
	add(p.byName, key, store)
}

// index reads the held objects twice: once for the names each store gives its
// own content and the family each store's descriptor declares for it, and once
// for objects a store named nothing, which reach a family only through a
// digest another store published for the same content.
//
// A store's descriptor is the identity. A store that published none leaves the
// object under the name that store uses, which is the alias fallback. A
// component (MODEL-C3) never declares a family through this path; foldComponent
// folds it instead.
func index(held []HeldObject) *provenance {
	p := &provenance{byFamily: map[string]map[string]bool{}, byName: map[string]map[string]bool{},
		spelling: map[string]string{}, declared: map[string]string{}, declaredByDigest: map[string]string{},
		families: map[string]bool{}, components: map[string][]Component{}, nameOwner: map[string]string{}}
	families := map[string][]string{}
	for _, o := range held {
		declared := o.family()
		if role := o.componentRole(); role != "" {
			p.foldComponent(o, role, declared, families)
			continue
		}
		if declared != "" {
			p.families[declared] = true
		}
		p.declare(p.declaredByDigest, o.Digest, declared)
		for _, name := range o.Names {
			lower := strings.ToLower(name)
			for _, key := range match(lower) {
				p.declare(p.declared, key, declared)
				p.declareName(key, lower, o.Store)
			}
			fam := declared
			if fam == "" {
				fam = identity.Family(name)
			}
			if fam != "" {
				add(p.byFamily, fam, o.Store)
				if _, seen := p.spelling[fam]; !seen {
					p.spelling[fam] = name
				}
				if o.Digest != "" {
					families[o.Digest] = append(families[o.Digest], fam)
				}
			}
		}
	}
	for _, o := range held {
		if len(o.Names) > 0 || o.Digest == "" {
			continue
		}
		for _, fam := range families[o.Digest] {
			add(p.byFamily, fam, o.Store)
		}
	}
	return p
}

// foldComponent records a role-bearing object under the family it belongs
// to: base when the object's descriptor derived one, its own declared family
// otherwise (MODEL-C3's fallback, "as today"). Either way p.families stays
// untouched for it, so a fallback family is never mistaken for one a store
// declared: its family_source reads alias, matching an ordinary name no
// descriptor names. byDigestFamily is index's own families map, threaded
// through so an unnamed component still reaches its target by digest the
// same way a named one does.
func (p *provenance) foldComponent(o HeldObject, role, declared string, byDigestFamily map[string][]string) {
	target := o.base()
	fallback := target == ""
	if fallback {
		target = declared
	}
	if target == "" {
		return
	}
	for _, name := range o.Names {
		lower := strings.ToLower(name)
		for _, key := range match(lower) {
			p.declare(p.declared, key, target)
			p.declareName(key, lower, o.Store)
		}
		add(p.byFamily, target, o.Store)
		if fallback {
			if _, seen := p.spelling[target]; !seen {
				p.spelling[target] = name
			}
		}
		p.components[target] = append(p.components[target], Component{Store: o.Store, Role: role, Name: name})
		if o.Digest != "" {
			byDigestFamily[o.Digest] = append(byDigestFamily[o.Digest], target)
		}
	}
	if len(o.Names) == 0 {
		p.components[target] = append(p.components[target], Component{Store: o.Store, Role: role})
	}
}

func stores(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for store := range set {
		out = append(out, store)
	}
	sort.Strings(out)
	return out
}

// compose writes held_in, family_source and components onto every family and
// alias, and appends a family for each held model no host names. A host's
// model matches a store's object by the store adapter's own naming; a
// store's own unnamed object reaches a family by the digest that store
// published for it. A role-bearing object never appends a family of its own
// through this loop: foldComponent already gave it a target, folded or
// fallback, and components names it there (MODEL-C3).
func (p *provenance) compose(families []Family) []Family {
	named := map[string]bool{}
	for i := range families {
		fam := families[i].Family
		named[fam] = true
		families[i].HeldIn = stores(p.byFamily[fam])
		families[i].FamilySource = p.source(fam)
		families[i].Components = p.componentsFor(fam)
		for j := range families[i].Names {
			families[i].Names[j].HeldIn = stores(p.byName[strings.ToLower(families[i].Names[j].Name)])
		}
	}
	for fam, name := range p.spelling {
		if named[fam] {
			continue
		}
		held := stores(p.byFamily[fam])
		families = append(families, Family{Family: fam, HeldIn: held, FamilySource: p.source(fam),
			Names: []Alias{{Name: name, HeldIn: held}}, Components: p.componentsFor(fam)})
	}
	sort.Slice(families, func(i, j int) bool { return families[i].Family < families[j].Family })
	return families
}

// componentsFor is the role-bearing objects folded into fam, store then role
// then name, or nil when none folded there.
func (p *provenance) componentsFor(fam string) []Component {
	list := p.components[fam]
	if len(list) == 0 {
		return nil
	}
	out := append([]Component(nil), list...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Store != out[j].Store {
			return out[i].Store < out[j].Store
		}
		if out[i].Role != out[j].Role {
			return out[i].Role < out[j].Role
		}
		return out[i].Name < out[j].Name
	})
	return out
}
