package router

import (
	"errors"
	"strings"
	"testing"
)

type fakeResidency struct {
	rows  []Holder
	err   error
	asked int
}

func (f *fakeResidency) Holders(bool) ([]Holder, error) {
	f.asked++
	return f.rows, f.err
}

// The router reports the rows its source gives it and asks once per survey.
func TestResidencyComesFromTheSource(t *testing.T) {
	source := &fakeResidency{rows: []Holder{
		{Program: `C:\lms\llama-server.exe`, Account: "S-1-5-21-7-1001", GiB: 17.29},
		{Program: `C:\comfy\python.exe`, GiB: 5.02},
	}}
	r := New()
	r.SetResidency(source)
	r.Survey()
	_, holders, why, _, _ := r.Residency(false)
	if why != "" {
		t.Fatalf("a source that answered left a reason: %q", why)
	}
	if len(holders) != 2 || holders[0].Program != `C:\lms\llama-server.exe` || holders[0].GiB != 17.29 {
		t.Fatalf("holders %+v", holders)
	}
	if source.asked != 1 {
		t.Fatalf("one survey asked the source %d times", source.asked)
	}
}

// A router nobody wired a table to reports no holder and says why, rather
// than an empty card.
func TestResidencyWithoutASourceSaysSo(t *testing.T) {
	r := New()
	r.Survey()
	_, holders, why, _, _ := r.Residency(false)
	if len(holders) != 0 {
		t.Fatalf("holders with no source: %+v", holders)
	}
	if why != noResidencySource {
		t.Fatalf("reason %q", why)
	}
}

// A source that refuses is reported as a reason, and the answer keeps every
// other thing the survey read.
func TestResidencyReportsWhyTheSourceRefused(t *testing.T) {
	r := New()
	r.SetResidency(&fakeResidency{err: errors.New("the resource table is not resolved")})
	r.Survey()
	_, holders, why, _, _ := r.Residency(false)
	if len(holders) != 0 || !strings.Contains(why, "not resolved") {
		t.Fatalf("holders %+v, why %q", holders, why)
	}
}
