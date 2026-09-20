package router

import "testing"

func TestSpeechURLAndFormatsRequireExplicitBackendSupport(t *testing.T) {
	tests := []struct {
		name string
		host *Host
		url  string
	}{
		{"lemonade", Lemonade("http://127.0.0.1:8000"), "http://127.0.0.1:8000/api/v1/audio/speech"},
		{"openai", NewHosted("openai", "https://api.openai.com/v1", WireOpenAICompatible, "openai"), "https://api.openai.com/v1/audio/speech"},
		{"piper", Piper("http://127.0.0.1:5000"), "http://127.0.0.1:5000/synthesize"},
		{"elevenlabs", NewHosted("elevenlabs", "https://api.elevenlabs.io", WireElevenLabsStream, "elevenlabs"), "https://api.elevenlabs.io/v1/text-to-speech"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.host.SpeechURL(); got != tt.url {
				t.Fatalf("SpeechURL()=%q want %q", got, tt.url)
			}
		})
	}
	if Piper("http://localhost").SupportsSpeechFormat("mp3") || !Piper("http://localhost").SupportsSpeechFormat("wav") {
		t.Fatal("Piper must advertise WAV only")
	}
	eleven := NewHosted("elevenlabs", "https://api.elevenlabs.io", WireElevenLabsStream, "elevenlabs")
	if !eleven.SupportsSpeechFormat("opus") || eleven.SupportsSpeechFormat("aac") {
		t.Fatal("ElevenLabs format mapping is too broad")
	}
	for _, h := range []*Host{LMStudio("http://localhost"), Ollama("http://localhost")} {
		h.Profiles = []string{ProfileChat, ProfileEmbed}
		if h.SpeechURL() != "" {
			t.Fatalf("%s claimed undeclared speech", h.Name)
		}
	}
}
