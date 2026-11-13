package sheets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Error is a failed call to Google. Status is the HTTP status (0 when the
// request did not get an answer); Reason is Google's own text, for logs
// only: it is never shown to users.
type Error struct {
	Status int
	Reason string
}

func (e *Error) Error() string {
	switch {
	case e.Status == 0:
		return "Google did not answer; try again later"
	case e.Status == http.StatusUnauthorized:
		return "Google rejected the service account key"
	case e.Status == http.StatusForbidden:
		return "the service account has no access to this spreadsheet"
	case e.Status == http.StatusNotFound:
		return "the spreadsheet or sheet was not found"
	case e.Status == http.StatusTooManyRequests:
		return "Google limits requests right now; try again later"
	case e.Status >= 500:
		return "Google Sheets is unavailable right now; try again later"
	}
	return "Google refused the request"
}

// IsAccess reports a missing permission or a missing spreadsheet or sheet.
func IsAccess(err error) bool {
	var e *Error
	return errors.As(err, &e) && (e.Status == http.StatusForbidden || e.Status == http.StatusNotFound)
}

// IsAuth reports a key Google does not accept.
func IsAuth(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusUnauthorized
}

// Reason returns Google's text of err for logs.
func Reason(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return strconv.Itoa(e.Status) + " " + e.Reason
	}
	if err == nil {
		return ""
	}
	return err.Error()
}

// Sheet is one tab of a spreadsheet.
type Sheet struct {
	ID    int64
	Title string
}

// Client calls Google as one service account. It is safe for concurrent use
// and keeps the access token until shortly before it expires.
type Client struct {
	acct Account
	http *http.Client
	now  func() time.Time

	mu      sync.Mutex
	token   string
	expires time.Time
}

// New builds a client; tr is the transport under the host guard (use
// NewTransport in production).
func New(acct Account, tr http.RoundTripper, now func() time.Time) *Client {
	if now == nil {
		now = time.Now
	}
	return &Client{acct: acct, http: newHTTPClient(tr), now: now}
}

// Email is the service account address users share spreadsheets with.
func (c *Client) Email() string { return c.acct.Email }

// Token returns a valid access token, asking Google for a new one when
// needed.
func (c *Client) Token(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if c.token != "" && now.Before(c.expires) {
		return c.token, nil
	}
	jwt, err := c.acct.assertion(now)
	if err != nil {
		return "", err
	}
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {jwt}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := c.do(req, &out); err != nil {
		var e *Error
		if errors.As(err, &e) && e.Status == http.StatusBadRequest {
			// invalid_grant: a revoked or deleted key.
			e.Status = http.StatusUnauthorized
		}
		return "", err
	}
	if out.AccessToken == "" {
		return "", &Error{Status: http.StatusBadGateway, Reason: "empty access token"}
	}
	life := time.Duration(out.ExpiresIn) * time.Second
	if life <= 0 || life > time.Hour {
		life = time.Hour
	}
	c.token, c.expires = out.AccessToken, now.Add(life-time.Minute)
	return c.token, nil
}

// do sends req and decodes a JSON answer into out (nil skips it).
func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		if errors.Is(err, ErrHostNotAllowed) {
			return err
		}
		return &Error{Reason: err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		return &Error{Reason: "read response: " + err.Error()}
	}
	if len(body) > MaxResponseBytes {
		return &Error{Status: http.StatusRequestEntityTooLarge, Reason: "response larger than 10 MB"}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &Error{Status: resp.StatusCode, Reason: googleReason(body)}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return &Error{Status: http.StatusBadGateway, Reason: "decode response: " + err.Error()}
	}
	return nil
}

func googleReason(body []byte) string {
	var e struct {
		Error any `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error != nil {
		if m, ok := e.Error.(map[string]any); ok {
			if s, ok := m["message"].(string); ok {
				return s
			}
		}
		if s, ok := e.Error.(string); ok {
			return s
		}
	}
	if len(body) > 200 {
		body = body[:200]
	}
	return string(body)
}

// api sends an authorized Sheets API request.
func (c *Client) api(ctx context.Context, method, path string, q url.Values, in, out any) error {
	token, err := c.Token(ctx)
	if err != nil {
		return err
	}
	u := APIBase + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.do(req, out)
}

func valuesPath(id, a1 string) string {
	return url.PathEscape(id) + "/values/" + url.PathEscape(a1)
}

// Sheets lists the tabs of a spreadsheet.
func (c *Client) Sheets(ctx context.Context, id string) ([]Sheet, error) {
	var out struct {
		Sheets []struct {
			Properties struct {
				SheetID int64  `json:"sheetId"`
				Title   string `json:"title"`
			} `json:"properties"`
		} `json:"sheets"`
	}
	if err := c.api(ctx, http.MethodGet, url.PathEscape(id), url.Values{"fields": {"sheets.properties(sheetId,title)"}}, nil, &out); err != nil {
		return nil, err
	}
	res := make([]Sheet, 0, len(out.Sheets))
	for _, s := range out.Sheets {
		res = append(res, Sheet{ID: s.Properties.SheetID, Title: s.Properties.Title})
	}
	return res, nil
}

// Resolve finds the sheet a reference points to (the first one without a
// gid); a missing sheet is a 404 Error.
func (c *Client) Resolve(ctx context.Context, ref Ref) (Sheet, error) {
	list, err := c.Sheets(ctx, ref.SpreadsheetID)
	if err != nil {
		return Sheet{}, err
	}
	for _, s := range list {
		if ref.GID == nil || s.ID == *ref.GID {
			return s, nil
		}
	}
	return Sheet{}, &Error{Status: http.StatusNotFound, Reason: "sheet not found"}
}

// Values reads a range as displayed (every value a string).
func (c *Client) Values(ctx context.Context, id, a1 string) ([][]string, error) {
	var out struct {
		Values [][]any `json:"values"`
	}
	q := url.Values{"valueRenderOption": {"FORMATTED_VALUE"}, "majorDimension": {"ROWS"}}
	if err := c.api(ctx, http.MethodGet, valuesPath(id, a1), q, nil, &out); err != nil {
		return nil, err
	}
	rows := make([][]string, len(out.Values))
	for i, r := range out.Values {
		row := make([]string, len(r))
		for j, v := range r {
			switch t := v.(type) {
			case string:
				row[j] = t
			case nil:
			default:
				row[j] = fmt.Sprint(t)
			}
		}
		rows[i] = row
	}
	return rows, nil
}

type valueRange struct {
	MajorDimension string     `json:"majorDimension"`
	Values         [][]string `json:"values"`
}

// Update writes rows to a range as plain text (formulas are not evaluated).
func (c *Client) Update(ctx context.Context, id, a1 string, rows [][]string) error {
	return c.api(ctx, http.MethodPut, valuesPath(id, a1), url.Values{"valueInputOption": {"RAW"}},
		valueRange{MajorDimension: "ROWS", Values: rows}, nil)
}

// Append adds rows after the data of a range as plain text.
func (c *Client) Append(ctx context.Context, id, a1 string, rows [][]string) error {
	q := url.Values{"valueInputOption": {"RAW"}, "insertDataOption": {"INSERT_ROWS"}}
	return c.api(ctx, http.MethodPost, valuesPath(id, a1)+":append", q, valueRange{MajorDimension: "ROWS", Values: rows}, nil)
}
