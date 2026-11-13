package domain

import "regexp"

// ConnectLinkSegment follows the MCP path in a connect URL:
// https://api.<domain>/mcp/c/<code>.
const ConnectLinkSegment = "/c/"

// ConnectCodeFromLinkPath returns the normalized code of a link path suffix
// (what follows "/mcp/c/"), or "" when it cannot be a connect code. The
// suffix must be one path segment of letters, digits and dashes, so it is
// safe to echo back in a URL.
func ConnectCodeFromLinkPath(rest string) string {
	if rest == "" || len(rest) > 64 {
		return ""
	}
	for _, r := range rest {
		if r != '-' && (r < '0' || r > '9') && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
			return ""
		}
	}
	return NormalizeConnectCode(rest)
}

// linkInText matches the code of a connect URL in a path, a URL or a
// percent-encoded query value.
var linkInText = regexp.MustCompile(`(?i)(mcp(?:/|%2f)c(?:/|%2f))[^/?&#%\s"]+`)

// RedactConnectLinks replaces the code of every connect URL in s, so paths
// and URLs can be logged without the secret.
func RedactConnectLinks(s string) string {
	return linkInText.ReplaceAllString(s, "${1}REDACTED")
}
