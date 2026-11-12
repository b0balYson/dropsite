package api

import (
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

type channelDTO struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Target  string `json:"target"`
	Status  string `json:"status"`
	Enabled bool   `json:"enabled"`
	// Failures and PausedAt expose the circuit breaker.
	Failures  int        `json:"consecutive_failures"`
	PausedAt  *time.Time `json:"paused_at"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

func toChannel(c domain.FormChannel) channelDTO {
	return channelDTO{
		ID: strconv.FormatInt(c.ID, 10), Kind: string(c.Kind), Target: c.Target, Status: string(c.Status),
		Enabled: c.Enabled, Failures: c.ConsecutiveFailures, PausedAt: c.PausedAt, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}

type channelResultDTO struct {
	Channel channelDTO `json:"channel"`
	Secret  string     `json:"secret,omitempty"`
	Link    string     `json:"link,omitempty"`
}

type deliveryDTO struct {
	ChannelID     string     `json:"channel_id"`
	Kind          string     `json:"kind"`
	Target        string     `json:"target"`
	Status        string     `json:"status"`
	Attempts      int        `json:"attempts"`
	LastError     string     `json:"last_error"`
	NextAttemptAt *time.Time `json:"next_attempt_at"`
	SentAt        *time.Time `json:"sent_at"`
}

type fieldDTO struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type submissionDTO struct {
	ID         string        `json:"id"`
	Form       string        `json:"form"`
	Host       string        `json:"host"`
	CreatedAt  time.Time     `json:"created_at"`
	Fields     []fieldDTO    `json:"fields"`
	Deliveries []deliveryDTO `json:"deliveries"`
}

func toSubmission(s domain.FormSubmission) submissionDTO {
	out := submissionDTO{
		ID: strconv.FormatInt(s.ID, 10), Form: s.Form, Host: s.Host, CreatedAt: s.CreatedAt,
		Fields: make([]fieldDTO, 0, len(s.Fields)), Deliveries: make([]deliveryDTO, 0, len(s.Deliveries)),
	}
	for _, f := range s.Fields {
		out.Fields = append(out.Fields, fieldDTO(f))
	}
	for _, d := range s.Deliveries {
		dd := deliveryDTO{
			ChannelID: strconv.FormatInt(d.ChannelID, 10), Kind: string(d.ChannelKind), Target: d.ChannelTarget,
			Status: string(d.Status), Attempts: d.Attempts, LastError: d.LastError, SentAt: d.SentAt,
		}
		if d.Status == domain.DeliveryPending {
			next := d.NextAttemptAt
			dd.NextAttemptAt = &next
		}
		out.Deliveries = append(out.Deliveries, dd)
	}
	return out
}

// formSiteSettingsDTO is the form setup of a site with what an owner needs
// to add forms and channels.
type formSiteSettingsDTO struct {
	SiteID            string   `json:"site_id"`
	SiteURL           string   `json:"site_url"`
	RequireAltcha     bool     `json:"require_altcha"`
	FormNames         []string `json:"form_names"`
	TelegramAvailable bool     `json:"telegram_available"`
	TelegramBot       *string  `json:"telegram_bot"`
	EmailAvailable    bool     `json:"email_available"`
	CabinetURL        string   `json:"cabinet_url"`
}

// formSiteSettings completes the settings of a site the actor may see.
func (a *api) formSiteSettings(r *http.Request, v service.SiteView, requireAltcha bool) (formSiteSettingsDTO, error) {
	ctx := r.Context()
	names, err := a.svc.ListFormNames(ctx, actorFrom(ctx), v.Site.ID)
	if err != nil {
		return formSiteSettingsDTO{}, err
	}
	av, err := a.svc.FormChannelAvailability(ctx)
	if err != nil {
		return formSiteSettingsDTO{}, err
	}
	out := formSiteSettingsDTO{
		SiteID: v.Site.ID, SiteURL: v.URL, RequireAltcha: requireAltcha, FormNames: nonNil(names),
		TelegramAvailable: av.Telegram, EmailAvailable: av.Email, CabinetURL: a.svc.FormsPageURL(v.Site.ID),
	}
	if av.Telegram {
		out.TelegramBot = optional("@" + av.Bot)
	}
	return out, nil
}

func channelID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, domain.ErrNotFound
	}
	return id, nil
}

func (a *api) listChannels(w http.ResponseWriter, r *http.Request) error {
	chs, err := a.svc.ListFormChannels(r.Context(), actorFrom(r.Context()), r.PathValue("ref"))
	if err != nil {
		return err
	}
	items := make([]channelDTO, 0, len(chs))
	for _, c := range chs {
		items = append(items, toChannel(c))
	}
	writeJSON(w, http.StatusOK, newList(items, len(items)))
	return nil
}

type createChannelRequest struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
}

func (a *api) createChannel(w http.ResponseWriter, r *http.Request) error {
	var req createChannelRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	res, err := a.svc.AddFormChannel(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), domain.ChannelKind(req.Kind), req.Target)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, channelResultDTO{Channel: toChannel(res.Channel), Secret: res.Secret, Link: res.Link})
	return nil
}

type updateChannelRequest struct {
	Enabled *bool `json:"enabled"`
}

func (a *api) updateChannel(w http.ResponseWriter, r *http.Request) error {
	id, err := channelID(r)
	if err != nil {
		return err
	}
	var req updateChannelRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	if req.Enabled == nil {
		return invalidField("enabled", "enabled must be true or false")
	}
	ch, err := a.svc.SetFormChannelEnabled(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), id, *req.Enabled)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toChannel(ch))
	return nil
}

func (a *api) deleteChannel(w http.ResponseWriter, r *http.Request) error {
	id, err := channelID(r)
	if err != nil {
		return err
	}
	if err := a.svc.DeleteFormChannel(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), id); err != nil {
		return err
	}
	noContent(w)
	return nil
}

type verifyChannelRequest struct {
	Code string `json:"code"`
}

func (a *api) verifyChannel(w http.ResponseWriter, r *http.Request) error {
	id, err := channelID(r)
	if err != nil {
		return err
	}
	var req verifyChannelRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	ch, err := a.svc.VerifyFormChannel(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), id, req.Code)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toChannel(ch))
	return nil
}

func (a *api) rotateChannelSecret(w http.ResponseWriter, r *http.Request) error {
	id, err := channelID(r)
	if err != nil {
		return err
	}
	res, err := a.svc.RotateWebhookSecret(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, channelResultDTO{Channel: toChannel(res.Channel), Secret: res.Secret})
	return nil
}

func (a *api) getFormSiteSettings(w http.ResponseWriter, r *http.Request) error {
	v, err := a.svc.GetSite(r.Context(), actorFrom(r.Context()), r.PathValue("ref"))
	if err != nil {
		return err
	}
	st, err := a.svc.GetFormSiteSettings(r.Context(), actorFrom(r.Context()), v.Site.ID)
	if err != nil {
		return err
	}
	out, err := a.formSiteSettings(r, v, st.RequireAltcha)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

type updateFormSiteSettingsRequest struct {
	RequireAltcha *bool `json:"require_altcha"`
}

func (a *api) updateFormSiteSettings(w http.ResponseWriter, r *http.Request) error {
	var req updateFormSiteSettingsRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	if req.RequireAltcha == nil {
		return invalidField("require_altcha", "require_altcha must be true or false")
	}
	v, err := a.svc.GetSite(r.Context(), actorFrom(r.Context()), r.PathValue("ref"))
	if err != nil {
		return err
	}
	st, err := a.svc.SetFormSiteSettings(r.Context(), actorFrom(r.Context()), v.Site.ID, *req.RequireAltcha)
	if err != nil {
		return err
	}
	out, err := a.formSiteSettings(r, v, st.RequireAltcha)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (a *api) listSubmissions(w http.ResponseWriter, r *http.Request) error {
	p, err := page(r.URL.Query())
	if err != nil {
		return err
	}
	subs, total, err := a.svc.ListFormSubmissions(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), r.URL.Query().Get("form"), p)
	if err != nil {
		return err
	}
	items := make([]submissionDTO, 0, len(subs))
	for _, s := range subs {
		items = append(items, toSubmission(s))
	}
	writeJSON(w, http.StatusOK, newList(items, total))
	return nil
}

func (a *api) exportSubmissions(w http.ResponseWriter, r *http.Request) error {
	started := false
	err := a.svc.ExportFormSubmissions(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), r.URL.Query().Get("form"),
		func(siteID string) io.Writer {
			started = true
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			w.Header().Set("Content-Disposition", `attachment; filename="`+siteID+`-submissions.csv"`)
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusOK)
			return w
		})
	if err != nil && started {
		// The status line is gone: the client sees a truncated file.
		a.log.Warn("form export interrupted", "err", err)
		return nil
	}
	return err
}

type telegramSettingsDTO struct {
	Bot      string `json:"bot"`
	TokenSet bool   `json:"token_set"`
}

type smtpSettingsDTO struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	PasswordSet bool   `json:"password_set"`
	From        string `json:"from"`
	Security    string `json:"security"`
}

type formSettingsDTO struct {
	Telegram          telegramSettingsDTO `json:"telegram"`
	SMTP              smtpSettingsDTO     `json:"smtp"`
	RetentionDays     int                 `json:"retention_days"`
	AltchaMaxNumber   int                 `json:"altcha_max_number"`
	SecretsUnreadable bool                `json:"secrets_unreadable"`
}

func toFormSettings(s service.FormSettings) formSettingsDTO {
	return formSettingsDTO{
		Telegram: telegramSettingsDTO{Bot: s.TelegramBot, TokenSet: s.TelegramTokenSet},
		SMTP: smtpSettingsDTO{Host: s.SMTPHost, Port: s.SMTPPort, Username: s.SMTPUsername,
			PasswordSet: s.SMTPPasswordSet, From: s.SMTPFrom, Security: s.SMTPSecurity},
		RetentionDays: s.RetentionDays, AltchaMaxNumber: s.AltchaMaxNumber, SecretsUnreadable: s.SecretsUnreadable,
	}
}

type smtpUpdateRequest struct {
	Host     *string `json:"host"`
	Port     *int    `json:"port"`
	Username *string `json:"username"`
	Password *string `json:"password"`
	From     *string `json:"from"`
	Security *string `json:"security"`
}

type formSettingsRequest struct {
	TelegramToken   *string            `json:"telegram_token"`
	SMTP            *smtpUpdateRequest `json:"smtp"`
	RetentionDays   *int               `json:"retention_days"`
	AltchaMaxNumber *int               `json:"altcha_max_number"`
}

func (a *api) getFormSettings(w http.ResponseWriter, r *http.Request) error {
	st, err := a.svc.GetFormSettings(r.Context(), actorFrom(r.Context()))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toFormSettings(st))
	return nil
}

func (a *api) updateFormSettings(w http.ResponseWriter, r *http.Request) error {
	var req formSettingsRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	u := service.FormSettingsUpdate{TelegramToken: req.TelegramToken, RetentionDays: req.RetentionDays, AltchaMaxNumber: req.AltchaMaxNumber}
	if m := req.SMTP; m != nil {
		u.SMTPHost, u.SMTPPort, u.SMTPUsername, u.SMTPPassword, u.SMTPFrom, u.SMTPSecurity =
			m.Host, m.Port, m.Username, m.Password, m.From, m.Security
	}
	st, err := a.svc.UpdateFormSettings(r.Context(), actorFrom(r.Context()), u)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toFormSettings(st))
	return nil
}
