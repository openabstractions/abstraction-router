package service

import (
	"context"
	"flag"
	"fmt"
	router "github.com/openabstractions/abstraction-router/go"
	"github.com/openabstractions/abstraction-router/go/client"
	"os"
	"os/signal"
	"time"
)

// Serve is the framed service, registered separately as router-v1 during migration.
// It stays in the user session, like the existing provider.
func Serve(args []string) error {
	fs := flag.NewFlagSet("router-v1", flag.ContinueOnError)
	endpoint := fs.String("endpoint", client.DefaultEndpoint(), "framed router endpoint")
	every := fs.Duration("survey", 2*time.Second, "how often existing hosts are read")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("router-v1: unexpected arguments")
	}
	if *every <= 0 {
		return fmt.Errorf("router-v1: survey duration must be positive")
	}
	r := router.New(router.Installed()...)
	h, err := Listen(*endpoint, r)
	if err != nil {
		return err
	}
	defer h.Close()
	r.Survey()
	stop := make(chan struct{})
	defer close(stop)
	go r.Poll(*every, stop)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	h.OnError = func(err error) { fmt.Fprintln(os.Stderr, "router-v1:", err) }
	fmt.Fprintln(os.Stdout, "router-v1: listening", *endpoint)
	return h.Serve(ctx)
}
