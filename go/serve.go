package router

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"time"
)

const routerUsage = `Usage: openabstractions serve router [options]

Reports the model hosts running on this machine, polled on an interval.

  --endpoint EP   pipe or socket callers connect to (default: the installed
                  runtime's pipe for this service)
  --window ADDR   optional read-only loopback address, e.g. 127.0.0.1:11801;
                  carries no caller identity (default: closed)
  --survey DUR    how often the hosts are read (default: 2s)

Exit codes: 0 a clean stop, 1 startup failure, 2 usage error.
`

func isHelp(arg string) bool { return arg == "--help" || arg == "-h" || arg == "help" }

// ErrUsage wraps an error Serve returns for a bad flag or argument, after it
// has already printed routerUsage. A caller that hosts several capabilities
// tells this apart from a startup failure with errors.Is(err, ErrUsage).
var ErrUsage = errors.New("router: usage error")

// Serve is this layer's resident half as something another program can call.
// See asks.Serve for why the body left a main. The CLI `router` has not moved.
//
// This one must stay in the user's session whatever hosts it: it reads the
// hosts a person started, and the resource table it would read residency from
// reads per-process counters that are unreadable from session 0. There is no
// admin registration of it to offer. Standing alone it wires no residency
// source, and its residency answer says so; the runtime wires the table.
func Serve(args []string) error {
	if len(args) > 0 && isHelp(args[0]) {
		_, err := io.WriteString(os.Stdout, routerUsage)
		return err
	}
	fs := flag.NewFlagSet("router", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	// routerUsage already documents every option; the fallback on a genuine
	// parse error prints it once, not a second time as flag.PrintDefaults'
	// own single-dash listing.
	var usageErr error
	fs.Usage = func() { _, usageErr = fmt.Fprint(fs.Output(), routerUsage) }
	endpoint := fs.String("endpoint", DefaultEndpoint(), "pipe or socket callers connect to")
	window := fs.String("window", "", "optional read-only loopback address, e.g. 127.0.0.1:11801; carries no caller identity")
	every := fs.Duration("survey", 2*time.Second, "how often the hosts are read")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return usageErr
		}
		return errors.Join(fmt.Errorf("%w: %v", ErrUsage, err), usageErr)
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
