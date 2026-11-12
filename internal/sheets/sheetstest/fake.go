// Package sheetstest is an in-memory Google Sheets for tests: a service
// account key and an http.RoundTripper that answers the token endpoint and
// the Sheets API calls the sheets package makes.
package sheetstest

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// Email is the service account address of Key.
const Email = "dropsite@test-project.iam.gserviceaccount.com"

var (
	keyOnce sync.Once
	keyJSON []byte
	keyPub  *rsa.PublicKey
)

func genKey() {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		panic(err)
	}
	p := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	keyJSON, err = json.Marshal(map[string]string{ //nolint:gosec // G101: a throwaway key generated for tests
		"type": "service_account", "project_id": "test-project", "private_key_id": "kid1",
		"private_key": string(p), "client_email": Email, "token_uri": "https://evil.example/token",
	})
	if err != nil {
		panic(err)
	}
	keyPub = &k.PublicKey
}

// Key returns a valid service account JSON key (generated once).
func Key() []byte {
	keyOnce.Do(genKey)
	return keyJSON
}

// Tab is one sheet of a fake spreadsheet.
type Tab struct {
	ID    int64
	Title string
	Rows  [][]string
}

// Fake is the in-memory Google. Its zero value has no spreadsheets.
type Fake struct {
	mu     sync.Mutex
	docs   map[string][]*Tab
	denied map[string]bool
	ro     map[string]bool
	// Status, when set, answers every Sheets API call with this status.
	Status int
	// TokenStatus, when set, answers the token endpoint with this status.
	TokenStatus int
	calls       []string
}

// Add creates or replaces a spreadsheet shared with the service account.
func (f *Fake) Add(id string, tabs ...*Tab) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.docs == nil {
		f.docs = map[string][]*Tab{}
	}
	f.docs[id] = tabs
}

// Deny makes a spreadsheet answer 403 (not shared).
func (f *Fake) Deny(id string, on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.denied == nil {
		f.denied = map[string]bool{}
	}
	f.denied[id] = on
}

// ReadOnly makes writes to a spreadsheet answer 403 (shared as viewer).
func (f *Fake) ReadOnly(id string, on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ro == nil {
		f.ro = map[string]bool{}
	}
	f.ro[id] = on
}

// Rows returns a copy of a sheet's rows.
func (f *Fake) Rows(id string, gid int64) [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.docs[id] {
		if t.ID == gid {
			out := make([][]string, len(t.Rows))
			for i, r := range t.Rows {
				out[i] = append([]string(nil), r...)
			}
			return out
		}
	}
	return nil
}

// SetRows replaces a sheet's rows.
func (f *Fake) SetRows(id string, gid int64, rows [][]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.docs[id] {
		if t.ID == gid {
			t.Rows = rows
		}
	}
}

// Calls returns the requests seen so far as "METHOD host path".
func (f *Fake) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func reply(status int, v any) *http.Response {
	b, _ := json.Marshal(v)
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(bytes.NewReader(b))}
}

func errReply(status int, msg string) *http.Response {
	return reply(status, map[string]any{"error": map[string]any{"code": status, "message": msg}})
}

// RoundTrip implements http.RoundTripper.
func (f *Fake) RoundTrip(r *http.Request) (*http.Response, error) {
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(r.Body)
		_ = r.Body.Close()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Host+" "+r.URL.EscapedPath())
	switch r.URL.Host {
	case "oauth2.googleapis.com":
		return f.token(body), nil
	case "sheets.googleapis.com":
		if r.Header.Get("Authorization") != "Bearer test-token" {
			return errReply(http.StatusUnauthorized, "bad token"), nil
		}
		if f.Status != 0 {
			return errReply(f.Status, "forced failure"), nil
		}
		return f.api(r, body), nil
	}
	return errReply(http.StatusNotFound, "unknown host"), nil
}

func (f *Fake) token(body []byte) *http.Response {
	if f.TokenStatus != 0 {
		return reply(f.TokenStatus, map[string]string{"error": "invalid_grant"})
	}
	form, err := url.ParseQuery(string(body))
	if err != nil || form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" || !validJWT(form.Get("assertion")) {
		return reply(http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
	}
	return reply(http.StatusOK, map[string]any{"access_token": "test-token", "expires_in": 3600, "token_type": "Bearer"})
}

func validJWT(s string) bool {
	keyOnce.Do(genKey)
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(keyPub, crypto.SHA256, sum[:], sig) != nil {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	var c map[string]any
	return json.Unmarshal(raw, &c) == nil && c["iss"] == Email && c["aud"] == "https://oauth2.googleapis.com/token"
}

func (f *Fake) api(r *http.Request, body []byte) *http.Response {
	segs := strings.Split(strings.TrimPrefix(r.URL.EscapedPath(), "/v4/spreadsheets/"), "/")
	id, _ := url.PathUnescape(segs[0])
	tabs, ok := f.docs[id]
	if !ok {
		return errReply(http.StatusNotFound, "Requested entity was not found.")
	}
	if f.denied[id] {
		return errReply(http.StatusForbidden, "The caller does not have permission")
	}
	if len(segs) == 1 && r.Method == http.MethodGet {
		var out []map[string]any
		for _, t := range tabs {
			out = append(out, map[string]any{"properties": map[string]any{"sheetId": t.ID, "title": t.Title}})
		}
		return reply(http.StatusOK, map[string]any{"sheets": out})
	}
	if len(segs) != 3 || segs[1] != "values" {
		return errReply(http.StatusNotFound, "unknown call")
	}
	rng, _ := url.PathUnescape(segs[2])
	appendCall := strings.HasSuffix(rng, ":append")
	rng = strings.TrimSuffix(rng, ":append")
	tab, sub := findTab(tabs, rng)
	if tab == nil {
		return errReply(http.StatusBadRequest, "Unable to parse range: "+rng)
	}
	if r.Method != http.MethodGet && f.ro[id] {
		return errReply(http.StatusForbidden, "The caller does not have permission")
	}
	var in struct {
		Values [][]string `json:"values"`
	}
	_ = json.Unmarshal(body, &in)
	switch {
	case r.Method == http.MethodGet:
		rows := tab.Rows
		if sub == "1:1" {
			rows = rows[:min(1, len(rows))]
		}
		return reply(http.StatusOK, map[string]any{"values": rows})
	case r.Method == http.MethodPut && sub == "1:1" && len(in.Values) == 1:
		if len(tab.Rows) == 0 {
			tab.Rows = [][]string{nil}
		}
		tab.Rows[0] = in.Values[0]
		return reply(http.StatusOK, map[string]any{})
	case r.Method == http.MethodPost && appendCall:
		tab.Rows = append(tab.Rows, in.Values...)
		return reply(http.StatusOK, map[string]any{})
	}
	return errReply(http.StatusBadRequest, "unsupported call")
}

// findTab parses 'Title' or 'Title'!sub.
func findTab(tabs []*Tab, rng string) (*Tab, string) {
	if !strings.HasPrefix(rng, "'") {
		return nil, ""
	}
	end := strings.LastIndex(rng, "'")
	if end <= 0 {
		return nil, ""
	}
	title := strings.ReplaceAll(rng[1:end], "''", "'")
	sub := strings.TrimPrefix(rng[end+1:], "!")
	for _, t := range tabs {
		if t.Title == title {
			return t, sub
		}
	}
	return nil, ""
}
