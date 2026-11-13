package ui

import (
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

// numericSetting describes one numeric field of the settings form.
type numericSetting struct {
	key   string
	bytes bool // shown in megabytes
	get   func(domain.Limits) int64
	set   func(*service.SettingsUpdate, *int64)
}

var numericSettings = []numericSetting{
	{service.SettingMaxSiteBytes, true, func(l domain.Limits) int64 { return l.MaxSiteBytes },
		func(u *service.SettingsUpdate, v *int64) { u.MaxSiteBytes = v }},
	{service.SettingMaxFileBytes, true, func(l domain.Limits) int64 { return l.MaxFileBytes },
		func(u *service.SettingsUpdate, v *int64) { u.MaxFileBytes = v }},
	{service.SettingMaxFiles, false, func(l domain.Limits) int64 { return int64(l.MaxFiles) },
		func(u *service.SettingsUpdate, v *int64) { u.MaxFiles = v }},
	{service.SettingMaxSitesPerUser, false, func(l domain.Limits) int64 { return int64(l.MaxSitesPerUser) },
		func(u *service.SettingsUpdate, v *int64) { u.MaxSitesPerUser = v }},
	{service.SettingKeepReleases, false, func(l domain.Limits) int64 { return int64(l.KeepReleases) },
		func(u *service.SettingsUpdate, v *int64) { u.KeepReleases = v }},
}

type settingField struct {
	Key        string
	Value      string
	Default    string
	Bytes      bool
	Min        int
	Overridden bool
}

type settingsData struct {
	Fields    []settingField
	Extra     string
	ExtraOver bool
	Builtin   []string
	EnvSlugs  []string
	Counters  domain.Counters
	Forms     service.FormSettings
	Sheets    service.SheetsStatus
	// PublicAdmin is public access to the admin interface.
	PublicAdmin service.PublicAdminStatus
}

func formatSetting(l *locale, bytes bool, v int64) string {
	if bytes {
		return l.MB(v)
	}
	return strconv.FormatInt(v, 10)
}

func (s *server) settingsPage(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	st, err := s.svc.GetSettings(r.Context(), rc.actor)
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	forms, err := s.svc.GetFormSettings(r.Context(), rc.actor)
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	sheets, err := s.svc.GetSheetsStatus(r.Context())
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	public, err := s.svc.GetPublicAdmin(r.Context(), rc.actor)
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	defaults := s.svc.Config().Limits
	data := settingsData{
		Extra: strings.Join(st.ExtraReserved, "\n"), ExtraOver: st.Overridden[service.SettingReservedSlugs],
		Builtin: st.Builtin, EnvSlugs: st.EnvReserved, Counters: st.Counters, Forms: forms, Sheets: sheets,
		PublicAdmin: public,
	}
	for _, f := range numericSettings {
		minimum := 1
		if f.key == service.SettingKeepReleases {
			minimum = 2
		}
		data.Fields = append(data.Fields, settingField{
			Key: f.key, Bytes: f.bytes, Min: minimum, Overridden: st.Overridden[f.key],
			Value: formatSetting(rc.loc, f.bytes, f.get(st.Limits)), Default: formatSetting(rc.loc, f.bytes, f.get(defaults)),
		})
	}
	s.page(w, r, rc, http.StatusOK, "admin_settings", "settings", "settings.title", data)
}

func (s *server) saveSettings(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	st, err := s.svc.GetSettings(r.Context(), rc.actor)
	if err != nil {
		s.fail(w, r, rc, err, "/admin/settings")
		return
	}
	upd, bad := settingsUpdate(r, rc.loc, st)
	if bad != "" {
		s.setFlash(w, r, flashError, rc.loc.T("settings.notnumber", "field", rc.loc.T("settings."+bad)))
		redirect(w, r, "/admin/settings")
		return
	}
	if _, err := s.svc.UpdateSettings(r.Context(), rc.actor, upd); err != nil {
		s.fail(w, r, rc, err, "/admin/settings")
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("settings.saved"))
	redirect(w, r, "/admin/settings")
}

// settingsUpdate builds an update containing only changed or reset fields,
// so saving the form untouched does not turn defaults into overrides. It
// returns the key of the first field that is not a number.
func settingsUpdate(r *http.Request, l *locale, st service.Settings) (service.SettingsUpdate, string) {
	var upd service.SettingsUpdate
	for _, f := range numericSettings {
		if r.PostFormValue("reset_"+f.key) == "1" {
			upd.Reset = append(upd.Reset, f.key)
			continue
		}
		raw := strings.ReplaceAll(strings.TrimSpace(r.PostFormValue(f.key)), ",", ".")
		if raw == "" || raw == formatSetting(l, f.bytes, f.get(st.Limits)) {
			continue
		}
		v, ok := parseSetting(raw, f.bytes)
		if !ok {
			return service.SettingsUpdate{}, f.key
		}
		f.set(&upd, &v)
	}
	counters := domain.Counters{
		MetrikaID: strings.TrimSpace(r.PostFormValue(service.SettingCounterMetrika)),
		GA4ID:     strings.ToUpper(strings.TrimSpace(r.PostFormValue(service.SettingCounterGA4))),
	}
	if counters != st.Counters {
		upd.Counters = &counters
	}
	if r.PostFormValue("reset_"+service.SettingReservedSlugs) == "1" {
		upd.Reset = append(upd.Reset, service.SettingReservedSlugs)
		return upd, ""
	}
	slugs := strings.FieldsFunc(strings.ToLower(r.PostFormValue(service.SettingReservedSlugs)), func(c rune) bool {
		return c == ',' || c == ' ' || c == '\n' || c == '\r' || c == '\t'
	})
	sort.Strings(slugs)
	if strings.Join(slugs, ",") != strings.Join(st.ExtraReserved, ",") {
		if len(slugs) == 0 {
			upd.Reset = append(upd.Reset, service.SettingReservedSlugs)
		} else {
			upd.ReservedSlugs = &slugs
		}
	}
	return upd, ""
}

func parseSetting(raw string, bytes bool) (int64, bool) {
	if !bytes {
		v, err := strconv.ParseInt(raw, 10, 64)
		return v, err == nil
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f > 1<<20 {
		return 0, false
	}
	return int64(math.Round(f * mib)), true
}
