package router

import (
	"context"
	"slices"
	"testing"

	identity "github.com/openabstractions/abstraction-identity"
	"github.com/openabstractions/abstraction-identity/listen"
	wire "github.com/openabstractions/abstraction-router/go/abstraction/router"
)

// lemonadeImageEntries is the installed Lemonade declaration's model list:
// the picture model for image, Whisper for transcription and Kokoro for
// speech (installer/declarations/lemonade-image.json).
var lemonadeImageEntries = []string{"image:user.LocalApprovedKlein", "transcription:Whisper-Large-v3-Turbo", "speech:kokoro-v1"}

var lemonadeImageProfiles = []string{ProfileImage, ProfileTranscription, ProfileSpeech}

func lemonadeImageRouter(t *testing.T) (*Router, *Host) {
	t.Helper()
	expect := listen.ServerExpectation{Principal: identity.User{Kind: "posix", UID: 1}, Program: "/lemonaded"}
	h, err := NewNative("lemonade-image", "lemonade-image", expect, lemonadeImageEntries, lemonadeImageProfiles)
	if err != nil {
		t.Fatal(err)
	}
	r := New(h)
	if err := r.SurveyContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	return r, h
}

// ROUTE-D9: a model is reachable only through the profiles that name it. An
// image request naming the speech model reads no_host, the word ROUTE-E1
// gives a model every holder serves for a different profile only.
func TestImageRequestNamingSpeechModelReadsNoHost(t *testing.T) {
	r, _ := lemonadeImageRouter(t)
	for _, refused := range []struct{ profile, model string }{
		{ProfileImage, "kokoro-v1"},
		{ProfileImage, "Whisper-Large-v3-Turbo"},
		{ProfileSpeech, "user.LocalApprovedKlein"},
		{ProfileTranscription, "kokoro-v1"},
	} {
		d, _ := r.Route(Request{Model: refused.model, Profile: refused.profile})
		if d.Verdict != NoHost || d.Host != "" {
			t.Fatalf("%s %s: verdict %s host %q (%s), want %s", refused.profile, refused.model, d.Verdict, d.Host, d.Why, NoHost)
		}
	}
	for _, served := range []struct{ profile, model string }{
		{ProfileImage, "user.LocalApprovedKlein"},
		{ProfileTranscription, "Whisper-Large-v3-Turbo"},
		{ProfileSpeech, "kokoro-v1"},
	} {
		d, _ := r.Route(Request{Model: served.model, Profile: served.profile})
		if d.Verdict != WouldLoad || d.Host != "lemonade-image" || d.Model != served.model {
			t.Fatalf("%s %s: verdict %s host %q model %q (%s)", served.profile, served.model, d.Verdict, d.Host, d.Model, d.Why)
		}
	}
}

// ROUTE-D8 with ROUTE-D9: an offer for one profile lists only the models
// named for it.
func TestOfferListsOnlyModelsNamedForTheProfile(t *testing.T) {
	r, _ := lemonadeImageRouter(t)
	want := map[string][]string{
		ProfileImage:         {"user.LocalApprovedKlein"},
		ProfileTranscription: {"Whisper-Large-v3-Turbo"},
		ProfileSpeech:        {"kokoro-v1"},
		ProfileChat:          nil,
	}
	for profile, names := range want {
		offers, _, err := r.Offer(Request{Op: OpOffer, Profile: profile, Placement: PlacementAny})
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, o := range offers {
			got = append(got, o.Names...)
		}
		if !slices.Equal(got, names) {
			t.Fatalf("offer %s: %v, want %v", profile, got, names)
		}
	}
}

// Models reports each native alias with the profiles named for it, the
// answer a remote runtime's reader judges it by.
func TestNativeAliasesReportTheirProfiles(t *testing.T) {
	r, _ := lemonadeImageRouter(t)
	families, _ := r.Models(false)
	got := map[string][]string{}
	for _, f := range families {
		for _, a := range f.Names {
			got[a.Name] = a.Profiles
		}
	}
	for name, profiles := range map[string][]string{"user.LocalApprovedKlein": {ProfileImage}, "Whisper-Large-v3-Turbo": {ProfileTranscription}, "kokoro-v1": {ProfileSpeech}} {
		if !slices.Equal(got[name], profiles) {
			t.Fatalf("%s profiles %v, want %v", name, got[name], profiles)
		}
	}
}

