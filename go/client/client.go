// Package client supplies router capabilities through the framed service only.
package client

import (
	"context"
	"errors"
	"fmt"
	"github.com/openabstractions/abstraction-facade/go-core/bootstrap"
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
type OfferRequest = wire.OfferRequest
type OfferSnapshot = wire.OfferSnapshot
type OfferObservation = wire.OfferObservation
type ModalityPage = wire.ModalityPage
type ServiceError = wire.ServiceError
type Engine = wire.Engine
type EngineCause = wire.EngineCause
type ModelEngine = wire.ModelEngine
type ModelObject = wire.ModelObject

const (
	EngineCauseBound        = wire.EngineCauseBound
	EngineCauseSourceOnly   = wire.EngineCauseSourceOnly
	EngineCauseIncompatible = wire.EngineCauseIncompatible
	EngineCauseUnknown      = wire.EngineCauseUnknown
	EngineCauseOther        = wire.EngineCauseOther
)

// EngineCauseValues returns every member in declaration order, in a new slice.
func EngineCauseValues() []EngineCause { return wire.EngineCauseValues() }

type Client struct {
	transport listen.FrameClient
	initErr   error
}

// DefaultEndpoint returns empty when the account namespace cannot be resolved.
func DefaultEndpoint() string { endpoint, _ := checkedDefaultEndpoint(); return endpoint }
func checkedDefaultEndpoint() (string, error) {
	if endpoint := os.Getenv(EnvEndpoint); endpoint != "" {
		return endpoint, nil
	}
	return bootstrap.InstalledEndpoint("router-v1")
}
func emptyEndpointError() error {
	_, err := checkedDefaultEndpoint()
	if err != nil {
		return err
	}
	return fmt.Errorf("router: empty endpoint")
}

func Discover() *Client {
	endpoint, err := checkedDefaultEndpoint()
	c := New(endpoint)
	c.initErr = err
	return c
}
func New(endpoint string) *Client {
	c := NewWithTransport(listen.FrameClient{Endpoint: endpoint})
	if endpoint == "" {
		c.initErr = emptyEndpointError()
	}
	return c
}

// NewWithTransport retains the caller's endpoint, server trust and waiting limits.
func NewWithTransport(transport listen.FrameClient) *Client {
	c := &Client{transport: transport.WithDefaults(10*time.Second, 1<<20)}
	if transport.Endpoint == "" && transport.Dialer == nil {
		c.initErr = emptyEndpointError()
	}
	return c
}
func (c *Client) Models(fresh bool) (ModelsSnapshot, error) {
	return c.ModelsContext(context.Background(), fresh)
}

// ModelsContext uses ctx only for this operation.
func (c *Client) ModelsContext(ctx context.Context, fresh bool) (ModelsSnapshot, error) {
	if c.initErr != nil {
		return ModelsSnapshot{}, c.initErr
	}
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
	if c.initErr != nil {
		return HostsSnapshot{}, c.initErr
	}
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
	if c.initErr != nil {
		return PickResult{}, c.initErr
	}
	if err := ctx.Err(); err != nil {
		return PickResult{}, err
	}
	return wire.NewRouterClient(c.transport.WithContext(ctx)).Pick(request)
}
func (c *Client) Offer(request OfferRequest) (OfferSnapshot, error) {
	return c.OfferContext(context.Background(), request)
}

// OfferContext uses ctx only for this operation.
func (c *Client) OfferContext(ctx context.Context, request OfferRequest) (OfferSnapshot, error) {
	if c.initErr != nil {
		return OfferSnapshot{}, c.initErr
	}
	if err := ctx.Err(); err != nil {
		return OfferSnapshot{}, err
	}
	snapshot, err := wire.NewRouterClient(c.transport.WithContext(ctx)).Offer(request)
	if err != nil {
		return OfferSnapshot{}, err
	}
	return validateOfferSnapshot(snapshot)
}

func (c *Client) ObserveOffer(request OfferRequest, cursor string, waitMs int64) (OfferObservation, error) {
	return c.ObserveOfferContext(context.Background(), request, cursor, waitMs)
}

// ObserveOfferContext uses ctx only for this operation. The call may take its
// wait beyond the client's ordinary time limit.
func (c *Client) ObserveOfferContext(ctx context.Context, request OfferRequest, cursor string, waitMs int64) (OfferObservation, error) {
	if c.initErr != nil {
		return OfferObservation{}, c.initErr
	}
	if err := ctx.Err(); err != nil {
		return OfferObservation{}, err
	}
	transport := c.transport
	if waitMs > 0 {
		transport.Timeout += time.Duration(waitMs) * time.Millisecond
	}
	observation, err := wire.NewRouterClient(transport.WithContext(ctx)).ObserveOffer(request, cursor, waitMs)
	if err != nil {
		return OfferObservation{}, err
	}
	if !observation.Outcome.Known() {
		return OfferObservation{}, errors.New("router: invalid offer outcome")
	}
	if observation.Outcome != wire.OfferOutcomeOffered && (len(observation.Offers) != 0 || observation.Observation != (wire.Observation{}) || observation.Cursor != "") {
		return OfferObservation{}, errors.New("router: refused offer carries data")
	}
	return observation, nil
}

func validateOfferSnapshot(snapshot OfferSnapshot) (OfferSnapshot, error) {
	if !snapshot.Outcome.Known() {
		return OfferSnapshot{}, errors.New("router: invalid offer outcome")
	}
	if snapshot.Outcome != wire.OfferOutcomeOffered && (len(snapshot.Offers) != 0 || snapshot.Observation != (wire.Observation{})) {
		return OfferSnapshot{}, errors.New("router: refused offer carries data")
	}
	return snapshot, nil
}

func (c *Client) Modalities(cursor string, limit int64) (ModalityPage, error) {
	return c.ModalitiesContext(context.Background(), cursor, limit)
}
func (c *Client) ModalitiesContext(ctx context.Context, cursor string, limit int64) (ModalityPage, error) {
	if c.initErr != nil {
		return ModalityPage{}, c.initErr
	}
	if err := ctx.Err(); err != nil {
		return ModalityPage{}, err
	}
	page, err := wire.NewRouterClient(c.transport.WithContext(ctx)).Modalities(cursor, limit)
	if err != nil {
		return ModalityPage{}, err
	}
	if !page.Outcome.Known() || (page.Outcome != wire.ModalityOutcomePage && (len(page.Modalities) != 0 || page.Next != "" || page.Complete)) {
		return ModalityPage{}, errors.New("router: invalid modality page")
	}
	return page, nil
}
