package client

import (
	"context"
	"net/http"
)

// ConnectResult is the response of exchanging a connect code.
type ConnectResult struct {
	Token Token  `json:"token"`
	Value string `json:"value"`
	User  User   `json:"user"`
}

type exchangeRequest struct {
	Code string `json:"code"`
	Name string `json:"name,omitempty"`
}

// ExchangeConnectCode trades a one-time connect code for a new API token. It
// needs no token: create the client with an empty one.
func (c *Client) ExchangeConnectCode(ctx context.Context, code, name string) (*ConnectResult, error) {
	var out ConnectResult
	if err := c.doJSON(ctx, http.MethodPost, "/connect/exchange", nil, exchangeRequest{Code: code, Name: name}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
