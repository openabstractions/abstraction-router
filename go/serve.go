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

Starts a standalone empty router, polled on an interval. The composed OA runtime
supplies configured/product-declared hosts; this standalone command reads no
registry or product records and assumes no installed third-party hosts.

  --endpoint EP   pipe or socket callers connect to (default: the installed
                  runtime's pipe for this service)
  --window ADDR   retired; serve refuses to start when it is given
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
	window := fs.String("window", "", "retired; serve refuses to start when it is given")
	every := fs.Duration("survey", 2*time.Second, "how often the hosts are read")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return usageErr
		}
		return errors.Join(fmt.Errorf("%w: %v", ErrUsage, err), usageErr)
	}

	r := New()
	if *window != "" {
		_, err := Window(*window, r)
		return err
	}
	s, err := Start(*endpoint, r)
	if err != nil {
		return err
	}
	defer s.Close()

	stop := make(chan struct{})
	go r.Poll(*every, stop)
	defer close(stop)
	slog.Info("router", "endpoint", *endpoint, "pid", os.Getpid())

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig
	return nil
}
