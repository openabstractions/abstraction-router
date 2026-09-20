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
func TestServeRejectsInvalidFlags(t *testing.T) {
	for _, args := range [][]string{{"--survey=0"}, {"--survey=-1s"}, {"extra"}} {
		if err := Serve(args); err == nil {
			t.Fatal(args)
		}
	}
}