// An entry without a declared profile prefix serves every profile the
// declaration lists, the reading of every declaration written before
// ROUTE-D9; a colon inside a model name is part of the name.
func TestUnscopedEntriesServeEveryDeclaredProfile(t *testing.T) {
	names, byProfile := DeclaredModels([]string{"qwen3:8b", "embed:nomic", "live:other"}, []string{ProfileChat, ProfileEmbed})
	if !slices.Equal(names, []string{"qwen3:8b", "nomic", "live:other"}) {
		t.Fatalf("names %v", names)
	}
	if !slices.Equal(byProfile[ProfileChat], []string{"qwen3:8b", "live:other"}) || !slices.Equal(byProfile[ProfileEmbed], []string{"qwen3:8b", "nomic", "live:other"}) {
		t.Fatalf("by profile %v", byProfile)
	}
	expect := listen.ServerExpectation{Principal: identity.User{Kind: "posix", UID: 1}, Program: "/provider"}
	h, err := NewNative("flat", "flat", expect, []string{"fixture-model"}, []string{ProfileChat, ProfileEmbed})
	if err != nil {
		t.Fatal(err)
	}
	r := New(h)
	r.Survey()
	for _, profile := range []string{ProfileChat, ProfileEmbed} {
		if d, _ := r.Route(Request{Model: "fixture-model", Profile: profile}); d.Verdict != WouldLoad {
			t.Fatalf("%s: %s (%s)", profile, d.Verdict, d.Why)
		}
		if !h.ServesModel(profile, "fixture-model") {
			t.Fatalf("ServesModel(%s) false for a flat entry", profile)
		}
	}
	if h.ServesModel(ProfileImage, "fixture-model") {
		t.Fatal("ServesModel answered a profile the host does not declare")
	}
}

// DeclaredEntry is the one entry parser: a prefix scopes only when it is a
// served profile and a model follows, and a model literally named chat:x is
// written chat:chat:x.
func TestDeclaredEntryParsesOneEntry(t *testing.T) {
	served := []string{ProfileChat, ProfileImage}
	for entry, want := range map[string][2]string{
		"image:picture": {"picture", ProfileImage},
		"chat:chat:x":   {"chat:x", ProfileChat},
		"chat:x":        {"x", ProfileChat},
		"speech:voice":  {"speech:voice", ""},
		"image:":        {"image:", ""},
		"qwen3:8b":      {"qwen3:8b", ""},
		"plain":         {"plain", ""},
	} {
		if model, profile := DeclaredEntry(entry, served); model != want[0] || profile != want[1] {
			t.Errorf("%q: %q %q, want %q %q", entry, model, profile, want[0], want[1])
		}
	}
}

func TestServesModelReadsTheDeclaration(t *testing.T) {
	_, h := lemonadeImageRouter(t)
	if !h.ServesModel(ProfileSpeech, "kokoro-v1") || h.ServesModel(ProfileImage, "kokoro-v1") || h.ServesModel(ProfileImage, "unknown") {
		t.Fatal("ServesModel does not follow the per-profile declaration")
	}
	open := OpenAICompatible("open", "http://127.0.0.1:9", "/v1", []string{"m"})
	open.Profiles = []string{ProfileImage, ProfileSpeech}
	open.ScopeModels(map[string][]string{ProfileImage: {"m"}})
	if !open.ServesModel(ProfileImage, "m") || open.ServesModel(ProfileSpeech, "m") {
		t.Fatal("ScopeModels did not scope an OpenAI-compatible host")
	}
	r := New(open)
	r.Survey()
	if d, _ := r.Route(Request{Model: "m", Profile: ProfileSpeech}); d.Verdict != NoHost {
		t.Fatalf("scoped host served an unnamed profile: %s", d.Verdict)
	}
}

// A native provider is reached at abstraction.inference/<profile>@1 for each
// profile it declares, audio included; it decides its own formats.
func TestNativeHostAnswersAudioAddresses(t *testing.T) {
	_, h := lemonadeImageRouter(t)
	if got := h.TranscriptionURL(); got != "abstraction.inference/transcription@1" {
		t.Fatalf("TranscriptionURL %q", got)
	}
	if got := h.SpeechURL(); got != "abstraction.inference/speech@1" {
		t.Fatalf("SpeechURL %q", got)
	}
	for _, format := range []string{"mp3", "opus", "aac", "flac", "wav", "pcm"} {
		if !h.SupportsSpeechFormat(format) {
			t.Fatalf("native host refused format %s", format)
		}
	}
	expect := listen.ServerExpectation{Principal: identity.User{Kind: "posix", UID: 1}, Program: "/provider"}
	chat, err := NewNative("chat", "chat", expect, []string{"m"}, []string{ProfileChat})
	if err != nil {
		t.Fatal(err)
	}
	if chat.TranscriptionURL() != "" || chat.SpeechURL() != "" || chat.SupportsSpeechFormat("wav") {
		t.Fatal("a chat-only native host answered an audio address")
	}
}

