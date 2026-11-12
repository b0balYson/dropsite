package ui

import (
	"net/http"
	"strconv"
	"time"

	"github.com/b0balYson/dropsite/internal/brand"
	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/skills"
)

const (
	installSh  = "curl -fsSL https://raw.githubusercontent.com/b0balYson/dropsite/main/scripts/install.sh | sh"
	installPs1 = "irm https://raw.githubusercontent.com/b0balYson/dropsite/main/scripts/install.ps1 | iex"
	skillPath  = "~/.claude/skills/" + brand.Name + "-publish/SKILL.md"
	day        = 24 * time.Hour
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

type connectData struct {
	Tokens     []tokenRow
	Issued     *issuedToken
	Error      string
	FormName   string
	TTLs       []int
	CLI        string
	InstallSh  string
	InstallPs1 string
	SkillPath  string
	Skill      string
	Codex      string
	APIURL     string
}

func (s *server) connectData(r *http.Request, rc *reqCtx) (connectData, error) {
	tokens, err := s.svc.ListTokens(r.Context(), rc.actor)
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
	return connectData{
		Tokens: rows, TTLs: tokenTTLs, CLI: brand.Name, InstallSh: installSh, InstallPs1: installPs1,
		SkillPath: skillPath, Skill: skills.ClaudeCode, Codex: skills.Codex, APIURL: s.svc.APIURL(),
	}, nil
}

func (s *server) connectPage(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	data, err := s.connectData(r, rc)
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	s.page(w, r, rc, http.StatusOK, "connect", "connect", "connect.title", data)
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
	redirect(w, r, "/connect")
}
