package router

import (
	"net"
	"net/url"
)

// OnThisMachine identifies a host whose execution endpoint is demonstrably
// local for permission-tier decisions. A local-placement declaration alone
// does not prove locality: operator and product HTTP bases may name a remote
// machine. Native providers use authenticated local IPC; the image-engine
// adapter runs in this process. HTTP endpoints must name a literal loopback IP.
func (h *Host) OnThisMachine() bool {
	if h == nil || h.Hosted || h.Wire == WireRemote || h.Domain != "" {
		return false
	}
	if h.native != nil {
		return h.Wire == WireNative
	}
	if h.Wire == WireImageEngine {
		return true
	}
	u, err := url.Parse(h.Base)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Hostname() == "" {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback()
}
