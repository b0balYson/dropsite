package main

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/brand"
	"github.com/b0balYson/dropsite/internal/client"
	"github.com/b0balYson/dropsite/internal/domain"
)

// usageError means the command line was wrong: exit code 2.
type usageError struct {
	msg   string
	shown bool // the flag package already printed the problem
}

func (e *usageError) Error() string { return e.msg }

func usagef(format string, args ...any) error {
	return &usageError{msg: fmt.Sprintf(format, args...)}
}

// hintError adds follow-up advice to an error.
type hintError struct {
	err   error
	hints []string
}

func (e *hintError) Error() string { return e.err.Error() }
func (e *hintError) Unwrap() error { return e.err }

func withHint(err error, hints ...string) error { return &hintError{err: err, hints: hints} }

var errNotLoggedIn = withHint(errors.New("not logged in"),
	"Press Connect on the Connect AI page of the dropsite web interface and run the command shown there,",
	fmt.Sprintf("or run '%s login --server <url> --token <token>', or set %s and %s.", brand.Name, envServer, envToken))

// describeError renders err as one message line plus hint lines.
func describeError(err error) (string, []string) {
	var hints []string
	var he *hintError
	if errors.As(err, &he) {
		hints = he.hints
	}
	var ae *client.APIError
	if errors.As(err, &ae) {
		return apiMessage(err, ae), append(apiHints(ae), hints...)
	}
	var de *domain.Error
	if errors.As(err, &de) {
		return fmt.Sprintf("%s (%s)", de.Message, de.Code), append(detailLines(de.Params), hints...)
	}
	return err.Error(), hints
}

// apiMessage keeps any context added around the API error (e.g. which site)
// while showing the server's message and code.
func apiMessage(err error, ae *client.APIError) string {
	msg := fmt.Sprintf("%s (HTTP %d)", ae.Message, ae.Status)
	if ae.Code != "" {
		msg = fmt.Sprintf("%s (%s)", ae.Message, ae.Code)
	}
	if prefix, ok := strings.CutSuffix(err.Error(), ae.Error()); ok && prefix != "" {
		return prefix + msg
	}
	return msg
}

func apiHints(ae *client.APIError) []string {
	hints := detailLines(ae.Details)
	switch ae.Status {
	case http.StatusUnauthorized:
		hints = append(hints, fmt.Sprintf("The token was rejected. Run '%s login --server <url> --token <token>' with a valid token.", brand.Name))
	case http.StatusTooManyRequests:
		hints = append(hints, retryHint(ae.RetryAfter))
	}
	return hints
}

func retryHint(d time.Duration) string {
	if d <= 0 {
		return "Too many requests. Wait a little and retry."
	}
	secs := int(math.Ceil(d.Seconds()))
	return fmt.Sprintf("Too many requests. Retry in %d seconds.", secs)
}

func detailLines(details map[string]any) []string {
	keys := make([]string, 0, len(details))
	for k := range details {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf("%s: %v", k, details[k]))
	}
	return lines
}
