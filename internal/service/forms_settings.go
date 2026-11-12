package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/forms"
)

// Form setting keys in the settings table. Secret values are sealed.
const (
	FormSettingTelegramToken = "forms.telegram_token"
	FormSettingTelegramBot   = "forms.telegram_bot"
	FormSettingSMTPHost      = "forms.smtp_host"
	FormSettingSMTPPort      = "forms.smtp_port"
	FormSettingSMTPUsername  = "forms.smtp_username"
	FormSettingSMTPPassword  = "forms.smtp_password" //nolint:gosec // G101: a settings key, not a credential
	FormSettingSMTPFrom      = "forms.smtp_from"
	FormSettingSMTPSecurity  = "forms.smtp_security"
	FormSettingRetentionDays = "forms.retention_days"
	FormSettingAltchaMax     = "forms.altcha_max_number"

	DefaultFormRetentionDays = 90
	MaxFormRetentionDays     = 3650
)

// FormSettings is the admin view of form delivery settings. Secrets are
// reported only as set or not.
type FormSettings struct {
	TelegramBot      string
	TelegramTokenSet bool
	SMTPHost         string
	SMTPPort         int
	SMTPUsername     string
	SMTPPasswordSet  bool
	SMTPFrom         string
	SMTPSecurity     string
	RetentionDays    int
	AltchaMaxNumber  int
	// SecretsUnreadable is true when a stored secret cannot be decrypted
	// (for example after a restore without the key file).
	SecretsUnreadable bool
}

// FormSettingsUpdate changes form settings; nil leaves a value as is and an
// empty string clears it.
type FormSettingsUpdate struct {
	TelegramToken   *string
	SMTPHost        *string
	SMTPPort        *int
	SMTPUsername    *string
	SMTPPassword    *string
	SMTPFrom        *string
	SMTPSecurity    *string
	RetentionDays   *int
	AltchaMaxNumber *int
}

// formConfig is the decrypted configuration used for delivery.
type formConfig struct {
	telegramToken string
	botName       string
	smtp          forms.SMTPConfig
	retentionDays int
	altchaMax     int
	unreadable    bool
}

func secretContext(key string) string { return "setting:" + key }

func (s *Service) formConfig(ctx context.Context) (formConfig, error) {
	eff, err := s.settings(ctx)
	if err != nil {
		return formConfig{}, err
	}
	raw := eff.raw
	c := formConfig{
		botName:       raw[FormSettingTelegramBot],
		retentionDays: DefaultFormRetentionDays,
		altchaMax:     forms.DefaultAltchaMaxNumber,
		smtp: forms.SMTPConfig{
			Host: raw[FormSettingSMTPHost], Username: raw[FormSettingSMTPUsername],
			From: raw[FormSettingSMTPFrom], Security: raw[FormSettingSMTPSecurity],
		},
	}
	if c.smtp.Security == "" {
		c.smtp.Security = forms.SMTPStartTLS
	}
	c.smtp.Port, _ = strconv.Atoi(raw[FormSettingSMTPPort])
	if v, err := strconv.Atoi(raw[FormSettingRetentionDays]); err == nil && v > 0 {
		c.retentionDays = v
	}
	if v, err := strconv.Atoi(raw[FormSettingAltchaMax]); err == nil && v >= forms.MinAltchaMaxNumber {
		c.altchaMax = v
	}
	open := func(key string) string {
		v := raw[key]
		if v == "" || s.sealer == nil {
			return ""
		}
		pt, err := s.sealer.Open(v, secretContext(key))
		if err != nil {
			c.unreadable = true
			return ""
		}
		return pt
	}
	c.telegramToken = open(FormSettingTelegramToken)
	c.smtp.Password = open(FormSettingSMTPPassword)
	if c.telegramToken == "" {
		c.botName = ""
	}
	return c, nil
}

// GetFormSettings returns form settings (admin only).
func (s *Service) GetFormSettings(ctx context.Context, a Actor) (FormSettings, error) {
	if err := requireAdmin(a); err != nil {
		return FormSettings{}, err
	}
	c, err := s.formConfig(ctx)
	if err != nil {
		return FormSettings{}, err
	}
	return FormSettings{
		TelegramBot: c.botName, TelegramTokenSet: c.telegramToken != "",
		SMTPHost: c.smtp.Host, SMTPPort: c.smtp.Port, SMTPUsername: c.smtp.Username,
		SMTPPasswordSet: c.smtp.Password != "", SMTPFrom: c.smtp.From, SMTPSecurity: c.smtp.Security,
		RetentionDays: c.retentionDays, AltchaMaxNumber: c.altchaMax, SecretsUnreadable: c.unreadable,
	}, nil
}

func validSMTPHost(h string) bool {
	if h == "" {
		return true
	}
	if net.ParseIP(h) != nil {
		return true
	}
	return domain.IsValidHostname(strings.ToLower(h))
}

