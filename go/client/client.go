// Package client supplies router capabilities through the framed service only.
package client

import (
	"context"
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
	return NewWithTransport(listen.FrameClient{Endpoint: endpoint})
}

// NewWithTransport retains the caller's endpoint, server trust and waiting limits.
func NewWithTransport(transport listen.FrameClient) *Client {
	return &Client{transport: transport.WithDefaults(10*time.Second, 1<<20)}
}
func (c *Client) Models(fresh bool) (ModelsSnapshot, error) {
	return c.ModelsContext(context.Background(), fresh)
}

// ModelsContext uses ctx only for this operation.
func (c *Client) ModelsContext(ctx context.Context, fresh bool) (ModelsSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return ModelsSnapshot{}, err
	}
	return wire.NewRouterClient(c.transport.WithContext(ctx)).Models(fresh)
}
func (c *Client) Hosts(fresh bool) (HostsSnapshot, error) {
	return c.HostsContext(context.Background(), fresh)
}

// HostsContext uses ctx only for this operation.
func (c *Client) HostsContext(ctx context.Context, fresh bool) (HostsSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return HostsSnapshot{}, err
	}
	return wire.NewRouterClient(c.transport.WithContext(ctx)).Hosts(fresh)
}
func (c *Client) Pick(request PickRequest) (PickResult, error) {
	return c.PickContext(context.Background(), request)
}

// PickContext uses ctx only for this operation.
func (c *Client) PickContext(ctx context.Context, request PickRequest) (PickResult, error) {
	if err := ctx.Err(); err != nil {
		return PickResult{}, err
	}
	return wire.NewRouterClient(c.transport.WithContext(ctx)).Pick(request)
}
