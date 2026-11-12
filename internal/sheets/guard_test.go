package sheets

import (
	"errors"
	"net/http"
	"testing"
)

type okTransport struct{ hits int }

func (o *okTransport) RoundTrip(*http.Request) (*http.Response, error) {
	o.hits++
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
}

func TestGuard(t *testing.T) {
	next := &okTransport{}
	g := guard{next: next}
	for _, u := range []string{
		"https://sheets.googleapis.com/v4/spreadsheets/x",
		"https://oauth2.googleapis.com/token",
	} {
		req, _ := http.NewRequest(http.MethodGet, u, nil)
		if _, err := g.RoundTrip(req); err != nil {
			t.Fatalf("%s refused: %v", u, err)
		}
	}
	for _, u := range []string{
		"http://sheets.googleapis.com/v4/spreadsheets/x",
		"https://sheets.googleapis.com:8443/v4/spreadsheets/x",
		"https://user:pw@sheets.googleapis.com/",
		"https://evil.com/",
		"https://sheets.googleapis.com.evil.com/",
		"https://www.googleapis.com/",
	} {
		req, _ := http.NewRequest(http.MethodGet, u, nil)
		if _, err := g.RoundTrip(req); !errors.Is(err, ErrHostNotAllowed) {
			t.Fatalf("%s allowed: %v", u, err)
		}
	}
	if next.hits != 2 {
		t.Fatalf("hits %d", next.hits)
	}
}

func TestNoRedirects(t *testing.T) {
	c := newHTTPClient(&okTransport{})
	if c.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
		t.Fatal("redirects followed")
	}
}
