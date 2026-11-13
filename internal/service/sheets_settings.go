package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/ratelimit"
	"github.com/b0balYson/dropsite/internal/sheets"
)

// Google Sheets (change google-sheets-sync).
const (
	// SheetsSettingKey holds the sealed service account JSON key.
	SheetsSettingKey = "sheets.service_account"
	// SheetOpsPerSite bounds manual synchronizations and links per site
	// and hour.
	SheetOpsPerSite = 30
	// SheetCallTimeout bounds one whole operation with Google (several
	// calls of at most sheets.CallTimeout each).
	SheetCallTimeout = 60 * time.Second
)

// sheetsState is the Google Sheets part of Service, created in New.
type sheetsState struct {
	sheetsTransport http.RoundTripper
	sheetsSiteLimit *ratelimit.Window
	sheetsMu        sync.Mutex
	sheetsEnc       string
	sheetsClient    *sheets.Client
}

func newSheetsState(d Deps, now func() time.Time) sheetsState {
	tr := d.SheetsTransport
	if tr == nil {
		tr = sheets.NewTransport(selfPrefixes())
	}
	return sheetsState{
		sheetsTransport: tr,
		sheetsSiteLimit: ratelimit.New(SheetOpsPerSite, time.Hour).WithClock(now),
	}
}

// SheetsStatus is what users may know about the installation's Google
// Sheets setup: never the key.
type SheetsStatus struct {
	Configured bool
	// Email is the service account address users share spreadsheets with.
	Email string
	// Unreadable is true when a key is stored but cannot be decrypted.
	Unreadable bool
}

var errSheetsNotConfigured = domain.E(domain.KindInvalid, domain.CodeSheetsNotConfigured,
	"Google Sheets is not connected on this dropsite server; ask the administrator to add a Google service "+
		"account key in the settings of the web interface")

var errSheetsUnreadable = domain.E(domain.KindInvalid, domain.CodeSheetsNotConfigured,
	"the Google service account key of this server cannot be read (restored without the key file?); "+
		"ask the administrator to paste it again in the settings")

// sheetsAPI returns the client of the stored service account, rebuilt
// only when the key changes so the access token is reused.
func (s *Service) sheetsAPI(ctx context.Context) (*sheets.Client, error) {
	eff, err := s.settings(ctx)
	if err != nil {
		return nil, err
	}
	enc := eff.raw[SheetsSettingKey]
	if enc == "" || s.sealer == nil {
		return nil, errSheetsNotConfigured
	}
	s.sheetsMu.Lock()
	defer s.sheetsMu.Unlock()
	if enc == s.sheetsEnc && s.sheetsClient != nil {
		return s.sheetsClient, nil
	}
	raw, err := s.sealer.Open(enc, secretContext(SheetsSettingKey))
	if err != nil {
		return nil, errSheetsUnreadable
	}
	acct, err := sheets.ParseServiceAccount([]byte(raw))
	if err != nil {
		return nil, errSheetsUnreadable
	}
	s.sheetsEnc, s.sheetsClient = enc, sheets.New(acct, s.sheetsTransport, s.now)
	return s.sheetsClient, nil
}

// GetSheetsStatus reports whether Google Sheets is set up and the service
// account address; any signed-in user may see it.
func (s *Service) GetSheetsStatus(ctx context.Context) (SheetsStatus, error) {
	c, err := s.sheetsAPI(ctx)
	if err == nil {
		return SheetsStatus{Configured: true, Email: c.Email()}, nil
	}
	// Both errors share the code sheets_not_configured, and errors.Is on a
	// domain error compares codes: tell them apart by identity.
	// sheetsAPI returns these sentinels unwrapped.
	if err == errSheetsUnreadable { //nolint:errorlint // identity, see above
		return SheetsStatus{Unreadable: true}, nil
	}
	if err == errSheetsNotConfigured { //nolint:errorlint // identity, see above
		return SheetsStatus{}, nil
	}
	return SheetsStatus{}, err
}

func sheetsKeyErr(msg string) error {
	return domain.E(domain.KindInvalid, domain.CodeSheetsInvalid, msg).WithParams(map[string]any{"field": "service_account"})
}

