package forms

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultTelegramAPI is the Bot API base URL.
const DefaultTelegramAPI = "https://api.telegram.org"

// telegramMaxText is the Bot API message limit (4096) minus room for "…".
const telegramMaxText = 4000

var (
	chatIDRe   = regexp.MustCompile(`^-?[0-9]{1,20}$`)
	botTokenRe = regexp.MustCompile(`^[0-9]{1,20}:[A-Za-z0-9_-]{20,100}$`)
)

// ValidChatID reports whether s looks like a Telegram chat ID.
func ValidChatID(s string) bool { return chatIDRe.MatchString(s) }

// ValidBotToken reports whether s looks like a bot token.
func ValidBotToken(s string) bool { return botTokenRe.MatchString(s) }

// Telegram calls the Bot API. Errors never contain the token: it is part of
// every request URL, so transport errors are unwrapped from *url.Error.
type Telegram struct {
	BaseURL string
	Token   string
	Client  *http.Client
}

// NewTelegram returns a client with a request timeout.
func NewTelegram(baseURL, token string) *Telegram {
	if baseURL == "" {
		baseURL = DefaultTelegramAPI
	}
	return &Telegram{
		BaseURL: strings.TrimSuffix(baseURL, "/"), Token: token,
		Client: &http.Client{Timeout: 20 * time.Second},
	}
}

type tgResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
}

func (t *Telegram) call(ctx context.Context, method string, params any, out any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return Permanent("telegram: encode request")
	}
	endpoint := t.BaseURL + "/bot" + t.Token + "/" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Permanent("telegram: invalid API address")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.Client.Do(req) //nolint:gosec // fixed Bot API host from configuration
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return Temporary("telegram: request failed: " + t.redact(err.Error()))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Temporary("telegram: read response failed")
	}
	var r tgResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		return Temporary(fmt.Sprintf("telegram: unexpected response (HTTP %d)", resp.StatusCode))
	}
	if !r.OK {
		msg := fmt.Sprintf("telegram: %d %s", r.ErrorCode, t.redact(r.Description))
		switch r.ErrorCode {
		case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
			return Permanent(msg)
		}
		return Temporary(msg)
	}
	if out != nil {
		if err := json.Unmarshal(r.Result, out); err != nil {
			return Temporary("telegram: unexpected result")
		}
	}
	return nil
}

// redact removes the token should a message ever echo it.
func (t *Telegram) redact(s string) string {
	if t.Token == "" {
		return s
	}
	return strings.ReplaceAll(s, t.Token, "[token]")
}

// GetMe validates the token and returns the bot username.
func (t *Telegram) GetMe(ctx context.Context) (string, error) {
	var me struct {
		Username string `json:"username"`
	}
	if err := t.call(ctx, "getMe", struct{}{}, &me); err != nil {
		return "", err
	}
	return me.Username, nil
}

// Send posts a plain-text message (no parse_mode: nothing is interpreted).
func (t *Telegram) Send(ctx context.Context, chatID, text string) error {
	return t.call(ctx, "sendMessage", map[string]any{
		"chat_id": chatID, "text": truncate(text, telegramMaxText),
		"link_preview_options": map[string]bool{"is_disabled": true},
	}, nil)
}

// Update is an incoming message relevant to chat binding.
type Update struct {
	ID     int64
	ChatID string
	Text   string
}

// Updates fetches pending updates after offset without waiting.
func (t *Telegram) Updates(ctx context.Context, offset int64) ([]Update, error) {
	var raw []struct {
		UpdateID int64 `json:"update_id"`
		Message  *struct {
			Text string `json:"text"`
			Chat struct {
				ID int64 `json:"id"`
			} `json:"chat"`
		} `json:"message"`
	}
	params := map[string]any{"offset": offset, "timeout": 0, "allowed_updates": []string{"message"}}
	if err := t.call(ctx, "getUpdates", params, &raw); err != nil {
		return nil, err
	}
	out := make([]Update, 0, len(raw))
	for _, u := range raw {
		up := Update{ID: u.UpdateID}
		if u.Message != nil {
			up.ChatID = strconv.FormatInt(u.Message.Chat.ID, 10)
			up.Text = u.Message.Text
		}
		out = append(out, up)
	}
	return out, nil
}

// StartCode extracts the code of a "/start <code>" message.
func StartCode(text string) (string, bool) {
	cmd, arg, ok := strings.Cut(strings.TrimSpace(text), " ")
	if !ok || (cmd != "/start" && !strings.HasPrefix(cmd, "/start@")) {
		return "", false
	}
	arg = strings.TrimSpace(arg)
	return arg, len(arg) == LinkCodeLen
}
