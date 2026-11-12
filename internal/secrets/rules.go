package secrets

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"regexp"
)

// maxPerRule bounds the matches of one rule in one file.
const maxPerRule = 50

// rule is one secret format. anchors are literals of which at least one must
// occur before the regular expression runs; check, when set, confirms a
// match (tail is the content after it).
type rule struct {
	kind    string
	label   string
	anchors [][]byte
	re      *regexp.Regexp
	check   func(match, tail []byte) bool
}

func (r rule) mayMatch(data []byte) bool {
	for _, a := range r.anchors {
		if bytes.Contains(data, a) {
			return true
		}
	}
	return false
}

func lits(s ...string) [][]byte {
	out := make([][]byte, len(s))
	for i, v := range s {
		out[i] = []byte(v)
	}
	return out
}

var rules = []rule{
	{kind: KindServiceRole, label: "Supabase service_role key", anchors: lits("eyJ"),
		re:    regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{16,}`),
		check: serviceRoleJWT},
	{kind: KindSupabaseSecret, label: "Supabase secret key", anchors: lits("sb_secret_"),
		re: regexp.MustCompile(`\bsb_secret_[A-Za-z0-9_-]{20,}`), check: random},
	{kind: KindStripe, label: "Stripe secret key", anchors: lits("sk_live_", "rk_live_"),
		re: regexp.MustCompile(`\b[rs]k_live_[0-9A-Za-z]{20,}`), check: random},
	{kind: KindOpenAI, label: "OpenAI API key", anchors: lits("sk-proj-", "sk-svcacct-", "sk-admin-", "T3BlbkFJ"),
		re:    regexp.MustCompile(`\bsk-(?:(?:proj|svcacct|admin)-[A-Za-z0-9_-]{40,}|[A-Za-z0-9]{20}T3BlbkFJ[A-Za-z0-9]{20})`),
		check: random},
	{kind: KindAnthropic, label: "Anthropic API key", anchors: lits("sk-ant-"),
		re: regexp.MustCompile(`\bsk-ant-[a-z0-9]{2,12}-[A-Za-z0-9_-]{40,}`), check: random},
	{kind: KindTelegram, label: "Telegram bot token", anchors: lits(":AA"),
		re: regexp.MustCompile(`\b[0-9]{8,10}:AA[0-9A-Za-z_-]{33}`), check: telegramToken},
	{kind: KindAWS, label: "AWS access key", anchors: lits("AKIA", "ASIA"),
		re: regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), check: random},
	{kind: KindPrivateKey, label: "private key", anchors: lits("PRIVATE KEY"),
		re: regexp.MustCompile(`-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY(?: BLOCK)?-----`), check: pemBody},
	{kind: KindGitHub, label: "GitHub token", anchors: lits("ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_"),
		re: regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{36}\b|github_pat_[A-Za-z0-9_]{60,})`), check: random},
}

// Placeholder detection: real keys are random, examples in documentation
// are not.
const (
	minDistinct = 10
	maxRun      = 6
)

// random rejects placeholders: too few distinct characters, a long run of
// one character, or the word EXAMPLE.
func random(match, _ []byte) bool {
	if bytes.Contains(bytes.ToUpper(match), []byte("EXAMPLE")) {
		return false
	}
	var seen [256]bool
	distinct, run := 0, 0
	for i, c := range match {
		if !seen[c] {
			seen[c] = true
			distinct++
		}
		if i > 0 && match[i-1] == c {
			run++
			if run >= maxRun-1 {
				return false
			}
		} else {
			run = 0
		}
	}
	return distinct >= minDistinct
}

func isTokenByte(c byte) bool {
	return c == '_' || c == '-' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// telegramToken requires the token to end where the match ends.
func telegramToken(match, tail []byte) bool {
	if len(tail) > 0 && isTokenByte(tail[0]) {
		return false
	}
	return random(match, tail)
}

// serviceRoleJWT decodes the payload of a JWT and reports whether it grants
// the service_role (a server-only key of Supabase and similar services).
func serviceRoleJWT(match, _ []byte) bool {
	parts := bytes.Split(match, []byte("."))
	if len(parts) != 3 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(string(bytes.TrimRight(parts[1], "=")))
	if err != nil {
		return false
	}
	var claims struct {
		Role any `json:"role"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return false
	}
	role, _ := claims.Role.(string)
	return role == "service_role"
}

// minPEMBody is how many base64 characters must follow a PEM header for it
// to be a key rather than a placeholder.
const (
	minPEMBody   = 64
	pemLookahead = 512
)

// pemBody accepts a header followed by key material, also inside a JSON
// string where line breaks are written as \n.
func pemBody(_, tail []byte) bool {
	n := 0
	for i := 0; i < len(tail) && i < pemLookahead; i++ {
		c := tail[i]
		switch {
		case c == ' ' || c == '\n' || c == '\r' || c == '\t':
		case c == '\\' && i+1 < len(tail) && (tail[i+1] == 'n' || tail[i+1] == 'r'):
			i++
		case c == '+' || c == '/' || c == '=' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			n++
			if n >= minPEMBody {
				return true
			}
		default:
			return false
		}
	}
	return false
}
