package service

import (
	"context"
	"errors"
	"flag"
	"fmt"
	router "github.com/openabstractions/abstraction-router/go"
	"github.com/openabstractions/abstraction-router/go/client"
	"io"
	"os"
	"os/signal"
	"time"
)

const routerV1Usage = `Usage: openabstractions serve router-v1 [options]

Answers model discovery and routing requests over the framed service.

  --endpoint EP   framed router endpoint (default: the installed runtime's
                  pipe for this service)
  --survey DUR    how often existing hosts are read (default: 2s)

Exit codes: 0 a clean stop, 1 startup failure, 2 usage error.
`

func isHelp(arg string) bool { return arg == "--help" || arg == "-h" || arg == "help" }

// ErrUsage wraps an error Serve returns for a bad flag or argument, after it
// has already printed routerV1Usage. A caller that hosts several
// capabilities tells this apart from a startup failure with
// errors.Is(err, ErrUsage).
var ErrUsage = errors.New("router-v1: usage error")

// Serve is the framed service, registered separately as router-v1 during migration.
// It stays in the user session, like the existing provider.
func Serve(args []string) error {
	if len(args) > 0 && isHelp(args[0]) {
		_, err := io.WriteString(os.Stdout, routerV1Usage)
		return err
	}
	fs := flag.NewFlagSet("router-v1", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	// routerV1Usage already documents every option; the fallback on a
	// genuine parse error prints it once, not a second time as
	// flag.PrintDefaults' own single-dash listing.
	var usageErr error
	fs.Usage = func() { _, usageErr = fmt.Fprint(fs.Output(), routerV1Usage) }
	endpoint := fs.String("endpoint", client.DefaultEndpoint(), "framed router endpoint")
	every := fs.Duration("survey", 2*time.Second, "how often existing hosts are read")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return usageErr
		}
		return errors.Join(fmt.Errorf("%w: %v", ErrUsage, err), usageErr)
	}
	if fs.NArg() != 0 {
		//unchecked: stderr is diagnostic only; ErrUsage below preserves the failed invocation
		fmt.Fprint(os.Stderr, routerV1Usage)
		return fmt.Errorf("%w: router-v1: unexpected arguments", ErrUsage)
	}
	if *every <= 0 {
		//unchecked: stderr is diagnostic only; ErrUsage below preserves the failed invocation
		fmt.Fprint(os.Stderr, routerV1Usage)
		return fmt.Errorf("%w: router-v1: survey duration must be positive", ErrUsage)
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