// formSettingValues validates an update into key → value ("" deletes).
func (s *Service) formSettingValues(ctx context.Context, cur formConfig, u FormSettingsUpdate) (map[string]string, error) {
	out := map[string]string{}
	if u.TelegramToken != nil {
		token := strings.TrimSpace(*u.TelegramToken)
		if token == "" {
			out[FormSettingTelegramToken], out[FormSettingTelegramBot] = "", ""
		} else {
			if !forms.ValidBotToken(token) {
				return nil, invalidSetting(FormSettingTelegramToken, "this does not look like a bot token from @BotFather")
			}
			name, err := s.senders.TelegramMe(ctx, token)
			if err != nil {
				return nil, domain.E(domain.KindInvalid, domain.CodeTelegramTokenFailed,
					"Telegram rejected the bot token: "+errText(err))
			}
			out[FormSettingTelegramToken] = s.sealer.Seal(token, secretContext(FormSettingTelegramToken))
			out[FormSettingTelegramBot] = name
		}
	}
	smtp := cur.smtp
	str := func(key string, p *string, dst *string) {
		if p != nil {
			*dst = strings.TrimSpace(*p)
			out[key] = *dst
		}
	}
	str(FormSettingSMTPHost, u.SMTPHost, &smtp.Host)
	str(FormSettingSMTPUsername, u.SMTPUsername, &smtp.Username)
	str(FormSettingSMTPFrom, u.SMTPFrom, &smtp.From)
	if u.SMTPSecurity != nil {
		smtp.Security = strings.TrimSpace(*u.SMTPSecurity)
		if !forms.ValidSecurity(smtp.Security) {
			return nil, invalidSetting(FormSettingSMTPSecurity, "must be starttls, tls or none")
		}
		out[FormSettingSMTPSecurity] = smtp.Security
	}
	if u.SMTPPort != nil {
		if *u.SMTPPort < 0 || *u.SMTPPort > 65535 {
			return nil, invalidSetting(FormSettingSMTPPort, "must be a TCP port")
		}
		smtp.Port = *u.SMTPPort
		out[FormSettingSMTPPort] = strconv.Itoa(*u.SMTPPort)
		if *u.SMTPPort == 0 {
			out[FormSettingSMTPPort] = ""
		}
	}
	if u.SMTPPassword != nil {
		if *u.SMTPPassword == "" {
			out[FormSettingSMTPPassword] = ""
		} else {
			out[FormSettingSMTPPassword] = s.sealer.Seal(*u.SMTPPassword, secretContext(FormSettingSMTPPassword))
		}
	}
	if !validSMTPHost(smtp.Host) || len(smtp.Host) > 253 {
		return nil, invalidSetting(FormSettingSMTPHost, "must be a host name or IP address")
	}
	if smtp.Host != "" {
		if smtp.Port == 0 {
			return nil, invalidSetting(FormSettingSMTPPort, "is required when a mail server is set")
		}
		if _, ok := forms.ParseEmail(smtp.From); !ok {
			return nil, invalidSetting(FormSettingSMTPFrom, "must be a plain e-mail address such as forms@example.com")
		}
	}
	if u.RetentionDays != nil {
		if *u.RetentionDays < 1 || *u.RetentionDays > MaxFormRetentionDays {
			return nil, invalidSetting(FormSettingRetentionDays, fmt.Sprintf("must be from 1 to %d", MaxFormRetentionDays))
		}
		out[FormSettingRetentionDays] = strconv.Itoa(*u.RetentionDays)
	}
	if u.AltchaMaxNumber != nil {
		if *u.AltchaMaxNumber < forms.MinAltchaMaxNumber || *u.AltchaMaxNumber > forms.MaxAltchaMaxNumber {
			return nil, invalidSetting(FormSettingAltchaMax,
				fmt.Sprintf("must be from %d to %d", forms.MinAltchaMaxNumber, forms.MaxAltchaMaxNumber))
		}
		out[FormSettingAltchaMax] = strconv.Itoa(*u.AltchaMaxNumber)
	}
	return out, nil
}

// UpdateFormSettings validates and stores form settings (admin only). The
// audit entry lists changed keys, never secret values.
func (s *Service) UpdateFormSettings(ctx context.Context, a Actor, u FormSettingsUpdate) (FormSettings, error) {
	if err := requireAdmin(a); err != nil {
		return FormSettings{}, err
	}
	cur, err := s.formConfig(ctx)
	if err != nil {
		return FormSettings{}, err
	}
	vals, err := s.formSettingValues(ctx, cur, u)
	if err != nil {
		return FormSettings{}, err
	}
	changed := make([]string, 0, len(vals))
	for k, v := range vals {
		if v == "" {
			err = s.repo.DeleteSetting(ctx, k)
		} else {
			err = s.repo.SetSetting(ctx, k, v)
		}
		if err != nil {
			return FormSettings{}, fmt.Errorf("save form setting: %w", err)
		}
		changed = append(changed, k)
	}
	s.settingsMu.Lock()
	s.settingsData = nil
	s.settingsMu.Unlock()
	if len(changed) > 0 {
		s.audit(ctx, a, "forms.settings.update", "settings", map[string]any{"changed": sortedStrings(changed)})
	}
	return s.GetFormSettings(ctx, a)
}

// FormAvailability tells site owners which channel kinds can be added.
type FormAvailability struct {
	Telegram bool
	Bot      string
	Email    bool
	// Sheets is true when Google Sheets is set up; SheetsEmail is the
	// service account to share spreadsheets with.
	Sheets      bool
	SheetsEmail string
}

// FormChannelAvailability reports configured transports without secrets.
func (s *Service) FormChannelAvailability(ctx context.Context) (FormAvailability, error) {
	c, err := s.formConfig(ctx)
	if err != nil {
		return FormAvailability{}, err
	}
	out := FormAvailability{Telegram: c.telegramToken != "" && c.botName != "", Bot: c.botName, Email: c.smtp.Configured()}
	if st, err := s.GetSheetsStatus(ctx); err == nil {
		out.Sheets, out.SheetsEmail = st.Configured, st.Email
	}
	return out, nil
}

// errText is the user-facing text of a sender error.
func errText(err error) string {
	var se *forms.SendError
	if errors.As(err, &se) {
		return se.Msg
	}
	return "request failed"
}
