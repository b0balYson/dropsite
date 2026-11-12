package api

import (
	"context"
	"net/http"
)

type exchangeRequest struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type exchangeResponse struct {
	Token tokenDTO `json:"token"`
	Value string   `json:"value"`
	User  userDTO  `json:"user"`
}

// exchangeConnect trades a connect code for an API token. It needs no bearer
// token: the one-time code is the credential.
func (a *api) exchangeConnect(w http.ResponseWriter, r *http.Request) error {
	var req exchangeRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	if req.Code == "" {
		return invalidField("code", "code is required")
	}
	res, err := a.svc.ExchangeConnectCode(r.Context(), req.Code, req.Name, infoFrom(r.Context()).ip)
	if err != nil {
		return err
	}
	infoFrom(r.Context()).userID = res.User.ID
	writeJSON(w, http.StatusCreated, exchangeResponse{
		Token: toToken(res.Token.Token), Value: res.Token.Value, User: toUser(res.User),
	})
	return nil
}

// Mount is an extra handler served on the public listener next to the API
// (MCP, OAuth). It shares the API middleware: request ID, access log, client
// IP from trusted proxies and panic recovery.
type Mount struct {
	Pattern string
	Handler http.Handler
}

// RequestIP returns the client IP the API middleware resolved for a request
// served by a Mount ("" outside the middleware).
func RequestIP(ctx context.Context) string {
	if info := infoFrom(ctx); info != nil {
		return info.ip
	}
	return ""
}

// SetRequestUser records the authenticated user for the access log.
func SetRequestUser(ctx context.Context, userID int64) {
	if info := infoFrom(ctx); info != nil {
		info.userID = userID
	}
}
