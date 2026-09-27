package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHostedListingDoesNotForwardCredentialOnRedirect(t *testing.T) {
	reached := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		if got := r.Header.Get("X-Api-Key"); got != "" {
			t.Errorf("redirect target received credential %q", got)
		}
		w.Write([]byte(`{"data":[]}`))
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/models", http.StatusFound)
	}))
	defer origin.Close()

	_, err := listIDs(context.Background(), origin.URL+"/models", map[string]string{"X-Api-Key": "listing-secret"})
	if err == nil {
		t.Fatal("credential-bearing redirect was followed")
	}
	if reached {
		t.Fatal("redirect target was reached")
	}
	if strings.Contains(err.Error(), "listing-secret") {
		t.Fatalf("redirect refusal exposed credential: %v", err)
	}

	// The other hosted listing shape uses the same redirect rule.
	reached = false
	_, err = listArrayIDs(context.Background(), origin.URL+"/models", map[string]string{"X-Api-Key": "array-secret"})
	if err == nil || reached {
		t.Fatalf("array listing followed credential redirect: reached=%v err=%v", reached, err)
	}

	// Public listings retain ordinary redirect behavior.
	reached = false
	if _, err := listIDs(context.Background(), origin.URL+"/models", nil); err != nil || !reached {
		t.Fatalf("public listing redirect: reached=%v err=%v", reached, err)
	}
}
