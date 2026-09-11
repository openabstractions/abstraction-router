// Package client supplies router capabilities through the framed service only.
package client

import (
	"github.com/openabstractions/abstraction-identity/listen"
	wire "github.com/openabstractions/abstraction-router/go/abstraction/router"
	"os"
	"time"
)

const EnvEndpoint = "ABSTRACTION_ROUTER_ENDPOINT"

type PickRequest = wire.PickRequest
type HostAllowance = wire.HostAllowance
type ModelsSnapshot = wire.ModelsSnapshot
type HostsSnapshot = wire.HostsSnapshot
type PickResult = wire.PickResult
type ServiceError = wire.ServiceError

type Client struct{ transport listen.FrameClient }

func DefaultEndpoint() string {
	if s := os.Getenv(EnvEndpoint); s != "" {
		return s
	}
	return listen.Endpoint("router-v1")
}
func Discover() *Client { return New(DefaultEndpoint()) }
func New(endpoint string) *Client {
	return &Client{transport: listen.FrameClient{Endpoint: endpoint, Timeout: 10 * time.Second, MaxFrame: 1 << 20}}
}
func (c *Client) Models(fresh bool) (ModelsSnapshot, error) {
	return wire.NewRouterClient(&c.transport).Models(fresh)
}
func (c *Client) Hosts(fresh bool) (HostsSnapshot, error) {
	return wire.NewRouterClient(&c.transport).Hosts(fresh)
}
func (c *Client) Pick(request PickRequest) (PickResult, error) {
	return wire.NewRouterClient(&c.transport).Pick(request)
}
