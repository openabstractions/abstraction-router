package service

import (
	"strings"
	"testing"

	wire "github.com/openabstractions/abstraction-router/go/abstraction/router"
)

// ModalityPage is a listing: a field a newer router adds is ignored.
// PickRequest, a request, still refuses an unknown field.
func TestModalityPageIgnoresAnUnknownField(t *testing.T) {
	data, err := wire.ToJSON(wire.ModalityPage{Outcome: wire.ModalityOutcomePage, Modalities: []string{"chat", "image"}, Complete: true})
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(string(data), `"modalities":`, `"newer_hint": "x", "modalities":`, 1)
	if !strings.Contains(text, "newer_hint") {
		t.Fatalf("fixture placed no unknown field: %s", text)
	}
	var page wire.ModalityPage
	if err := wire.FromJSON([]byte(text), &page); err != nil || len(page.Modalities) != 2 || !page.Complete {
		t.Fatalf("listing with an unknown field: %+v %v\n%s", page, err, text)
	}
	request, err := wire.ToJSON(wire.PickRequest{Model: "qwen"})
	if err != nil {
		t.Fatal(err)
	}
	var pick wire.PickRequest
	if err := wire.FromJSON([]byte(strings.Replace(string(request), `"model":`, `"newer": 1, "model":`, 1)), &pick); err == nil {
		t.Fatal("a request with an unknown field decoded")
	}
}
