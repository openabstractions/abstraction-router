package router

import "testing"

func TestImageEndpointsRequireExplicitProfile(t *testing.T) {
	cases := []struct {
		name string
		host *Host
		want string
	}{
		{"openai", NewHosted("openai", "https://api.openai.com/v1", WireOpenAICompatible, "openai"), "https://api.openai.com/v1/images"},
		{"stability", NewHosted("stability", "https://api.stability.ai", WireStabilityV2Beta, "stability"), "https://api.stability.ai/v2beta/stable-image"},
		{"fal", NewHosted("fal", "https://queue.fal.run", WireFalQueue, "fal"), "https://queue.fal.run/"},
		{"replicate", NewHosted("replicate", "https://api.replicate.com", WireReplicatePredictions, "replicate"), "https://api.replicate.com/v1/predictions"},
		{"comfyui", ComfyUI("http://127.0.0.1:8188"), "http://127.0.0.1:8188/prompt"},
		{"swarmui", SwarmUI("http://127.0.0.1:7801"), "http://127.0.0.1:7801/API/GenerateText2Image"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.host.ImageURL(); got != tc.want {
				t.Fatalf("ImageURL %q", got)
			}
			if !has(tc.host.HostProfiles(), ProfileImage) {
				t.Fatalf("profiles %v", tc.host.HostProfiles())
			}
		})
	}
	notImage := NewHosted("anthropic", "https://api.anthropic.com", WireAnthropicMessages, "anthropic")
	if got := notImage.ImageURL(); got != "" {
		t.Fatalf("anthropic image URL %q", got)
	}
}
