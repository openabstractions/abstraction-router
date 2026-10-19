package router

import (
	"errors"
	"net"

	"github.com/openabstractions/abstraction-identity/listen"
)

// Unidentified is the attribution of a caller no binding names.
var Unidentified = listen.Seen{Why: "a loopback socket carries no caller identity: any process on this machine can open one"}

var errWindowRetired = errors.New("router: the loopback window is retired: a loopback TCP caller cannot be bound at the proof this service requires (identity listen.Program); use the router endpoint")

// Window returns the retirement error without opening a listener.
// Deprecated: a loopback TCP caller cannot be bound at the proof this service
// requires (identity listen.Program); use the router endpoint.
func Window(addr string, r *Router) (net.Listener, error) {
	return nil, errWindowRetired
}
