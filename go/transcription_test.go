package router

import "testing"

func TestTranscriptionURLRequiresDeclaredProfileAndMapsWire(t *testing.T) {
	tests := []struct {
		name string
		host *Host
		want string
	}{
		{"lemonade", Lemonade("http://127.0.0.1:8000"), "http://127.0.0.1:8000/api/v1/audio/transcriptions"},
		{"openai", NewHosted("openai", "https://api.openai.com/v1", WireOpenAICompatible, "openai"), "https://api.openai.com/v1/audio/transcriptions"},
		{"deepgram", NewHosted("deepgram", "https://api.deepgram.com", WireDeepgramPrerecorded, "deepgram"), "https://api.deepgram.com/v1/listen"},
		{"whispercpp", WhisperCPP("http://127.0.0.1:8080"), "http://127.0.0.1:8080/inference"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.host.TranscriptionURL(); got != tt.want {
				t.Fatalf("TranscriptionURL() = %q, want %q", got, tt.want)
			}
		})
	}

	for _, h := range []*Host{LMStudio("http://127.0.0.1:1234"), Ollama("http://127.0.0.1:11434")} {
		h.Profiles = []string{ProfileChat, ProfileEmbed}
		if got := h.TranscriptionURL(); got != "" {
			t.Fatalf("%s claimed undeclared transcription endpoint %q", h.Name, got)
		}
	}
}
