package service

import (
	"context"
	"errors"
	"image"
	"io/fs"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/designref"
	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/render"
	"github.com/b0balYson/dropsite/internal/sitefetch"
	"github.com/b0balYson/dropsite/internal/visualdiff"
)

// fakePages serves documents by address; redirects map an address to its
// final one.
type fakePages struct {
	mu        sync.Mutex
	docs      map[string]sitefetch.Document
	redirects map[string]string
	calls     []string
	agents    []string
}

func (f *fakePages) Get(_ context.Context, raw string, o sitefetch.GetOptions) (sitefetch.Document, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, raw)
	f.agents = append(f.agents, o.UserAgent)
	if to, ok := f.redirects[raw]; ok {
		raw = to
	}
	d, ok := f.docs[raw]
	if !ok {
		return sitefetch.Document{}, &sitefetch.Error{URL: raw, Reason: "the server answered HTTP 404"}
	}
	for _, t := range o.Types {
		if t == d.ContentType {
			d.URL = raw
			return d, nil
		}
	}
	return sitefetch.Document{}, &sitefetch.Error{URL: raw, Reason: `content type "` + d.ContentType + `" is not accepted here`}
}

const refPage = `<html><head><title>Ref</title>
<link rel="stylesheet" href="/a.css"><link rel="stylesheet" href="https://other.example/x.css">
<script>alert(1)</script></head>
<body><header>Top</header><section><h1>Secret headline</h1></section></body></html>`

func refPages() *fakePages {
	html := func(s string) sitefetch.Document {
		return sitefetch.Document{ContentType: "text/html", Data: []byte(s)}
	}
	css := func(s string) sitefetch.Document { return sitefetch.Document{ContentType: "text/css", Data: []byte(s)} }
	return &fakePages{
		redirects: map[string]string{"https://ref.example/": "https://www.ref.example/"},
		docs: map[string]sitefetch.Document{
			"https://www.ref.example/":      html(refPage),
			"https://www.ref.example/a.css": css(`@import "b.css"; body{background:#101820;color:#f2f2f2} .btn{background:#ff6f3c}`),
			"https://www.ref.example/b.css": css(`h1{font-family:"Space Grotesk",sans-serif}`),
			"https://ref.example/logo.png":  {ContentType: "image/png", Data: []byte("png")},
		},
	}
}

// snapshotRenderer reads the snapshot through the render token, like the
// real renderer, and records what it saw.
type snapshotRenderer struct {
	svc  *Service
	seen map[string]string
	fail bool
}

func (r *snapshotRenderer) Screenshot(_ context.Context, shot render.Shot) ([]byte, error) {
	if r.fail {
		return nil, render.ErrUnavailable
	}
	files, done, ok := r.svc.OpenRenderRelease(shot.Token)
	if !ok {
		return nil, errors.New("token rejected")
	}
	defer done()
	r.seen = map[string]string{}
	_ = fs.WalkDir(files, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := fs.ReadFile(files, p)
			r.seen[p] = string(b)
		}
		return nil
	})
	return visualdiff.EncodePNG(image.NewRGBA(image.Rect(0, 0, 4, 4)))
}

func TestInspectReferenceBrief(t *testing.T) {
	e := newEnv(t)
	pages := refPages()
	e.svc.pages = pages
	u := e.user("u@pp.test")
	ref, err := e.svc.InspectReference(context.Background(), u, "https://ref.example/")
	if err != nil {
		t.Fatal(err)
	}
	b := ref.Brief
	if b.URL != "https://www.ref.example/" || b.Title != "Ref" || b.ColorScheme != designref.SchemeDark {
		t.Fatalf("brief %+v", b)
	}
	if b.Stylesheets.Fetched != 2 || b.Stylesheets.Skipped != 1 {
		t.Errorf("stylesheets %+v (calls %v)", b.Stylesheets, pages.calls)
	}
	if len(b.Fonts) == 0 || b.Fonts[0].Family != "Space Grotesk" {
		t.Errorf("fonts %+v", b.Fonts)
	}
	for _, c := range pages.calls {
		if strings.Contains(c, "other.example") {
			t.Errorf("fetched a stylesheet of another server: %v", pages.calls)
		}
	}
	for _, a := range pages.agents {
		if a != ReferenceUserAgent {
			t.Errorf("user agent %q", a)
		}
	}
	if ref.Shot.Status != designref.ShotUnavailable || ref.Shot.PNG != nil {
		t.Errorf("shot without renderer: %+v", ref.Shot)
	}
}

