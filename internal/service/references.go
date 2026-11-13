package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing/fstest"
	"time"

	"github.com/b0balYson/dropsite/internal/designref"
	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/ratelimit"
	"github.com/b0balYson/dropsite/internal/render"
	"github.com/b0balYson/dropsite/internal/sitefetch"
)

// Design references (change design-references, docs/references.md): one
// reference page and its same-site stylesheets become a design brief.
const (
	// ReferencesPerHour caps reference inspections per user.
	ReferencesPerHour = 30
	// ReferenceTimeout bounds one inspection, screenshot included.
	ReferenceTimeout = 30 * time.Second
	// MaxReferencePageBytes caps the reference page.
	MaxReferencePageBytes = 2 << 20
	// MaxReferenceSheets caps the stylesheets read per reference.
	MaxReferenceSheets = 8
	// MaxReferenceSheetBytes caps one stylesheet.
	MaxReferenceSheetBytes = 512 << 10
	// MaxReferenceCSSBytes caps all stylesheets of a reference.
	MaxReferenceCSSBytes = 2 << 20
	// ReferenceUserAgent identifies the fetches without personal data.
	ReferenceUserAgent = "dropsite-reference"
	referenceDevice    = "desktop"
)

// PageFetcher downloads one document through the guarded transport;
// *sitefetch.Client implements it.
type PageFetcher interface {
	Get(ctx context.Context, rawURL string, o sitefetch.GetOptions) (sitefetch.Document, error)
}

type referenceState struct {
	pages    PageFetcher
	refLimit *ratelimit.Window
}

func newReferenceState(d Deps, now func() time.Time) referenceState {
	p := d.PageFetcher
	if p == nil {
		p = sitefetch.New(sitefetch.Options{})
	}
	return referenceState{pages: p, refLimit: ratelimit.New(ReferencesPerHour, time.Hour).WithClock(now)}
}

// ReferenceShot is the first screen of a reference, when available.
type ReferenceShot struct {
	Status string
	Width  int
	Height int
	PNG    []byte
}

// Reference is the result of InspectReference.
type Reference struct {
	Brief designref.Brief
	Shot  ReferenceShot
}

var (
	pageTypes = sitefetch.GetOptions{
		Types:     []string{"text/html", "application/xhtml+xml"},
		MaxBytes:  MaxReferencePageBytes,
		Accept:    "text/html,application/xhtml+xml;q=0.9,*/*;q=0.1",
		UserAgent: ReferenceUserAgent,
	}
	sheetTypes = sitefetch.GetOptions{
		Types:     []string{"text/css", "text/plain"},
		MaxBytes:  MaxReferenceSheetBytes,
		Accept:    "text/css,*/*;q=0.1",
		UserAgent: ReferenceUserAgent,
	}
)

// InspectReference fetches a reference page the user pointed to and returns
// its design brief, with a screenshot when the renderer is configured. The
// brief carries no texts, images or scripts of the page.
func (s *Service) InspectReference(ctx context.Context, a Actor, raw string) (Reference, error) {
	if a.User.ID == 0 {
		return Reference{}, domain.ErrUnauthorized
	}
	if err := sitefetch.ValidateURL(raw); err != nil {
		return Reference{}, domain.E(domain.KindInvalid, domain.CodeInvalidInput, err.Error()).
			WithParams(map[string]any{"field": "url"})
	}
	if ok, retry := s.refLimit.Allow(userKey(a.User.ID)); !ok {
		return Reference{}, domain.ErrRateLimited.WithRetryAfter(retry).
			WithParams(map[string]any{"limit": ReferencesPerHour})
	}
	ctx, cancel := context.WithTimeout(ctx, ReferenceTimeout)
	defer cancel()
	doc, err := s.pages.Get(ctx, raw, pageTypes)
	if err != nil {
		return Reference{}, referenceErr(raw, err)
	}
	page, err := designref.ParsePage(doc.URL, doc.Data)
	if err != nil {
		return Reference{}, fetchErr(raw, "the page could not be read")
	}
	sheets, stats := s.referenceSheets(ctx, page)
	css := make([]string, 0, len(sheets))
	for _, sh := range sheets {
		css = append(css, sh.CSS)
	}
	out := Reference{Brief: page.Brief(css, stats)}
	out.Shot = s.referenceShot(ctx, a, page, sheets)
	return out, nil
}

func referenceErr(raw string, err error) error {
	var fe *sitefetch.Error
	if errors.As(err, &fe) {
		reason := fe.Reason
		if strings.HasPrefix(reason, "content type") {
			reason = "this is not a web page (" + reason + ")"
		}
		return fetchErr(raw, reason)
	}
	return fmt.Errorf("fetch reference: %w", err)
}

// referenceSheets reads the same-site stylesheets of the page and of their
// @import rules, within the count and size limits. A stylesheet that fails
// is skipped, not fatal.
func (s *Service) referenceSheets(ctx context.Context, page *designref.Page) ([]designref.Sheet, designref.Stylesheets) {
	var (
		stats designref.Stylesheets
		out   []designref.Sheet
		total int
	)
	queue := append([]string(nil), page.Stylesheets...)
	seen := map[string]bool{}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		if seen[u] {
			continue
		}
		seen[u] = true
		if !page.SameOrigin(u) {
			stats.Skipped++
			continue
		}
		if len(out) >= MaxReferenceSheets || total >= MaxReferenceCSSBytes {
			stats.Skipped++
			stats.Truncated = true
			continue
		}
		d, err := s.pages.Get(ctx, u, sheetTypes)
		if err != nil || total+len(d.Data) > MaxReferenceCSSBytes {
			stats.Skipped++
			continue
		}
		total += len(d.Data)
		out = append(out, designref.Sheet{URL: u, CSS: string(d.Data)})
		queue = append(queue, designref.Imports(string(d.Data), d.URL)...)
	}
	stats.Fetched = len(out)
	return out, stats
}

// referenceShot renders a sanitized copy of the page; any failure only
// leaves the brief without a picture.
func (s *Service) referenceShot(ctx context.Context, a Actor, page *designref.Page, sheets []designref.Sheet) ReferenceShot {
	none := ReferenceShot{Status: designref.ShotUnavailable}
	if s.shooter == nil {
		return none
	}
	if ok, _ := s.shotLimit.Allow(userKey(a.User.ID)); !ok {
		return none
	}
	files, err := page.Snapshot(sheets)
	if err != nil {
		s.log.Warn("reference snapshot", "err", err)
		return none
	}
	mfs := fstest.MapFS{}
	for name, data := range files {
		mfs[name] = &fstest.MapFile{Data: data, Mode: 0o444}
	}
	size := Devices[referenceDevice]
	token := s.grantRenderFiles(mfs)
	defer s.revokeRender(token)
	data, err := s.shooter.Screenshot(ctx, render.Shot{URL: s.renderBase + "/", Token: token, Width: size[0], Height: size[1]})
	if err != nil {
		s.log.Warn("reference screenshot failed", "err", err)
		return none
	}
	return ReferenceShot{Status: designref.ShotIncluded, Width: size[0], Height: size[1], PNG: data}
}

// grantRenderFiles lets the renderer read files from memory for a short time.
func (s *Service) grantRenderFiles(files fs.FS) string {
	token := rand.Text()
	s.grantsMu.Lock()
	defer s.grantsMu.Unlock()
	s.grants[token] = renderGrant{files: files, expires: s.now().Add(RenderTokenTTL)}
	return token
}
