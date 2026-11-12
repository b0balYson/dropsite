package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/b0balYson/dropsite/internal/service"
	"github.com/b0balYson/dropsite/internal/testenv"
)

// logBuffer is a concurrency-safe sink for the JSON logger.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type harness struct {
	t          *testing.T
	env        *testenv.Env
	h          http.Handler
	logs       *logBuffer
	adminToken string
}

func newHarness(t *testing.T, opts ...testenv.Option) *harness {
	t.Helper()
	env := testenv.New(t, opts...)
	logs := &logBuffer{}
	log := slog.New(slog.NewJSONHandler(logs, nil))
	return &harness{t: t, env: env, h: New(env.Svc, log), logs: logs, adminToken: env.Token(env.Admin, "admin")}
}

// user creates an active user and returns the actor and a token.
func (h *harness) user(email string) (service.Actor, string) {
	h.t.Helper()
	a := h.env.User(email)
	return a, h.env.Token(a, "test")
}

type reqOpt func(*http.Request)

func withHeader(k, v string) reqOpt { return func(r *http.Request) { r.Header.Set(k, v) } }

func (h *harness) do(method, path, token string, body any, opts ...reqOpt) *httptest.ResponseRecorder {
	h.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	case []byte:
		rd = bytes.NewReader(b)
	case io.Reader:
		rd = b
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			h.t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if rd != nil && body != nil {
		if _, isReader := body.(io.Reader); !isReader {
			req.Header.Set("Content-Type", "application/json")
		}
	}
	for _, o := range opts {
		o(req)
	}
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, req)
	return rec
}

func (h *harness) deploy(ref, token string, archive []byte, opts ...reqOpt) *httptest.ResponseRecorder {
	h.t.Helper()
	body, ctype := multipartBody(h.t, "archive", archive)
	return h.do(http.MethodPost, "/api/v1/sites/"+ref+"/deploys", token, bytes.NewReader(body),
		append([]reqOpt{withHeader("Content-Type", ctype)}, opts...)...)
}

func multipartBody(t testing.TB, field string, data []byte) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("note", "ignored"); err != nil {
		t.Fatal(err)
	}
	fw, err := mw.CreateFormFile(field, "site.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), mw.FormDataContentType()
}

func index(body string) map[string]string { return map[string]string{"index.html": body} }

func decodeAs[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	dec := json.NewDecoder(bytes.NewReader(rec.Body.Bytes()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("decode %T: %v; body %s", v, err, rec.Body.String())
	}
	return v
}

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, status int) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status %d, want %d; body %s", rec.Code, status, rec.Body.String())
	}
}

// wantError checks status and error code and returns the payload.
func wantError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) errorPayload {
	t.Helper()
	wantStatus(t, rec, status)
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("content type %q", ct)
	}
	env := decodeAs[errorEnvelope](t, rec)
	if env.Error.Code != code {
		t.Fatalf("code %q, want %q; body %s", env.Error.Code, code, rec.Body.String())
	}
	if env.Error.Message == "" {
		t.Fatal("empty error message")
	}
	return env.Error
}

// createSite creates a site over the API and returns it.
func (h *harness) createSite(token, title, slug string) siteDTO {
	h.t.Helper()
	body := map[string]string{"title": title}
	if slug != "" {
		body["slug"] = slug
	}
	rec := h.do(http.MethodPost, "/api/v1/sites", token, body)
	wantStatus(h.t, rec, http.StatusCreated)
	return decodeAs[siteDTO](h.t, rec)
}