// A remote with one profiled alias and one unprofiled: the unprofiled alias
// serves every profile declared for the remote, as it did before ROUTE-D9,
// and the profiled one only its own (review of 48d44c5b7, finding 1).
func TestMixedRemoteServesUnprofiledAliasForDeclaredProfiles(t *testing.T) {
	fake := &fakeRemote{models: wire.ModelsSnapshot{Models: []wire.Family{
		{Family: "kokoro-v1", Names: []wire.Alias{{Host: "lemonade-image", Name: "kokoro-v1", Servable: true, Profiles: []string{ProfileSpeech}}}},
		{Family: "llama", Names: []wire.Alias{{Host: "ollama", Name: "llama", Servable: true}}},
	}}}
	far := remoteHost("far", "far.test:443", "", func(context.Context) remoteRouter { return fake })
	far.Profiles = []string{ProfileChat, ProfileSpeech}
	r := New(far)
	if err := r.SurveyContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ profile, model, verdict string }{
		{ProfileChat, "llama", Hosted},
		{ProfileSpeech, "llama", Hosted},
		{ProfileSpeech, "kokoro-v1", Hosted},
		{ProfileChat, "kokoro-v1", NoHost},
	} {
		if d, _ := r.Route(Request{Model: c.model, Profile: c.profile}); d.Verdict != c.verdict {
			t.Fatalf("%s %s: %s (%s), want %s", c.profile, c.model, d.Verdict, d.Why, c.verdict)
		}
	}
	families, _ := r.Models(false)
	for _, f := range families {
		for _, a := range f.Names {
			if a.Name == "llama" && !slices.Equal(a.Profiles, []string{ProfileChat, ProfileSpeech}) {
				t.Fatalf("unprofiled remote alias profiles %v, want the declared ones", a.Profiles)
			}
		}
	}
}

// ROUTE-S4 with ROUTE-D9: a remote runtime's aliases carry the profiles its
// own Models reports, bounded by the profiles this runtime declared for it.
func TestRemoteAliasesKeepTheirProfiles(t *testing.T) {
	fake := &fakeRemote{models: wire.ModelsSnapshot{Models: []wire.Family{
		{Family: "user.localapprovedklein", Names: []wire.Alias{{Host: "lemonade-image", Name: "user.LocalApprovedKlein", Servable: true, Profiles: []string{ProfileImage}}}},
		{Family: "kokoro-v1", Names: []wire.Alias{{Host: "lemonade-image", Name: "kokoro-v1", Servable: true, Profiles: []string{ProfileSpeech}}}},
		{Family: "older", Names: []wire.Alias{{Host: "lemonade-image", Name: "older", Servable: true}}},
	}}}
	far := remoteHost("far", "far.test:443", "", func(context.Context) remoteRouter { return fake })
	far.Profiles = []string{ProfileImage, ProfileSpeech}
	r := New(far)
	if err := r.SurveyContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		profile, model, verdict string
	}{
		{ProfileImage, "kokoro-v1", NoHost},
		{ProfileSpeech, "kokoro-v1", Hosted},
		{ProfileImage, "user.LocalApprovedKlein", Hosted},
		// An alias that reports no profiles is judged by the remote host's.
		{ProfileImage, "older", Hosted},
		{ProfileSpeech, "older", Hosted},
	}
	for _, c := range cases {
		d, _ := r.Route(Request{Model: c.model, Profile: c.profile})
		if d.Verdict != c.verdict {
			t.Fatalf("%s %s: %s (%s), want %s", c.profile, c.model, d.Verdict, d.Why, c.verdict)
		}
	}
	// A profile this runtime did not declare for the remote stays closed,
	// whatever the remote reports.
	far.Profiles = []string{ProfileImage}
	r.Survey()
	if d, _ := r.Route(Request{Model: "kokoro-v1", Profile: ProfileSpeech}); d.Verdict == Hosted {
		t.Fatalf("remote served an undeclared profile: %s", d.Why)
	}
}
