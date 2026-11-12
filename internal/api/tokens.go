package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

// maxTokenDays bounds expires_in_days (ten years).
const maxTokenDays = 3650

func (a *api) listTokens(w http.ResponseWriter, r *http.Request) error {
	ts, err := a.svc.ListTokens(r.Context(), actorFrom(r.Context()))
	if err != nil {
		return err
	}
	items := make([]tokenDTO, 0, len(ts))
	for _, t := range ts {
		items = append(items, toToken(t))
	}
	writeJSON(w, http.StatusOK, newList(items, len(items)))
	return nil
}

type createTokenRequest struct {
	Name          string `json:"name"`
	ExpiresInDays *int   `json:"expires_in_days"`
}

func (req createTokenRequest) ttl() (time.Duration, error) {
	if req.ExpiresInDays == nil {
		return 0, nil
	}
	days := *req.ExpiresInDays
	if days < 1 || days > maxTokenDays {
		return 0, invalidField("expires_in_days", "expires_in_days must be from 1 to "+strconv.Itoa(maxTokenDays))
	}
	return time.Duration(days) * 24 * time.Hour, nil
}

func (a *api) createToken(w http.ResponseWriter, r *http.Request) error {
	var req createTokenRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	ttl, err := req.ttl()
	if err != nil {
		return err
	}
	nt, err := a.svc.CreateToken(r.Context(), actorFrom(r.Context()), req.Name, ttl)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, newTokenDTO{Token: toToken(nt.Token), Value: nt.Value})
	return nil
}

func (a *api) revokeToken(w http.ResponseWriter, r *http.Request) error {
	id, ok := parseID(r.PathValue("id"))
	if !ok {
		return domain.ErrNotFound
	}
	if err := a.svc.RevokeToken(r.Context(), actorFrom(r.Context()), id); err != nil {
		return err
	}
	noContent(w)
	return nil
}

// parseID parses a positive numeric ID from a path or body.
func parseID(s string) (int64, bool) {
	id, err := strconv.ParseInt(s, 10, 64)
	return id, err == nil && id > 0
}
