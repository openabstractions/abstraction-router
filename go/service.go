package router

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"time"

	identity "github.com/openabstractions/abstraction-identity"
	"github.com/openabstractions/abstraction-identity/listen"
)

const maxFrame = 64 << 10

type Service struct {
	Endpoint string
	r        *Router
	l        listen.Listener
}

// Start refuses to run at all on a machine that cannot enforce the identity this
// service's answers are attributed to. A router that cannot say who asked keeps
// an audit of nobody.
func Start(endpoint string, r *Router) (*Service, error) {
	if err := identity.CanEver(Bound); err != nil {
		return nil, fmt.Errorf("this machine cannot bind a caller to a connection: %w", err)
	}
	l, err := listen.Listen(endpoint)
	if err != nil {
		return nil, err
	}
	s := &Service{Endpoint: endpoint, r: r, l: l}
	go s.loop()
	return s, nil
}

func (s *Service) Close() error { return s.l.Close() }

func (s *Service) loop() {
	for {
		c, err := s.l.Accept()
		if errors.Is(err, net.ErrClosed) {
			return
		}
		if err != nil {
			slog.Warn("accept", "err", err)
			continue
		}
		go s.serve(c)
	}
}

func (s *Service) serve(c listen.Conn) {
	defer c.Close()
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, maxFrame), maxFrame)
	if !sc.Scan() {
		return
	}
	frame := append([]byte(nil), sc.Bytes()...)

	// Windows will not identify the client of a pipe nothing has been read
	// from, so the frame arrives before the caller does. It stays bytes until
	// the caller is known, and it is never read as a claim about who sent it.
	b, err := c.Bind()
	seen := listen.SeenBy(b, err)
	if err != nil {
		reply(c, Response{Code: CodeCallerRefused, Caller: seen, Error: "refused: " + seen.String()})
		return
	}
	defer b.Close()
	if err := b.Check(Bound); err != nil {
		reply(c, Response{Code: CodeCallerRefused, Caller: seen, Error: "refused: " + err.Error()})
		return
	}

	var req Request
	if err := json.Unmarshal(frame, &req); err != nil {
		reply(c, Response{Code: CodeInvalidRequest, Caller: seen, Error: "not a request: " + err.Error()})
		return
	}
	reply(c, s.r.Answer(req, seen))
}

func reply(w io.Writer, r Response) {
	raw, err := json.Marshal(r)
	if err != nil {
		return
	}
	w.Write(append(raw, '\n'))
}

// Answer is the whole interface. Every path into this service goes through it,
// so an entry point that cannot name its caller refuses in one place rather than
// each remembering to.
func (r *Router) Answer(req Request, caller listen.Seen) Response {
	start := time.Now()
	out := Response{Caller: caller}
	took := func(at time.Time) Response {
		out.TookMS = time.Since(start).Milliseconds()
		out.AgeMS = start.Sub(at).Milliseconds()
		return out
	}
	switch req.Op {
	case OpModels:
		fams, at := r.Models(req.Fresh)
		out.Models = fams
		return took(at)
	case OpResidency:
		hosts, gpu, why, dbl, at := r.Residency(req.Fresh)
		out.Hosts, out.GPU, out.GPUWhy, out.Doubled, out.Asked = hosts, gpu, why, dbl, r.Asked()
		return took(at)
	case OpRoute:
		if !caller.Bound {
			out.Code = CodeCallerRefused
			out.Error = "refused: a routing decision is attributed to the program that asked for it, and " + caller.Why
			return took(time.Now())
		}
		d, at := r.Route(req)
		out.Decision = d
		r.record(Ask{At: time.Now(), Caller: caller.Path, User: caller.User,
			Model: req.Model, Family: d.Family, Verdict: d.Verdict, Host: d.Host})
		return took(at)
	}
	out.Code = CodeUnknownOperation
	out.Error = fmt.Sprintf("unknown op %q; this service answers %q, %q and %q and nothing else",
		req.Op, OpModels, OpResidency, OpRoute)
	return took(time.Now())
}
