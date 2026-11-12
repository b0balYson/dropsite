package ui

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/b0balYson/dropsite/internal/service"
)

// formSettingsUpdate reads the admin "Form submissions" form. Secret fields
// change only when typed in or explicitly cleared; plain fields are always
// sent, so an emptied field clears the value. It returns the key of a field
// that is not a number.
func formSettingsUpdate(r *http.Request) (service.FormSettingsUpdate, string) {
	var u service.FormSettingsUpdate
	str := func(name string) *string {
		v := strings.TrimSpace(r.PostFormValue(name))
		return &v
	}
	num := func(name string) (*int, bool) {
		v := strings.TrimSpace(r.PostFormValue(name))
		if v == "" {
			return nil, true
		}
		n, err := strconv.Atoi(v)
		return &n, err == nil
	}
	secret := func(name string) *string {
		if r.PostFormValue("clear_"+name) == "1" {
			empty := ""
			return &empty
		}
		if v := r.PostFormValue(name); v != "" {
			return &v
		}
		return nil
	}
	u.TelegramToken = secret("telegram_token")
	u.SMTPPassword = secret("smtp_password")
	u.SMTPHost, u.SMTPUsername, u.SMTPFrom, u.SMTPSecurity = str("smtp_host"), str("smtp_username"), str("smtp_from"), str("smtp_security")
	var ok bool
	if u.SMTPPort, ok = num("smtp_port"); !ok {
		return u, service.FormSettingSMTPPort
	}
	if u.SMTPPort == nil {
		zero := 0
		u.SMTPPort = &zero
	}
	if u.RetentionDays, ok = num("retention_days"); !ok {
		return u, service.FormSettingRetentionDays
	}
	if u.AltchaMaxNumber, ok = num("altcha_max_number"); !ok {
		return u, service.FormSettingAltchaMax
	}
	return u, ""
}

func (s *server) saveFormSettings(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	u, bad := formSettingsUpdate(r)
	if bad != "" {
		s.setFlash(w, r, flashError, rc.loc.T("settings.notnumber", "field", rc.loc.T("settings."+bad)))
		redirect(w, r, "/admin/settings#forms-h")
		return
	}
	if _, err := s.svc.UpdateFormSettings(r.Context(), rc.actor, u); err != nil {
		s.fail(w, r, rc, err, "/admin/settings#forms-h")
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("forms.admin.saved"))
	redirect(w, r, "/admin/settings#forms-h")
}
