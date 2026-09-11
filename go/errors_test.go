package router

import (
	"bufio"

	"encoding/json"
	"errors"
	"fmt"
	"github.com/openabstractions/abstraction-identity/listen"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestClientRefusalCompatibility(t *testing.T) {
	for _, tc := range []struct{ name, frame, code, message string }{
		{"legacy", `{"error":"old server refusal"}`, "", "old server refusal"},
		{"unknown", `{"code":"future_code","error":"new refusal"}`, "future_code", "new refusal"},
		{"code_only", `{"code":"future_code"}`, "future_code", ""},
		{"known", `{"code":"` + CodeUnknownOperation + `","error":"human reason"}`, CodeUnknownOperation, "human reason"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Test names can exceed Darwin's Unix socket path limit.
			dir, err := os.MkdirTemp("", "oa-rt-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(dir) })
			at := filepath.Join(dir, "s")
			if runtime.GOOS == "windows" {
				at = listen.Endpoint(fmt.Sprintf("router-codes-%d-%d", os.Getpid(), time.Now().UnixNano()))
			}
			listener, err := listen.Listen(at)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan error, 1)
			go func() {
				c, err := listener.Accept()
				if err != nil {
					done <- err
					return
				}
				defer c.Close()
				if _, err := bufio.NewReader(c).ReadString('\n'); err != nil {
					done <- err
					return
				}
				_, err = fmt.Fprintln(c, tc.frame)
				done <- err
			}()
			_, err = (&Client{Endpoint: at}).Ask(Request{})
			var remote *RemoteError
			if !errors.As(err, &remote) {
				t.Fatalf("not a remote refusal: %v", err)
			}
			if remote.Code != tc.code || remote.Message != tc.message {
				t.Fatalf("lost refusal: %+v", remote)
			}
			if err.Error() == "" {
				t.Fatal("empty refusal text")
			}
			if serverErr := <-done; serverErr != nil {
				t.Fatal(serverErr)
			}
		})
	}
}

func TestRefusalWireCompatibility(t *testing.T) {
	raw, err := json.Marshal(Response{Code: CodeUnknownOperation, Error: "unchanged"})
	if err != nil {
		t.Fatal(err)
	}
	var old struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &old); err != nil {
		t.Fatal(err)
	}
	if old.Error != "unchanged" {
		t.Fatal("old client lost error")
	}
	if (Response{}).Err() != nil {
		t.Fatal("success became refusal")
	}
}

func TestServiceRefusalHasCode(t *testing.T) {
	r := &Router{}
	out := r.Answer(Request{Op: "unknown"}, listen.Seen{})
	if out.Code != CodeUnknownOperation || out.Error == "" {
		t.Fatalf("unknown op: %+v", out)
	}
	out = r.Answer(Request{Op: OpRoute}, listen.Seen{})
	if out.Code != CodeCallerRefused || out.Error == "" {
		t.Fatalf("unbound caller: %+v", out)
	}
}

func TestWindowRefusalStatus(t *testing.T) {
	l, err := Window("127.0.0.1:0", &Router{})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	for _, tc := range []struct {
		path, code string
		status     int
	}{
		{"/route", CodeCallerRefused, http.StatusForbidden},
		{"/unknown", CodeUnknownOperation, http.StatusBadRequest},
	} {
		resp, err := http.Get("http://" + l.Addr().String() + tc.path)
		if err != nil {
			t.Fatal(err)
		}
		var out Response
		err = json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != tc.status || out.Code != tc.code || out.Error == "" {
			t.Fatalf("%s: status %d, response %+v", tc.path, resp.StatusCode, out)
		}
	}
}
