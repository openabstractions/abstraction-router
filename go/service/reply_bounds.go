package service

import (
	"errors"
	"log"

	identityapi "github.com/openabstractions/abstraction-identity/go/abstraction/identity/api"
	"github.com/openabstractions/abstraction-identity/go/replyfit"
	wire "github.com/openabstractions/abstraction-router/go/abstraction/router"
)

const maxReplyBytes = int(identityapi.IPCDefaultMaxFrame)

// errReplyBytes is the probe's refusal of a complete reply over the frame
// bound; replyfit reports it as the why "reply_bytes".
var errReplyBytes = errors.New("reply_bytes")

// fitReply returns a read reply the generated reader accepts. A list entry
// the reader refuses is dropped and a list that overflows the frame is cut,
// each with one log line naming the field path (replyfit). A fault outside
// every list is unavailable with reason field:<path>:<why>.
func fitReply[T any](method string, value T) (T, error) {
	fitted, err := replyfit.Fit(value, func(v T) error { return checkReply(v) }, func(line string) {
		log.Printf("router %s reply: %s", method, line)
	})
	if err != nil {
		log.Printf("router %s reply: %v", method, err)
		return fitted, &wire.ServiceError{Code: CodeUnavailable, Message: err.Error()}
	}
	return fitted, nil
}

// checkReply uses the generated writer, result validation and complete reply
// envelope. The probe returns only the already computed value: it performs no
// second provider call or policy decision. Native and paired views share it.
func checkReply(value any) error {
	probe := replyProbe{value: value}
	dispatcher := wire.RouterDispatcher{Handler: probe}
	client := wire.NewRouterClient(replyProbeTransport{dispatcher: &dispatcher})
	var err error
	switch value.(type) {
	case wire.ModelsSnapshot:
		_, err = client.Models(false)
	case wire.HostsSnapshot:
		_, err = client.Hosts(false)
	case wire.OfferSnapshot:
		_, err = client.Offer(wire.OfferRequest{Placement: wire.PlacementAny})
	case wire.OfferObservation:
		_, err = client.ObserveOffer(wire.OfferRequest{Placement: wire.PlacementAny}, "", 0)
	}
	return err
}

type replyProbeTransport struct{ dispatcher *wire.RouterDispatcher }

func (t replyProbeTransport) ExchangeFrame(frame []byte) ([]byte, error) {
	reply, err := t.dispatcher.ExchangeFrame(frame)
	if err != nil {
		return nil, err
	}
	if len(reply) > maxReplyBytes {
		return nil, errReplyBytes
	}
	return reply, nil
}

type replyProbe struct {
	wire.Router
	value any
}

func (p replyProbe) Models(bool) (wire.ModelsSnapshot, error) {
	return p.value.(wire.ModelsSnapshot), nil
}
func (p replyProbe) Hosts(bool) (wire.HostsSnapshot, error) {
	return p.value.(wire.HostsSnapshot), nil
}
func (p replyProbe) Offer(wire.OfferRequest) (wire.OfferSnapshot, error) {
	return p.value.(wire.OfferSnapshot), nil
}
func (p replyProbe) ObserveOffer(wire.OfferRequest, string, int64) (wire.OfferObservation, error) {
	return p.value.(wire.OfferObservation), nil
}
