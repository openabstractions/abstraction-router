package hostedcatalogue

import (
	"strings"
	"testing"
)

func TestObservedUnitsRequireExactFixedProviderBinding(t *testing.T) {
	for _, provider := range []string{"deepseek", "openrouter"} {
		b, _ := Select(provider)
		units := Observations(b.Name, b.Base, b.Kind, b.Credential)
		if !units.Requests || !units.Tokens || units.Micros != (provider == "openrouter") {
			t.Fatal(provider, units)
		}
		for _, changed := range []Binding{{Name: "custom", Base: b.Base, Kind: b.Kind, Credential: b.Credential}, {Name: b.Name, Base: "https://person.example/v1", Kind: b.Kind, Credential: b.Credential}, {Name: b.Name, Base: b.Base, Kind: "anthropic-messages", Credential: b.Credential}, {Name: b.Name, Base: b.Base, Kind: b.Kind, Credential: "personal"}} {
			if got := Observations(changed.Name, changed.Base, changed.Kind, changed.Credential); got != (ObservedUnits{Requests: true}) {
				t.Fatal("custom binding borrowed provider units", changed, got)
			}
		}
	}
}

func TestCataloguePreservesExistingRowsAndOwnsFixedBinding(t *testing.T) {
	rows, err := All()
	if err != nil || len(rows) != 4 {
		t.Fatal(rows, err)
	}
	if rows[0].ID != "deepseek" || rows[0].Base != "https://api.deepseek.com/v1" || rows[0].Budget != "tokens" || rows[1].Budget != "money" || rows[2].Kind != "download" || rows[3].Base != "" {
		t.Fatal(rows)
	}
	for _, p := range rows {
		if p.KeyFormat != "opaque" || p.DefaultBudget != 0 || p.SignIn {
			t.Fatal(p)
		}
	}
	b, err := Select("deepseek")
	if err != nil || b.Name != "deepseek" || b.Base != rows[0].Base || b.Kind != "openai-compatible" || b.Credential != "deepseek" || b.Target != "api.deepseek.com" {
		t.Fatal(b, err)
	}
	for _, id := range []string{"missing", "openai-compatible", "github-download"} {
		if _, err := Select(id); err == nil {
			t.Fatal(id)
		}
	}
	rows[0].Base = "https://attacker.invalid"
	raw := Data()
	raw[0] = '!'
	again, _ := Lookup("deepseek")
	if again.Base == rows[0].Base {
		t.Fatal("caller mutated catalogue")
	}
	if _, err := All(); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogueRejectsMalformedMetadataAndKeyTransport(t *testing.T) {
	for _, raw := range []string{string(data) + `{}`, strings.Replace(string(data), `"opaque"`, `"unknown"`, 1), strings.Replace(string(data), `"sign_in": false`, `"sign_in": true`, 1), strings.Replace(string(data), `https://api.deepseek.com/v1`, `https://person:secret@api.deepseek.com/v1`, 1)} {
		if _, err := Load([]byte(raw)); err == nil {
			t.Fatal("malformed catalogue accepted")
		}
	}
	p, _ := Lookup("deepseek")
	if !p.AcceptsKey([]byte("inert-fixture-key")) {
		t.Fatal("opaque transport rejected")
	}
	for _, key := range []string{"", "a\nb", "a b", strings.Repeat("a", 16385)} {
		if p.AcceptsKey([]byte(key)) {
			t.Fatal("unsafe key accepted")
		}
	}
}
