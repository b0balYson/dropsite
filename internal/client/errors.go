package client

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// maxErrorBody bounds how much of an error response is read.
const maxErrorBody = 64 << 10

// maxPlainMessage bounds the message taken from a non-JSON error body.
const maxPlainMessage = 200

// APIError is a non-2xx response from the API.
type APIError struct {
	Status     int
	Code       string
	Message    string
	Details    map[string]any
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("server returned %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("%s (%s, HTTP %d)", e.Message, e.Code, e.Status)
}

// IsNotFound reports whether the error is a 404.
func (e *APIError) IsNotFound() bool { return e.Status == http.StatusNotFound }

// IsUnauthorized reports whether the error is a 401.
func (e *APIError) IsUnauthorized() bool { return e.Status == http.StatusUnauthorized }

type errorEnvelope struct {
	Error *struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

// parseAPIError builds an *APIError from a non-2xx response.
func parseAPIError(resp *http.Response) *APIError {
	e := &APIError{Status: resp.StatusCode, RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	var env errorEnvelope
	if json.Unmarshal(body, &env) == nil && env.Error != nil {
		e.Code = env.Error.Code
		e.Message = env.Error.Message
		e.Details = env.Error.Details
	}
	if e.Message == "" {
		e.Message = plainMessage(body, resp.StatusCode)
	}
	return e
}

func plainMessage(body []byte, status int) string {
	msg := strings.TrimSpace(string(body))
	if msg == "" || strings.HasPrefix(msg, "<") {
		return http.StatusText(status)
	}
	if len(msg) > maxPlainMessage {
		msg = msg[:maxPlainMessage] + "..."
	}
	return msg
}

// parseRetryAfter accepts delay-seconds or an HTTP date.
func parseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return t.Sub(now).Round(time.Second)
	}
	return 0
}
