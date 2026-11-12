package api

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// stalledRequest sends headers announcing a body that never fully arrives and
// reports whether the server answered (or closed the connection) within wait.
func stalledRequest(t *testing.T, addr, method, path, token string, wait time.Duration) bool {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_, err = fmt.Fprintf(conn, "%s %s HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer %s\r\n"+
		"Content-Type: application/json\r\nContent-Length: 1000\r\n\r\n{\"title\":", method, path, token)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(wait))
	_, err = bufio.NewReader(conn).ReadByte()
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() || errors.Is(err, os.ErrDeadlineExceeded) {
		return false
	}
	return true
}

func TestShortReadDeadlineExceptUploads(t *testing.T) {
	old := requestReadTimeout
	requestReadTimeout = 200 * time.Millisecond
	t.Cleanup(func() { requestReadTimeout = old })
	h := newHarness(t)
	site := h.createSite(h.adminToken, "Slow", "")
	srv := httptest.NewServer(h.h)
	t.Cleanup(srv.Close)
	addr := srv.Listener.Addr().String()

	if !stalledRequest(t, addr, http.MethodPost, "/api/v1/sites", h.adminToken, 3*time.Second) {
		t.Fatal("a stalled non-upload request must be cut off by the short read deadline")
	}
	if stalledRequest(t, addr, http.MethodPost, "/api/v1/sites/"+site.ID+"/deploys", h.adminToken, time.Second) {
		t.Fatal("archive uploads must not get the short read deadline")
	}
}

func TestIsUpload(t *testing.T) {
	cases := []struct {
		method, path string
		want         bool
	}{
		{http.MethodPost, "/api/v1/sites/abc/deploys", true},
		{http.MethodGet, "/api/v1/sites/abc/deploys", false},
		{http.MethodPost, "/api/v1/sites/abc/rollback", false},
		{http.MethodPost, "/api/v1/sites/a/b/deploys", false},
		{http.MethodPost, "/api/v1/sites//deploys", false},
		{http.MethodPost, "/api/v1/sites", false},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		if got := isUpload(r); got != tc.want {
			t.Errorf("%s %s: got %v", tc.method, tc.path, got)
		}
	}
}
