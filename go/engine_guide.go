package router

import (
	"sort"

	wire "github.com/openabstractions/abstraction-router/go/abstraction/router"
	content "github.com/openabstractions/abstraction-storage/go/abstraction/storage/content"
)

func sameModelObject(fact ModelObject, held HeldObject) bool {
	if fact.Store == held.Store && fact.Object != "" && fact.Object == held.Object {
		return true
	}
	return held.Evidence == content.EvidenceHashed && fact.Digest != "" && fact.Digest == held.Digest
}

func completeEngineModels(held []HeldObject, hosts []*Host, requireDigest bool) map[*Host]map[string]bool {
	complete := map[*Host]map[string]bool{}
	for _, host := range hosts {
		complete[host] = map[string]bool{}
		for _, fact := range host.EngineObjects {
			found := false
			for _, object := range held {
				matches := sameModelObject(fact, object)
				if requireDigest {
					matches = object.Evidence == content.EvidenceHashed && fact.Digest != "" && fact.Digest == object.Digest
				}
				found = found || matches
			}
			for _, engine := range fact.Engines {
				prior, seen := complete[host][engine.Model]
				complete[host][engine.Model] = found && (!seen || prior)
			}
		}
	}
	return complete
}

func composeEngineGuide(families []Family, held []HeldObject, hosts []*Host) []Family {
	complete := completeEngineModels(held, hosts, false)
	byFamily := map[string]int{}
	for i := range families {
		byFamily[families[i].Family] = i
	}
	for _, object := range held {
		family := object.family()
		if object.componentRole() != "" && object.base() != "" {
			family = object.base()
		}
		i, ok := byFamily[family]
		if !ok {
			continue
		}
		name := ""
		if len(object.Names) > 0 {
			name = object.Names[0]
		}
		role := object.EntryRole
		if role == "" {
			role = object.role()
		}
		row := ModelObject{Store: object.Store, Name: name, Object: object.Object, Digest: object.Digest,
			Role:  role,
			Cause: wire.EngineCauseUnknown, Why: "No provider reported an engine association for this object."}
		if object.EntryCount <= 1 {
			row.Format, row.Architecture = object.Descriptor["format"], object.Descriptor["architecture"]
		}
		if row.Object == "" && row.Digest == "" {
			row.Why = "The source has not identified this entry's content."
		} else {
			if object.Evidence != content.EvidenceHashed {
				row.Why = "The source has not verified this entry's bytes; its engine association is unknown."
			}
			for _, host := range hosts {
				for _, fact := range host.EngineObjects {
					if !sameModelObject(fact, object) {
						continue
					}
					if len(fact.Engines) == 0 || !complete[host][fact.Engines[0].Model] {
						row.Why = "A configured companion has not been identified by its source or verified digest."
						continue
					}
					row.Engines = append(row.Engines, fact.Engines...)
				}
			}
			if len(row.Engines) > 0 {
				row.Cause, row.Why = wire.EngineCauseBound, "Configured by the provider; byte verification and readiness are reported separately."
			}
		}
		families[i].Objects = append(families[i].Objects, row)
	}
	for _, host := range hosts {
		if host.remote == nil {
			continue
		}
		host.remote.mu.Lock()
		for family, objects := range host.remote.objects {
			for i := range families {
				matched := families[i].Family == family
				for _, alias := range families[i].Names {
					matched = matched || alias.Host == host.Name && has(host.remote.objectAliases[family], alias.Name)
				}
				if matched {
					families[i].Objects = append(families[i].Objects, objects...)
				}
			}
		}
		host.remote.mu.Unlock()
	}
	for i := range families {
		sort.SliceStable(families[i].Objects, func(a, b int) bool {
			x, y := families[i].Objects[a], families[i].Objects[b]
			if x.Domain != y.Domain {
				return x.Domain < y.Domain
			}
			if x.Store != y.Store {
				return x.Store < y.Store
			}
			if x.Name != y.Name {
				return x.Name < y.Name
			}
			return x.Digest < y.Digest
		})
	}
	return families
}
