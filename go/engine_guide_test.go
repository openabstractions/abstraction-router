package router

import (
	"context"
	"errors"
	"testing"

	wire "github.com/openabstractions/abstraction-router/go/abstraction/router"
	content "github.com/openabstractions/abstraction-storage/go/abstraction/storage/content"
)

func TestEngineGuideKeepsVariantsAndUnknownMetadataDistinct(t *testing.T) {
	held := []HeldObject{
		{Store: "models", Object: "approved", Digest: "sha256:a", Names: []string{"model-Q4.gguf"}, Descriptor: map[string]string{"family": "model", "format": "gguf"}},
		{Store: "models", Object: "other", Digest: "sha256:b", Names: []string{"model-Q8.gguf"}, Descriptor: map[string]string{"family": "model", "format": "gguf"}},
	}
	host := &Host{EngineObjects: []ModelObject{{Store: "models", Object: "approved", Engines: []ModelEngine{{Host: "modelhost", Model: "model", Engine: "llamacpp", Source: "generation-one"}}}}}
	got := composeEngineGuide([]Family{{Family: "model"}}, held, []*Host{host})[0].Objects
	if len(got) != 2 || got[0].Cause != wire.EngineCauseBound || got[1].Cause != wire.EngineCauseUnknown {
		t.Fatalf("family equality became compatibility: %+v", got)
	}
	if got[0].Architecture != "" || got[1].Architecture != "" {
		t.Fatal("extension invented architecture")
	}
	after := composeEngineGuide([]Family{{Family: "model"}}, held, nil)[0].Objects
	if len(after[0].Engines) != 0 || after[0].Cause != wire.EngineCauseUnknown {
		t.Fatalf("withdrawn registration survived: %+v", after)
	}
}

func TestEngineGuideRequiresEveryConfiguredCompanion(t *testing.T) {
	association := []ModelEngine{{Host: "lemonade-image", Model: "picture", Engine: "sdcpp", Source: "recipe"}}
	host := &Host{EngineObjects: []ModelObject{{Digest: "sha256:main", Engines: association}, {Digest: "sha256:encoder", Engines: association}}}
	held := []HeldObject{{Digest: "sha256:main", Evidence: content.EvidenceHashed, EntryRole: "weights", Descriptor: map[string]string{"family": "picture"}}}
	got := composeEngineGuide([]Family{{Family: "picture"}}, held, []*Host{host})[0].Objects
	if len(got[0].Engines) != 0 || got[0].Cause != wire.EngineCauseUnknown || got[0].Why != "A configured companion has not been identified by its source or verified digest." {
		t.Fatalf("missing companion accepted: %+v", got)
	}
	held = append(held, HeldObject{Digest: "sha256:encoder", Evidence: content.EvidenceHashed, EntryRole: "text_encoder", Descriptor: map[string]string{"family": "picture"}})
	for index := range held {
		held[index].EntryCount = 2
		held[index].Descriptor["format"] = "collection-format"
		held[index].Descriptor["architecture"] = "collection-architecture"
	}
	got = composeEngineGuide([]Family{{Family: "picture"}}, held, []*Host{host})[0].Objects
	if len(got) != 2 || got[0].Cause != wire.EngineCauseBound || got[1].Cause != wire.EngineCauseBound {
		t.Fatalf("complete recipe lost its entries: %+v", got)
	}
	if got[0].Format != "" || got[1].Architecture != "" {
		t.Fatal("collection metadata became a companion's metadata")
	}
}