// SetSheetsServiceAccount stores the service account JSON key (admin only,
// web interface only). The key is checked by asking Google for a token.
// The audit entry names the service account, never the key.
func (s *Service) SetSheetsServiceAccount(ctx context.Context, a Actor, key string) (SheetsStatus, error) {
	if err := requireAdmin(a); err != nil {
		return SheetsStatus{}, err
	}
	key = strings.TrimSpace(key)
	acct, err := sheets.ParseServiceAccount([]byte(key))
	if err != nil {
		return SheetsStatus{}, sheetsKeyErr(err.Error())
	}
	tctx, cancel := context.WithTimeout(ctx, sheets.CallTimeout)
	defer cancel()
	if _, err := sheets.New(acct, s.sheetsTransport, s.now).Token(tctx); err != nil {
		s.log.Warn("google service account check failed", "err", sheets.Reason(err))
		if sheets.IsAuth(err) {
			return SheetsStatus{}, sheetsKeyErr("Google rejected this key: it may be deleted or disabled; create a new key")
		}
		return SheetsStatus{}, domain.E(domain.KindInvalid, domain.CodeSheetsUnavailable,
			"could not check the key with Google: "+err.Error())
	}
	if err := s.repo.SetSetting(ctx, SheetsSettingKey, s.sealer.Seal(key, secretContext(SheetsSettingKey))); err != nil {
		return SheetsStatus{}, fmt.Errorf("save sheets key: %w", err)
	}
	s.dropSettingsCache()
	s.audit(ctx, a, "sheets.settings.update", "settings", map[string]any{"action": "set", "service_account": acct.Email})
	return s.GetSheetsStatus(ctx)
}

// ClearSheetsServiceAccount removes the key (admin only); every Google
// Sheets feature then reports that it is not configured.
func (s *Service) ClearSheetsServiceAccount(ctx context.Context, a Actor) error {
	if err := requireAdmin(a); err != nil {
		return err
	}
	if err := s.repo.DeleteSetting(ctx, SheetsSettingKey); err != nil {
		return fmt.Errorf("delete sheets key: %w", err)
	}
	s.dropSettingsCache()
	s.audit(ctx, a, "sheets.settings.update", "settings", map[string]any{"action": "clear"})
	return nil
}

func (s *Service) dropSettingsCache() {
	s.settingsMu.Lock()
	s.settingsData = nil
	s.settingsMu.Unlock()
}

// sheetsCallErr turns a failure of Google into a user-facing error. write
// tells which permission the user must give.
func (s *Service) sheetsCallErr(err error, email string, write bool) error {
	var de *domain.Error
	if errors.As(err, &de) {
		return err
	}
	if errors.Is(err, sheets.ErrInvalidURL) {
		return domain.E(domain.KindInvalid, domain.CodeSheetsInvalidURL, err.Error()).WithParams(map[string]any{"field": "url"})
	}
	s.log.Warn("google sheets call failed", "err", sheets.Reason(err))
	switch {
	case sheets.IsAccess(err):
		role := "Viewer"
		if write {
			role = "Editor"
		}
		return domain.E(domain.KindInvalid, domain.CodeSheetsAccessDenied,
			fmt.Sprintf("dropsite cannot open this spreadsheet or sheet: in Google Sheets press Share, add %s as %s, "+
				"and check that the link points to the right sheet", email, role)).
			WithParams(map[string]any{"service_account_email": email, "role": strings.ToLower(role)})
	case sheets.IsAuth(err):
		return domain.E(domain.KindInvalid, domain.CodeSheetsUnavailable,
			"Google rejected the service account key of this server; ask the administrator to check it")
	}
	return domain.E(domain.KindInvalid, domain.CodeSheetsUnavailable, sheetsErrText(err))
}

// sheetsErrText is a short text of a Google failure without Google's own
// message.
func sheetsErrText(err error) string {
	var se *sheets.Error
	if errors.As(err, &se) {
		return se.Error()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "Google did not answer in time; try again later"
	}
	return "the request to Google failed; try again later"
}
