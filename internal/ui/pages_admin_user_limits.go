package ui

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

type limitField struct {
	Key        string
	Value      string // the override, "" when inherited
	Global     string // the value inherited without an override
	Effective  string
	Bytes      bool
	Min        int
	Overridden bool
}

type userLimitsData struct {
	User   domain.User
	Fields []limitField
}

// overrideOf returns the override slot for a setting key.
func overrideOf(o *domain.LimitOverrides, key string) **int64 {
	switch key {
	case service.SettingMaxSiteBytes:
		return &o.MaxSiteBytes
	case service.SettingMaxFileBytes:
		return &o.MaxFileBytes
	case service.SettingMaxFiles:
		return &o.MaxFiles
	case service.SettingMaxSitesPerUser:
		return &o.MaxSitesPerUser
	default:
		return &o.KeepReleases
	}
}

func (s *server) userLimitsPage(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r, rc)
		return
	}
	ul, err := s.svc.GetUserLimits(r.Context(), rc.actor, id)
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	data := userLimitsData{User: ul.User}
	for _, f := range numericSettings {
		minimum := 1
		if f.key == service.SettingKeepReleases {
			minimum = 2
		}
		field := limitField{
			Key: f.key, Bytes: f.bytes, Min: minimum,
			Global:    formatSetting(rc.loc, f.bytes, f.get(ul.Global)),
			Effective: formatSetting(rc.loc, f.bytes, f.get(ul.Effective)),
		}
		if v := *overrideOf(&ul.Overrides, f.key); v != nil {
			field.Value, field.Overridden = formatSetting(rc.loc, f.bytes, *v), true
		}
		data.Fields = append(data.Fields, field)
	}
	s.page(w, r, rc, http.StatusOK, "admin_user_limits", "users", "limits.title", data)
}

// saveUserLimits replaces the overrides with the submitted values; an empty
// field or a ticked reset box inherits the global value.
func (s *server) saveUserLimits(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r, rc)
		return
	}
	back := "/admin/users/" + strconv.FormatInt(id, 10) + "/limits"
	var o domain.LimitOverrides
	for _, f := range numericSettings {
		raw := strings.ReplaceAll(strings.TrimSpace(r.PostFormValue(f.key)), ",", ".")
		if raw == "" || r.PostFormValue("reset_"+f.key) == "1" {
			continue
		}
		v, ok := parseSetting(raw, f.bytes)
		if !ok {
			s.setFlash(w, r, flashError, rc.loc.T("settings.notnumber", "field", rc.loc.T("settings."+f.key)))
			redirect(w, r, back)
			return
		}
		*overrideOf(&o, f.key) = &v
	}
	ul, err := s.svc.SetUserLimits(r.Context(), rc.actor, id, o)
	if err != nil {
		s.fail(w, r, rc, err, back)
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("limits.saved", "email", ul.User.Email))
	redirect(w, r, back)
}
