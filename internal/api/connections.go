package api

import (
	"net/http"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

// Connection kinds: an application connected through OAuth (a chat
// assistant) or an API token (the CLI, scripts).
const (
	connApp   = "app"
	connToken = "token"
)

// connectionDTO is a connected app or an active API token, without secrets.
type connectionDTO struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
	// Detail is the host an app returned to when connecting; null for tokens.
	Detail      *string    `json:"detail"`
	ConnectedAt time.Time  `json:"connected_at"`
	LastUsedAt  *time.Time `json:"last_used_at"`
	ExpiresAt   *time.Time `json:"expires_at"`
	// Current marks the token this request was authenticated with.
	Current bool `json:"current"`
}

func (a *api) listConnections(w http.ResponseWriter, r *http.Request) error {
	ctx, actor := r.Context(), actorFrom(r.Context())
	apps, err := a.svc.ListOAuthGrants(ctx, actor)
	if err != nil {
		return err
	}
	tokens, err := a.svc.ListTokens(ctx, actor)
	if err != nil {
		return err
	}
	items := make([]connectionDTO, 0, len(apps)+len(tokens))
	for _, g := range apps {
		items = append(items, connectionDTO{
			Kind: connApp, ID: idString(g.ID), Name: g.ClientName, Detail: optional(g.RedirectHost),
			ConnectedAt: utc(g.CreatedAt), LastUsedAt: utcPtr(g.LastUsedAt), Current: g.ID == actor.GrantID,
		})
	}
	now := a.svc.Now()
	for _, t := range tokens {
		if !t.Active(now) {
			continue
		}
		items = append(items, connectionDTO{
			Kind: connToken, ID: idString(t.ID), Name: t.Name, ConnectedAt: utc(t.CreatedAt),
			LastUsedAt: utcPtr(t.LastUsedAt), ExpiresAt: utcPtr(t.ExpiresAt), Current: t.ID == actor.TokenID,
		})
	}
	writeJSON(w, http.StatusOK, newList(items, len(items)))
	return nil
}

func (a *api) revokeConnection(w http.ResponseWriter, r *http.Request) error {
	id, ok := parseID(r.PathValue("id"))
	if !ok {
		return domain.ErrNotFound
	}
	ctx, actor := r.Context(), actorFrom(r.Context())
	var err error
	switch r.PathValue("kind") {
	case connApp:
		err = a.svc.RevokeOAuthGrant(ctx, actor, id)
	case connToken:
		err = a.svc.RevokeToken(ctx, actor, id)
	default:
		err = domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	noContent(w)
	return nil
}
