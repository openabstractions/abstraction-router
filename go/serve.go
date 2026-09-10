package router

import (
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"time"
)

// Serve is this layer's resident half as something another program can call.
// See asks.Serve for why the body left a main. The CLI `router` has not moved.
//
// This one must stay in the user's session whatever hosts it: the per-process
// GPU counters it reports are unreadable from session 0
// (VISION.md 2026-09-05 "A residency service must run in an interactive
// session"), so there is no admin registration of it to offer.
func Serve(args []string) error {
	fs := flag.NewFlagSet("router", flag.ContinueOnError)
	endpoint := fs.String("endpoint", DefaultEndpoint(), "pipe or socket callers connect to")
	window := fs.String("window", "", "optional read-only loopback address, e.g. 127.0.0.1:11801; carries no caller identity")
	every := fs.Duration("survey", 2*time.Second, "how often the hosts are read")
	if err := fs.Parse(args); err != nil {
		return err
	}

	r := New(Installed()...)
	s, err := Start(*endpoint, r)
	if err != nil {
		return err
	}
	defer s.Close()

	stop := make(chan struct{})
	go r.Poll(*every, stop)
	defer close(stop)
	slog.Info("router", "endpoint", *endpoint, "pid", os.Getpid())

	if *window != "" {
		l, err := Window(*window, r)
		if err != nil {
			return err
		}
		defer l.Close()
		slog.Warn("window open: read-only, unidentified callers, refuses route", "addr", l.Addr().String())
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig
	return nil
}
