// Package domain holds the core types, identifiers, validation rules and
// error codes shared by every layer of the control server.
package domain

import (
	"errors"
	"fmt"
	"time"
)

// Kind classifies an error for transport mapping (HTTP status, UI message).
type Kind int

const (
	KindInternal Kind = iota
	KindNotFound
	KindInvalid
	KindConflict
	KindLimit
	KindRateLimited
	KindUnauthorized
	KindTooLarge
)

// Stable machine-readable error codes. API clients and UI translations key on these.
const (
	CodeInternal       = "internal"
	CodeNotFound       = "not_found"
	CodeUnauthorized   = "unauthorized"
	CodeInvalidInput   = "invalid_input"
	CodeRateLimited    = "rate_limited"
	CodeEmailTaken     = "email_taken"
	CodeEmailInvalid   = "email_invalid"
	CodePasswordShort  = "password_too_short"
	CodeBadCredentials = "bad_credentials"
	CodeInviteInvalid  = "invite_invalid"
	CodeUserBlocked    = "user_blocked"
	CodeSelfAction     = "self_action_forbidden"
	CodeSlugInvalid    = "slug_invalid"
	CodeSlugReserved   = "slug_reserved"
	CodeSlugTaken      = "slug_taken"
	CodeTitleInvalid   = "title_invalid"
	CodeSiteLimit      = "site_limit_reached"
	CodeDeployNotReady = "deploy_not_ready"
	CodeNoPrevious     = "no_previous_deploy"
	CodeTokenInvalid   = "token_name_invalid"
	CodeSettingInvalid = "setting_invalid"
	// CodeConnectInvalid covers unknown, used and expired connect codes alike.
	CodeConnectInvalid = "connect_code_invalid"
	// CodeFilesTooLarge rejects an MCP file set above the inline limit.
	CodeFilesTooLarge  = "files_too_large"
	CodeCounterInvalid = "counter_id_invalid"

	// Editing site files through MCP (change mcp-site-files).
	CodeDeployConflict   = "deploy_conflict"
	CodeSiteNotPublished = "site_not_published"
	CodeUploadExpired    = "upload_expired"
	CodeUploadLimit      = "upload_limit_reached"
	CodeFetchFailed      = "fetch_failed"

	// External domain codes.
	CodeDomainInvalid       = "domain_invalid"
	CodeDomainForbidden     = "domain_forbidden"
	CodeDomainTaken         = "domain_taken"
	CodeDomainLimit         = "domain_limit_reached"
	CodeDomainReasonInvalid = "domain_reason_invalid"

	// Site access controls (stage 2).
	CodeSitePasswordRequired = "site_password_required"
	CodeExpiryInvalid        = "expiry_invalid"

	// Archive validation codes (produced by internal/archive).
	CodeArchiveFormat      = "archive_unsupported_format"
	CodeArchiveCorrupt     = "archive_corrupt"
	CodeArchiveTooLarge    = "archive_too_large"
	CodeArchiveFileTooBig  = "archive_file_too_large"
	CodeArchiveTooMany     = "archive_too_many_files"
	CodeArchiveAbsPath     = "archive_absolute_path"
	CodeArchiveTraversal   = "archive_path_traversal"
	CodeArchiveLink        = "archive_link"
	CodeArchiveSpecialFile = "archive_special_file"
	CodeArchiveBadName     = "archive_bad_name"
	CodeArchiveNoIndex     = "archive_no_index"
	CodeArchiveEmpty       = "archive_empty"

	// Site forms: intake, delivery channels and their settings.
	CodeFormInvalid         = "form_invalid"
	CodeFormEmpty           = "form_empty"
	CodeFormTooLarge        = "form_too_large"
	CodeFormUnsupported     = "form_unsupported_type"
	CodeFormOrigin          = "form_origin_mismatch"
	CodeFormCaptcha         = "form_captcha_invalid"
	CodeChannelInvalid      = "channel_invalid"
	CodeChannelUnavailable  = "channel_unavailable"
	CodeChannelLimit        = "channel_limit_reached"
	CodeChannelCodeInvalid  = "channel_code_invalid"
	CodeTelegramTokenFailed = "telegram_token_invalid"
)

// Error is a domain error with a stable code, an English message for logs and
// API clients, and parameters that UI translations can interpolate.
type Error struct {
	Kind       Kind
	Code       string
	Message    string
	Params     map[string]any
	RetryAfter time.Duration
	cause      error
}

func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.cause)
	}
	return e.Code + ": " + e.Message
}

func (e *Error) Unwrap() error { return e.cause }

// Is lets errors.Is match on code, so sentinel values below work as targets.
func (e *Error) Is(target error) bool {
	var t *Error
	if !errors.As(target, &t) {
		return false
	}
	return t.Code == e.Code
}

// E builds a domain error.
func E(kind Kind, code, message string) *Error {
	return &Error{Kind: kind, Code: code, Message: message}
}

// WithParams returns a copy of e with params attached.
func (e *Error) WithParams(params map[string]any) *Error {
	c := *e
	c.Params = params
	return &c
}

// WithCause returns a copy of e wrapping cause.
func (e *Error) WithCause(cause error) *Error {
	c := *e
	c.cause = cause
	return &c
}

// WithRetryAfter returns a copy of e with a retry hint.
func (e *Error) WithRetryAfter(d time.Duration) *Error {
	c := *e
	c.RetryAfter = d
	return &c
}

// Sentinels used across layers.
var (
	ErrNotFound       = E(KindNotFound, CodeNotFound, "not found")
	ErrUnauthorized   = E(KindUnauthorized, CodeUnauthorized, "authentication required")
	ErrBadCredentials = E(KindUnauthorized, CodeBadCredentials, "invalid email or password")
	ErrRateLimited    = E(KindRateLimited, CodeRateLimited, "too many requests")
)

// AsError extracts a *Error, converting anything else into an internal error
// whose message never leaks the cause.
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	var de *Error
	if errors.As(err, &de) {
		return de
	}
	return E(KindInternal, CodeInternal, "internal error").WithCause(err)
}
