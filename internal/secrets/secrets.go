// Package secrets finds high-confidence secret credentials (secret API keys,
// tokens, private keys) in the text files of a site before it is published.
// It reports where a secret is and what kind it is, never its value. Public
// keys meant for web pages (Supabase anon, Stripe pk_, Firebase web API keys)
// are not secrets and are not reported.
package secrets

import (
	"bytes"
	"context"
	"fmt"
	"strings"
)

// Kinds of secrets, stable codes for clients.
const (
	KindServiceRole    = "service_role_jwt"
	KindSupabaseSecret = "supabase_secret_key"
	KindStripe         = "stripe_secret_key"
	KindOpenAI         = "openai_api_key"
	KindAnthropic      = "anthropic_api_key"
	KindTelegram       = "telegram_bot_token"
	KindAWS            = "aws_access_key"
	KindPrivateKey     = "private_key"
	KindGitHub         = "github_token"
)

// Finding is one secret: where it is and what kind, never the value.
type Finding struct {
	Path  string // slash-separated path inside the site
	Line  int    // 1-based line number
	Kind  string // one of the Kind constants
	Label string // English name of the kind, e.g. "Stripe secret key"
}

// ScanText returns the secrets in one file's content, in order of position.
func ScanText(path string, data []byte) []Finding {
	var out []Finding
	for _, r := range rules {
		if !r.mayMatch(data) {
			continue
		}
		for _, loc := range r.re.FindAllIndex(data, maxPerRule) {
			if r.check != nil && !r.check(data[loc[0]:loc[1]], data[loc[1]:]) {
				continue
			}
			out = append(out, Finding{Path: path, Line: lineOf(data, loc[0]), Kind: r.kind, Label: r.label})
		}
	}
	sortFindings(out)
	return out
}

// Scan walks dir and returns the secrets in its text files, sorted by path
// and line. Binary files are skipped and only the first MaxFileBytes of a
// file are read.
func Scan(ctx context.Context, dir string) ([]Finding, error) {
	return scanDir(ctx, dir)
}

// Describe renders findings as "path:line (kind); …", at most max entries
// followed by "and N more".
func Describe(fs []Finding, max int) string {
	parts := make([]string, 0, min(len(fs), max)+1)
	for i, f := range fs {
		if i == max {
			parts = append(parts, fmt.Sprintf("and %d more", len(fs)-max))
			break
		}
		parts = append(parts, fmt.Sprintf("%s:%d (%s)", f.Path, f.Line, f.Label))
	}
	return strings.Join(parts, "; ")
}

func lineOf(data []byte, offset int) int {
	return bytes.Count(data[:offset], []byte("\n")) + 1
}
