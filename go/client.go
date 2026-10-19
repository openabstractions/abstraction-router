package router

import (
	"bufio"
	"encoding/json"
	"errors"

	"github.com/openabstractions/abstraction-facade/go-core/bootstrap"
	"github.com/openabstractions/abstraction-identity/listen"
)

type Client struct{ Endpoint string }

// DefaultEndpoint returns empty when the account namespace is unavailable.
func DefaultEndpoint() string { value, _ := bootstrap.Endpoint("router"); return value }
func missingEndpointError() error {
	_, err := bootstrap.Endpoint("router")
	if err != nil {
		return err
	}
	return errors.New("router: empty endpoint")
}

func (c *Client) Ask(req Request) (Response, error) {
	if c.Endpoint == "" {
		return Response{}, missingEndpointError()
	}
	nc, err := listen.Dial(c.Endpoint)
	if err != nil {
		return Response{}, errors.New("router: no service at " + c.Endpoint + " (" + err.Error() + ")")
	}
	defer nc.Close()
	raw, err := json.Marshal(req)
	if err != nil {
		return Response{}, err
	}
	if _, err := nc.Write(append(raw, '\n')); err != nil {
		return Response{}, err
	}
	sc := bufio.NewScanner(nc)
	sc.Buffer(make([]byte, maxFrame), maxFrame)
	if !sc.Scan() {
		return Response{}, errors.New("router: the service closed the connection")
	}
	var out Response
	if err := json.Unmarshal(sc.Bytes(), &out); err != nil {
		return Response{}, err
	}
	if err := out.Err(); err != nil {
		return out, err
	}
	return out, nil
}
