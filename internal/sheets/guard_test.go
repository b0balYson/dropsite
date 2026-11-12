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

func roundTrip(rt http.RoundTripper, u string) error {
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	resp, err := rt.RoundTrip(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	return err
}

func TestGuard(t *testing.T) {
	next := &okTransport{}
	g := guard{next: next}
	for _, u := range []string{
		"https://sheets.googleapis.com/v4/spreadsheets/x",
		"https://oauth2.googleapis.com/token",
	} {
		if err := roundTrip(g, u); err != nil {
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
		if err := roundTrip(g, u); !errors.Is(err, ErrHostNotAllowed) {
			t.Fatalf("%s allowed: %v", u, err)
		}
	}
	if next.hits != 2 {
		t.Fatalf("hits %d", next.hits)
	}
}

func TestNoRedirects(t *testing.T) {
	c := newHTTPClient(&okTransport{})
	if err := c.CheckRedirect(nil, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatal("redirects followed")
	}
}

func TestTransportRefusesPrivateAddresses(t *testing.T) {
	if err := roundTrip(NewTransport(nil), "http://127.0.0.1:1/"); err == nil {
		t.Fatal("connection to 127.0.0.1 allowed")
	}
}
