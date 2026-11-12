package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

// adminOnly answers 404 to non-admins before any input is parsed, so that a
// malformed query cannot reveal that an admin endpoint exists. The service
// enforces the same rule.
func adminOnly(next handlerFunc) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		if !actorFrom(r.Context()).IsAdmin() {
			return domain.ErrNotFound
		}
		return next(w, r)
	}
}

func (a *api) listUsers(w http.ResponseWriter, r *http.Request) error {
	f, err := userFilter(r.URL.Query())
	if err != nil {
		return err
	}
	rows, total, err := a.svc.ListUsers(r.Context(), actorFrom(r.Context()), f)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, newList(toUserItems(rows), total))
	return nil
}

func userFilter(q url.Values) (service.UserFilter, error) {
	p, err := page(q)
	if err != nil {
		return service.UserFilter{}, err
	}
	f := service.UserFilter{Query: q.Get("q"), Page: p}
	switch st := domain.UserStatus(q.Get("status")); st {
	case "", domain.UserInvited, domain.UserActive, domain.UserBlocked:
		f.Status = st
	default:
		return f, invalidField("status", "status must be invited, active or blocked")
	}
	return f, nil
}

type createUserRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

func (a *api) createUser(w http.ResponseWriter, r *http.Request) error {
	var req createUserRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	inv, err := a.svc.CreateUser(r.Context(), actorFrom(r.Context()), req.Email, domain.Role(req.Role))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, toInvite(inv))
	return nil
}

type updateUserRequest struct {
	Blocked       *bool      `json:"blocked"`
	ReissueInvite *bool      `json:"reissue_invite"`
	Limits        *overrides `json:"limits"`
}

func (a *api) getUser(w http.ResponseWriter, r *http.Request) error {
	id, ok := parseID(r.PathValue("id"))
	if !ok {
		return domain.ErrNotFound
	}
	ul, err := a.svc.GetUserLimits(r.Context(), actorFrom(r.Context()), id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toUserDetail(ul))
	return nil
}

func (a *api) setUserLimits(w http.ResponseWriter, r *http.Request, id int64, o overrides) error {
	ul, err := a.svc.SetUserLimits(r.Context(), actorFrom(r.Context()), id, o.domain())
	if err != nil {
		return err
	}
	lim, ov := toLimits(ul.Effective), toOverrides(ul.Overrides)
	writeJSON(w, http.StatusOK, inviteDTO{User: toUser(ul.User), Limits: &lim, LimitOverrides: &ov})
	return nil
}

func (a *api) updateUser(w http.ResponseWriter, r *http.Request) error {
	id, ok := parseID(r.PathValue("id"))
	if !ok {
		return domain.ErrNotFound
	}
	var req updateUserRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	n := 0
	for _, set := range []bool{req.Blocked != nil, req.ReissueInvite != nil, req.Limits != nil} {
		if set {
			n++
		}
	}
	switch {
	case n != 1:
	case req.Blocked != nil:
		return a.setBlocked(w, r, id, *req.Blocked)
	case req.ReissueInvite != nil && *req.ReissueInvite:
		return a.reissueInvite(w, r, id)
	case req.Limits != nil:
		return a.setUserLimits(w, r, id, *req.Limits)
	}
	return invalid(`send exactly one of {"blocked": bool}, {"reissue_invite": true} or {"limits": {...}}`)
}

func (a *api) setBlocked(w http.ResponseWriter, r *http.Request, id int64, blocked bool) error {
	u, err := a.svc.SetUserBlocked(r.Context(), actorFrom(r.Context()), id, blocked)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, inviteDTO{User: toUser(u)})
	return nil
}

func (a *api) reissueInvite(w http.ResponseWriter, r *http.Request, id int64) error {
	inv, err := a.svc.ReissueInvite(r.Context(), actorFrom(r.Context()), id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toInvite(inv))
	return nil
}

func (a *api) listAllSites(w http.ResponseWriter, r *http.Request) error {
	p, err := page(r.URL.Query())
	if err != nil {
		return err
	}
	rows, total, err := a.svc.ListAllSites(r.Context(), actorFrom(r.Context()), r.URL.Query().Get("q"), p)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, newList(toSiteItems(rows), total))
	return nil
}

func (a *api) setApex(w http.ResponseWriter, r *http.Request) error {
	v, err := a.svc.SetApex(r.Context(), actorFrom(r.Context()), r.PathValue("ref"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toSite(v))
	return nil
}

func (a *api) clearApex(w http.ResponseWriter, r *http.Request) error {
	v, err := a.svc.ClearApex(r.Context(), actorFrom(r.Context()), r.PathValue("ref"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toSite(v))
	return nil
}

// transferRequest accepts user_id as a string (as IDs are sent) or a number.
type transferRequest struct {
	UserID json.RawMessage `json:"user_id"`
}

func (req transferRequest) userID() (int64, error) {
	raw := bytes.Trim(req.UserID, `"`)
	id, ok := parseID(string(raw))
	if !ok {
		return 0, invalidField("user_id", "user_id must be a user ID")
	}
	return id, nil
}

func (a *api) transfer(w http.ResponseWriter, r *http.Request) error {
	var req transferRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	id, err := req.userID()
	if err != nil {
		return err
	}
	v, err := a.svc.TransferSite(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toSite(v))
	return nil
}

func (a *api) listAudit(w http.ResponseWriter, r *http.Request) error {
	f, err := auditFilter(r.URL.Query())
	if err != nil {
		return err
	}
	rows, total, err := a.svc.ListAudit(r.Context(), actorFrom(r.Context()), f)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, newList(toAudit(rows), total))
	return nil
}

func auditFilter(q url.Values) (service.AuditFilter, error) {
	p, err := page(q)
	if err != nil {
		return service.AuditFilter{}, err
	}
	f := service.AuditFilter{Action: q.Get("action"), Page: p}
	if v := q.Get("actor_id"); v != "" {
		id, ok := parseID(v)
		if !ok {
			return f, invalidField("actor_id", "actor_id must be a user ID")
		}
		f.ActorID = &id
	}
	if f.From, err = auditTime(q.Get("from"), "from", false); err != nil {
		return f, err
	}
	if f.To, err = auditTime(q.Get("to"), "to", true); err != nil {
		return f, err
	}
	return f, nil
}

// auditTime parses RFC 3339 or YYYY-MM-DD (UTC). The upper bound is
// exclusive, so a bare date in "to" includes that whole day.
func auditTime(v, field string, upper bool) (*time.Time, error) {
	if v == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		t = t.UTC()
		return &t, nil
	}
	t, err := time.Parse(time.DateOnly, v)
	if err != nil {
		return nil, invalidField(field, field+" must be an RFC 3339 time or a YYYY-MM-DD date")
	}
	if upper {
		t = t.AddDate(0, 0, 1)
	}
	return &t, nil
}
