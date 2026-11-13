package ui

import (
	"net/http"
	"strconv"
	"time"

	"github.com/b0balYson/dropsite/internal/brand"
	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/setupcheck"
)

const (
	installBase = "https://raw.githubusercontent.com/b0balYson/dropsite/main/scripts/"
	installSh   = "curl -fsSL " + installBase + "install.sh"
	installPs1  = "irm " + installBase + "install.ps1 | iex"
	day         = 24 * time.Hour
)

// tokenTTLs are the expiry choices offered on the page, in days (0 = never).
var tokenTTLs = []int{0, 30, 90}

type tokenRow struct {
	domain.Token
	State string // active, expired, revoked
}

type issuedToken struct {
	Name    string
	Command string
	Expires *time.Time
}

// issuedCode is a connect code with the ready-to-paste commands.
type issuedCode struct {
	Code      string
	Unix      string
	Windows   string
	ExpiresAt time.Time
	// ExpiresISO feeds the countdown script.
	ExpiresISO string
}

type connectData struct {
	Code   *issuedCode
	Apps   []domain.OAuthGrant
	MCPURL string
	// MetaURL is the OAuth metadata address people open when a connector fails.
	MetaURL  string
	Tokens   []tokenRow
	Issued   *issuedToken
	Error    string
	FormName string
	TTLs     []int
	// TokensOpen keeps the manual-token section expanded after using it.
	TokensOpen bool
}

// unixCommand installs the CLI, exchanges the code and connects assistants.
// The code goes through the environment of sh, not its arguments, so other
// users of the computer cannot see it in the process list. The server comes
// from validated configuration and the code alphabet has no shell
// metacharacters, so no quoting is needed.
func unixCommand(server, code string) string {
	return installSh + " | DROPSITE_SERVER=" + server + " DROPSITE_CODE=" + code + " sh"
}

// windowsCommand passes the same through environment variables: iex takes no arguments.
func windowsCommand(server, code string) string {
	return "$env:DROPSITE_SERVER='" + server + "'; $env:DROPSITE_CODE='" + code + "'; " + installPs1
}

func (s *server) connectData(r *http.Request, rc *reqCtx) (connectData, error) {
	tokens, err := s.svc.ListTokens(r.Context(), rc.actor)
	if err != nil {
		return connectData{}, err
	}
	apps, err := s.svc.ListOAuthGrants(r.Context(), rc.actor)
	if err != nil {
		return connectData{}, err
	}
	now := time.Now()
	rows := make([]tokenRow, 0, len(tokens))
	for _, t := range tokens {
		state := "active"
		switch {
		case t.RevokedAt != nil:
			state = "revoked"
		case !t.Active(now):
			state = "expired"
		}
		rows = append(rows, tokenRow{Token: t, State: state})
	}
	return connectData{Tokens: rows, Apps: apps, TTLs: tokenTTLs, MCPURL: s.svc.MCPResource(),
		MetaURL: s.svc.APIURL() + setupcheck.ProbePath}, nil
}

func (s *server) connectPage(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	data, err := s.connectData(r, rc)
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	s.page(w, r, rc, http.StatusOK, "connect", "connect", "connect.title", data)
}

// createCode issues a connect code and shows the commands with it.
func (s *server) createCode(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	res, err := s.svc.CreateConnectCode(r.Context(), rc.actor)
	if err != nil {
		s.fail(w, r, rc, err, "/connect")
		return
	}
	data, err := s.connectData(r, rc)
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	server := s.svc.APIURL()
	data.Code = &issuedCode{
		Code: res.Code, Unix: unixCommand(server, res.Code), Windows: windowsCommand(server, res.Code),
		ExpiresAt: res.ExpiresAt, ExpiresISO: res.ExpiresAt.UTC().Format(time.RFC3339),
	}
	if isHTMX(r) {
		s.fragment(w, r, rc, "connect_block", data)
		return
	}
	s.page(w, r, rc, http.StatusOK, "connect", "connect", "connect.title", data)
}

// revokeApp disconnects an OAuth application.
func (s *server) revokeApp(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r, rc)
		return
	}
	if err := s.svc.RevokeOAuthGrant(r.Context(), rc.actor, id); err != nil {
		s.fail(w, r, rc, err, "/connect")
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("connect.apps.revoked"))
	redirect(w, r, "/connect#apps")
}

func (s *server) createToken(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	name := r.PostFormValue("name")
	days, err := strconv.Atoi(r.PostFormValue("ttl"))
	if err != nil || days < 0 || days > 365 {
		days = 0
	}
	res, tokErr := s.svc.CreateToken(r.Context(), rc.actor, name, time.Duration(days)*day)
	data, err := s.connectData(r, rc)
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	data.TokensOpen = true
	status := http.StatusOK
	if tokErr != nil {
		data.Error, status = s.describe(rc, tokErr)
		data.FormName = name
	} else {
		data.Issued = &issuedToken{
			Name:    res.Token.Name,
			Command: brand.Name + " login --server " + s.svc.APIURL() + " --token " + res.Value,
			Expires: res.Token.ExpiresAt,
		}
	}
	if isHTMX(r) {
		// Validation errors still swap into the form area, so answer 200.
		s.fragment(w, r, rc, "token_result", data)
		return
	}
	s.page(w, r, rc, status, "connect", "connect", "connect.title", data)
}

func (s *server) revokeToken(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r, rc)
		return
	}
	if err := s.svc.RevokeToken(r.Context(), rc.actor, id); err != nil {
		s.fail(w, r, rc, err, "/connect")
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("connect.revoked"))
	redirect(w, r, "/connect#tokens")
}