func TestInspectReferenceScreenshot(t *testing.T) {
	e := newEnv(t)
	e.svc.pages = refPages()
	r := &snapshotRenderer{svc: e.svc}
	e.svc.shooter = r
	u := e.user("u@pp.test")
	ref, err := e.svc.InspectReference(context.Background(), u, "https://ref.example/")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Shot.Status != designref.ShotIncluded || len(ref.Shot.PNG) == 0 || ref.Shot.Width != 1280 {
		t.Fatalf("shot %+v", ref.Shot)
	}
	page := r.seen[designref.SnapshotIndex]
	if strings.Contains(page, "<script") || !strings.Contains(page, "s/0.css") || len(r.seen) != 3 {
		t.Errorf("renderer saw %v", r.seen)
	}
	// The token is revoked after the shot.
	e.svc.grantsMu.Lock()
	n := len(e.svc.grants)
	e.svc.grantsMu.Unlock()
	if n != 0 {
		t.Errorf("%d grants left", n)
	}
	r.fail = true
	ref, err = e.svc.InspectReference(context.Background(), u, "https://ref.example/")
	if err != nil || ref.Shot.Status != designref.ShotUnavailable {
		t.Fatalf("failing renderer: %+v %v", ref.Shot, err)
	}
}

func TestInspectReferenceErrors(t *testing.T) {
	e := newEnv(t)
	e.svc.pages = refPages()
	u := e.user("u@pp.test")
	ctx := context.Background()
	for _, raw := range []string{"http://ref.example/", "ftp://x", "https://localhost/", "https://10.0.0.1/", ""} {
		_, err := e.svc.InspectReference(ctx, u, raw)
		wantCode(t, err, domain.CodeInvalidInput)
	}
	_, err := e.svc.InspectReference(ctx, u, "https://ref.example/logo.png")
	wantCode(t, err, domain.CodeFetchFailed)
	if !strings.Contains(domain.AsError(err).Message, "not a web page") {
		t.Errorf("message %q", domain.AsError(err).Message)
	}
	_, err = e.svc.InspectReference(ctx, u, "https://ref.example/missing")
	wantCode(t, err, domain.CodeFetchFailed)
	_, err = e.svc.InspectReference(ctx, Actor{}, "https://ref.example/")
	wantCode(t, err, domain.CodeUnauthorized)
}

func TestInspectReferenceRateLimit(t *testing.T) {
	e := newEnv(t)
	e.svc.pages = refPages()
	u := e.user("u@pp.test")
	other := e.user("o@pp.test")
	ctx := context.Background()
	for i := range ReferencesPerHour {
		if _, err := e.svc.InspectReference(ctx, u, "https://ref.example/"); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	_, err := e.svc.InspectReference(ctx, u, "https://ref.example/")
	wantCode(t, err, domain.CodeRateLimited)
	if _, err := e.svc.InspectReference(ctx, other, "https://ref.example/"); err != nil {
		t.Fatalf("another user is limited: %v", err)
	}
	e.clock.Advance(time.Hour + time.Second)
	if _, err := e.svc.InspectReference(ctx, u, "https://ref.example/"); err != nil {
		t.Fatalf("after an hour: %v", err)
	}
}

func TestInspectReferenceSheetLimits(t *testing.T) {
	e := newEnv(t)
	pages := &fakePages{docs: map[string]sitefetch.Document{}}
	var links strings.Builder
	for i := range MaxReferenceSheets + 3 {
		u := "https://many.example/" + string(rune('a'+i)) + ".css"
		links.WriteString(`<link rel="stylesheet" href="` + u + `">`)
		pages.docs[u] = sitefetch.Document{ContentType: "text/css", Data: []byte("body{color:#123456}")}
	}
	pages.docs["https://many.example/"] = sitefetch.Document{ContentType: "text/html",
		Data: []byte("<html><head>" + links.String() + "</head><body></body></html>")}
	e.svc.pages = pages
	ref, err := e.svc.InspectReference(context.Background(), e.user("u@pp.test"), "https://many.example/")
	if err != nil {
		t.Fatal(err)
	}
	st := ref.Brief.Stylesheets
	if st.Fetched != MaxReferenceSheets || st.Skipped != 3 || !st.Truncated || len(pages.calls) != MaxReferenceSheets+1 {
		t.Fatalf("stylesheets %+v, %d calls", st, len(pages.calls))
	}
}