func TestEngineGuideRecipeDigestRequiresHashedEvidence(t *testing.T) {
	association := []ModelEngine{{Host: "lemonade", Model: "picture", Engine: "sdcpp", Source: "recipe"}}
	host := &Host{EngineObjects: []ModelObject{{Digest: "sha256:main", Engines: association}, {Digest: "sha256:encoder", Engines: association}}}
	for _, evidence := range []content.Evidence{content.EvidenceNamed, content.EvidenceForeign, content.EvidenceHashed} {
		t.Run(evidence.String(), func(t *testing.T) {
			held := []HeldObject{
				{Store: "source", Names: []string{"main"}, Digest: "sha256:main", Evidence: content.EvidenceHashed, Descriptor: map[string]string{"family": "picture"}},
				{Store: "source", Names: []string{"encoder"}, Digest: "sha256:encoder", Evidence: evidence, Descriptor: map[string]string{"family": "picture"}},
			}
			got := composeEngineGuide([]Family{{Family: "picture"}}, held, []*Host{host})[0].Objects
			if len(got) != 2 {
				t.Fatalf("entries lost: %+v", got)
			}
			for _, row := range got {
				if (len(row.Engines) > 0) != (evidence == content.EvidenceHashed) {
					t.Fatalf("unverified companion completed recipe: %+v", got)
				}
			}
		})
	}
	for _, evidence := range []content.Evidence{content.EvidenceNamed, content.EvidenceForeign} {
		held := []HeldObject{{Store: "source", Object: "opaque", Digest: "sha256:main", Evidence: evidence, Descriptor: map[string]string{"family": "picture"}}}
		digestOnly := &Host{EngineObjects: []ModelObject{{Digest: "sha256:main", Engines: association}}}
		got := composeEngineGuide([]Family{{Family: "picture"}}, held, []*Host{digestOnly})[0].Objects[0]
		if len(got.Engines) != 0 || got.Cause != wire.EngineCauseUnknown {
			t.Fatalf("%s digest became verified: %+v", evidence, got)
		}
		exact := &Host{EngineObjects: []ModelObject{{Store: "source", Object: "opaque", Digest: "sha256:main", Engines: association}}}
		got = composeEngineGuide([]Family{{Family: "picture"}}, held, []*Host{exact})[0].Objects[0]
		if len(got.Engines) != 1 || got.Cause != wire.EngineCauseBound || got.Why != "Configured by the provider; byte verification and readiness are reported separately." {
			t.Fatalf("exact source association lost or overstated: %+v", got)
		}
		exact.EngineObjects[0].Store = "another-source"
		if got = composeEngineGuide([]Family{{Family: "picture"}}, held, []*Host{exact})[0].Objects[0]; len(got.Engines) != 0 {
			t.Fatalf("object ID crossed source owner: %+v", got)
		}
	}
}

type engineGuideRemote struct {
	models wire.ModelsSnapshot
	hosts  wire.HostsSnapshot
	err    error
}

func (r *engineGuideRemote) Models(bool) (wire.ModelsSnapshot, error) { return r.models, r.err }
func (r *engineGuideRemote) Hosts(bool) (wire.HostsSnapshot, error)   { return r.hosts, r.err }
func (r *engineGuideRemote) Offer(wire.OfferRequest) (wire.OfferSnapshot, error) {
	return wire.OfferSnapshot{}, r.err
}

func TestEngineGuideRemoteKeepsRemoteFactsAndOlderAbsence(t *testing.T) {
	peer := &engineGuideRemote{models: wire.ModelsSnapshot{Models: []wire.Family{{Family: "model", Objects: []wire.ModelObject{{Store: "remote-store", Digest: "sha256:remote", Cause: wire.EngineCauseBound,
		Engines: []wire.ModelEngine{{Host: "peer-provider", Engine: "remote-engine", Source: "remote-generation"}}}}}}},
		hosts: wire.HostsSnapshot{Hosts: []wire.HostState{{Host: "peer-provider", Engines: []wire.Engine{{ID: "remote-engine", Name: "Remote engine", Version: "remote-version", Source: "remote-generation"}}}}}}
	host := remoteHost("lab", "inert", "", func(context.Context) remoteRouter { return peer })
	if _, err := host.listing(context.Background(), host, nil); err != nil {
		t.Fatal(err)
	}
	got := composeEngineGuide([]Family{{Family: "model"}}, nil, []*Host{host})[0].Objects
	if len(got) != 1 || got[0].Domain != "lab" || got[0].Engines[0].Host != "lab/peer-provider" || got[0].Engines[0].Engine != "remote-engine" {
		t.Fatalf("remote facts guessed locally: %+v", got)
	}
	peer.models.Models[0].Names = []wire.Alias{{Name: "opaque-alias", Servable: true}}
	if _, err := host.listing(context.Background(), host, nil); err != nil {
		t.Fatal(err)
	}
	byAlias := composeEngineGuide([]Family{{Family: "local-alias-spelling", Names: []Alias{{Host: "lab", Name: "opaque-alias"}}}}, nil, []*Host{host})[0].Objects
	if len(byAlias) != 1 || byAlias[0].Engines[0].Engine != "remote-engine" {
		t.Fatalf("explicit remote alias relation lost: %+v", byAlias)
	}
	states, err := host.remoteHosts(context.Background())
	if err != nil || states[0].Engines[0].Version != "remote-version" {
		t.Fatalf("remote engine version lost: %+v %v", states, err)
	}
	peer.models.Models[0].Objects = nil
	if _, err := host.listing(context.Background(), host, nil); err != nil {
		t.Fatal(err)
	}
	if got := composeEngineGuide([]Family{{Family: "model"}}, nil, []*Host{host})[0].Objects; len(got) != 0 {
		t.Fatalf("older remote acquired invented facts: %+v", got)
	}
	peer.err = errors.New("unreachable")
	if _, err := host.listing(context.Background(), host, nil); err == nil {
		t.Fatal("remote failure lost")
	}
}
