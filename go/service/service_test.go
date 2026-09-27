package service

import (
	"errors"
	router "github.com/openabstractions/abstraction-router/go"
	wire "github.com/openabstractions/abstraction-router/go/abstraction/router"
	"testing"
)

func TestUnboundNeverReachesProvider(t *testing.T) {
	r := receiver{provider: router.New()}
	_, err := r.views().Pick(wire.PickRequest{Model: "qwen2.5:0.5b"})
	var refused *wire.ServiceError
	if !errors.As(err, &refused) || refused.Code != router.CodeCallerRefused {
		t.Fatal(err)
	}
	if len(r.provider.Asked()) != 0 {
		t.Fatal("unbound decision audited as caller")
	}
}

func TestProviderResponseError(t *testing.T) {
	tests := []struct {
		name     string
		response router.Response
		code     wire.ServiceErrorCode
		message  string
	}{
		{name: "success"},
		{name: "legacy refusal", response: router.Response{Error: "refused"}, code: router.CodeInternal, message: "refused"},
		{name: "coded refusal", response: router.Response{Code: router.CodeInvalidRequest, Error: "invalid"}, code: router.CodeInvalidRequest, message: "invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := providerResponseError(tt.response)
			if tt.code == "" {
				if err != nil {
					t.Fatalf("providerResponseError() = %v, want nil", err)
				}
				return
			}
			var got *wire.ServiceError
			if !errors.As(err, &got) {
				t.Fatalf("providerResponseError() = %v, want *ServiceError", err)
			}
			if got.Code != tt.code || got.Message != tt.message {
				t.Fatalf("providerResponseError() = {%q, %q}, want {%q, %q}", got.Code, got.Message, tt.code, tt.message)
			}
		})
	}
}

func TestServeRejectsInvalidFlags(t *testing.T) {
	for _, args := range [][]string{{"--survey=0"}, {"--survey=-1s"}, {"extra"}} {
		if err := Serve(args); err == nil {
			t.Fatal(args)
		}
	}
}
