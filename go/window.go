package router

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"

	"github.com/openabstractions/abstraction-identity/listen"
)

// Unidentified is the caller a loopback socket produces. Any process on this
// machine can open one and the kernel vouches for none of them, so it is the
// same state a failed bind produces and it goes through the same refusal.
var Unidentified = listen.Seen{Why: "a loopback socket carries no caller identity: any process on this machine can open one"}

// Window is a read-only view for things that cannot speak the pipe — a browser,
// curl, a dashboard. It is off unless an address is given, it answers only the
// two observing questions, and every answer says its caller is unknown. It
// cannot ask for a decision, which is the one operation that is recorded against
// a program.
func Window(addr string, r *Router) (net.Listener, error) {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		op := strings.TrimPrefix(req.URL.Path, "/")
		out := r.Answer(Request{Op: op, Model: req.URL.Query().Get("model"),
			Fresh: req.URL.Query().Has("fresh")}, Unidentified)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case out.Code == CodeCallerRefused:
			w.WriteHeader(http.StatusForbidden)
		case out.Err() != nil:
			w.WriteHeader(http.StatusBadRequest)
		}
		json.NewEncoder(w).Encode(out)
	})
	go http.Serve(l, mux)
	return l, nil
}
