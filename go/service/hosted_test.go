package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	identity "github.com/openabstractions/abstraction-identity"
	router "github.com/openabstractions/abstraction-router/go"
	"github.com/openabstractions/abstraction-router/go/client"
)

const secret = "sk-FIXTURE-hosted-19ab"

func endpointFor(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return fmt.Sprintf(`\\.\pipe\router-hosted-test-%d-%d`, os.Getpid(), time.Now().UnixNano())
	}
	dir, err := os.MkdirTemp("/tmp", "router-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "s")
}

// TestHostedHostOverIPC lists a fake hosted host through the framed router
// service: the generated snapshots mark the host and its aliases hosted, name
// the wire and credential, and carry no header value; a pick restricted to the
// hosted host reads hosted with no endpoint.
func TestHostedHostOverIPC(t *testing.T) {
	if err := identity.CanEver(router.Bound); err != nil {
		t.Skip(err)
	}
	var mu sync.Mutex
	var authorizations []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Write([]byte(`{"data":[{"id":"anthropic/claude-sonnet-5"}]}`))
	}))
	defer upstream.Close()
	r := router.New(router.NewHosted("openrouter", upstream.URL+"/api/v1", router.WireOpenAICompatible, "openrouter"))
	r.UseCredentials(func(_ context.Context, consumer, name, target string) (map[string]string, error) {
		if consumer != router.CredentialConsumer || name != "openrouter" || target != "127.0.0.1" {
			return nil, &router.CredentialRefusal{Outcome: "target_refused", Name: name}
		}
		return map[string]string{"Authorization": "Bearer " + secret}, nil
	})
	endpoint := endpointFor(t)
	h, err := Listen(endpoint, r)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.Serve(ctx) }()
	defer func() { cancel(); <-done }()

	c := client.New(endpoint)
	hosts, err := c.Hosts(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts.Hosts) != 1 || !hosts.Hosts[0].Up || !hosts.Hosts[0].Hosted || hosts.Hosts[0].Wire != "openai-compatible" || hosts.Hosts[0].Credential != "openrouter" {
		t.Fatalf("hosts snapshot: %+v", hosts.Hosts)
	}
	models, err := c.Models(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(models.Models) != 1 || len(models.Models[0].Names) != 1 || !models.Models[0].Names[0].Hosted {
		t.Fatalf("models snapshot: %+v", models.Models)
	}
	pick, err := c.Pick(client.PickRequest{Model: "anthropic/claude-sonnet-5", Allowed: &client.HostAllowance{Hosts: []string{"openrouter"}}})
	if err != nil {
		t.Fatal(err)
	}
	if pick.Decision.Verdict != router.Hosted || pick.Decision.Host != "openrouter" || pick.Decision.Endpoint != "" {
		t.Fatalf("pick: %+v", pick.Decision)
	}
	// Profiles cross the wire: the host lists its default profiles, and a pick
	// for a profile it does not declare reads no-host.
	if got := strings.Join(hosts.Hosts[0].Profiles, ","); got != "chat,embed,transcription,speech,image" {
		t.Fatalf("host profiles %q", got)
	}
	r.Hosts()[0].Profiles = []string{router.ProfileChat}
	if speech, err := c.Pick(client.PickRequest{Model: "anthropic/claude-sonnet-5", Profile: router.ProfileSpeech}); err != nil || speech.Decision.Verdict != router.NoHost {
		t.Fatalf("speech pick: %+v %v", speech.Decision, err)
	}
	raw, _ := json.Marshal([]any{hosts, models, pick})
	if strings.Contains(string(raw), secret) {
		t.Fatalf("a snapshot carries the header value: %s", raw)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(authorizations) != 1 || authorizations[0] != "Bearer "+secret {
		t.Fatalf("upstream saw Authorization %q", authorizations)
	}
}
