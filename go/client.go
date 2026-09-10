package router

import (
	"bufio"
	"encoding/json"
	"errors"

	"github.com/openabstractions/abstraction-identity/listen"
)

type Client struct{ Endpoint string }

func DefaultEndpoint() string { return listen.Endpoint("router") }

func (c *Client) Ask(req Request) (Response, error) {
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
	if out.Error != "" {
		return out, errors.New(out.Error)
	}
	return out, nil
}
