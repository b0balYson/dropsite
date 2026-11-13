package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/b0balYson/dropsite/internal/archive"
	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/ratelimit"
	"github.com/b0balYson/dropsite/internal/sitefetch"
)

// FetchedURLsPerHour caps server-side downloads per user.
const FetchedURLsPerHour = 100_000

// SiteFetcher downloads assets for a site; *sitefetch.Client implements it.
type SiteFetcher interface {
	Fetch(ctx context.Context, reqs []sitefetch.Request, maxFile int64) ([]sitefetch.File, error)
}

// FetchSpec asks the server to download URL into the site at Path.
type FetchSpec struct {
	URL  string
	Path string
}

// FetchedFile reports one downloaded asset.
type FetchedFile struct {
	URL           string
	Path          string
	Type          string
	Bytes         int64
	OriginalBytes int64
	Resized       bool
}

// siteFilesState is the state of site file editing: the asset fetcher, its
// rate limit and the open upload sessions.
type siteFilesState struct {
	fetcher    SiteFetcher
	fetchLimit *ratelimit.Window
	uploads    uploadRegistry
	// Upload link limits: per link, per client network, failures per network.
	linkLimit     *ratelimit.Window
	linkIPLimit   *ratelimit.Window
	linkFailLimit *ratelimit.Window
}

func newSiteFilesState(d Deps, now func() time.Time) siteFilesState {
	f := d.Fetcher
	if f == nil {
		f = sitefetch.New(sitefetch.Options{})
	}
	return siteFilesState{
		fetcher:    f,
		fetchLimit: ratelimit.New(FetchedURLsPerHour, time.Hour).WithClock(now),
		uploads:    uploadRegistry{sessions: map[string]*uploadSession{}, links: map[[32]byte]string{}},

		linkLimit:     ratelimit.New(UploadLinkRequestsPerHour, time.Hour).WithClock(now),
		linkIPLimit:   ratelimit.New(UploadLinkRequestsPerIPHour, time.Hour).WithClock(now),
		linkFailLimit: ratelimit.New(UploadLinkFailuresPerIP, FailedLoginsWindow).WithClock(now),
	}
}

// checkFetchSpecs validates fetch entries before anything is downloaded.
func checkFetchSpecs(specs []FetchSpec, taken map[string]bool) ([]sitefetch.Request, error) {
	if len(specs) > sitefetch.MaxURLs {
		return nil, domain.E(domain.KindInvalid, domain.CodeInvalidInput,
			fmt.Sprintf("at most %d fetch entries per call", sitefetch.MaxURLs))
	}
	out := make([]sitefetch.Request, 0, len(specs))
	for _, f := range specs {
		p, err := checkWritePath(f.Path)
		if err != nil {
			return nil, err
		}
		if taken[p] {
			return nil, invalidFile(p, "the path is listed twice")
		}
		taken[p] = true
		if !sitefetch.AllowedPath(p) {
			return nil, invalidFile(p, "a downloaded file must be an image, font, .css or .js file")
		}
		if err := sitefetch.ValidateURL(f.URL); err != nil {
			return nil, fetchErr(f.URL, err.Error())
		}
		out = append(out, sitefetch.Request{URL: f.URL, Path: p})
	}
	return out, nil
}

func fetchErr(url, reason string) error {
	return domain.E(domain.KindInvalid, domain.CodeFetchFailed,
		fmt.Sprintf("could not download %s: %s; nothing was changed", url, reason)).
		WithParams(map[string]any{"url": url, "reason": reason})
}

// fetchAssets downloads reqs on behalf of a, within the per-user rate limit.
func (s *Service) fetchAssets(ctx context.Context, a Actor, reqs []sitefetch.Request, lim domain.Limits) ([]sitefetch.File, error) {
	if len(reqs) == 0 {
		return nil, nil
	}
	for range reqs {
		if ok, retry := s.fetchLimit.Allow(userKey(a.User.ID)); !ok {
			return nil, domain.ErrRateLimited.WithRetryAfter(retry).
				WithParams(map[string]any{"limit": FetchedURLsPerHour})
		}
	}
	files, err := s.fetcher.Fetch(ctx, reqs, lim.MaxFileBytes)
	var fe *sitefetch.Error
	if errors.As(err, &fe) {
		return nil, fetchErr(fe.URL, fe.Reason)
	}
	if err != nil {
		return nil, fmt.Errorf("fetch assets: %w", err)
	}
	return files, nil
}

func fetchedReport(files []sitefetch.File) []FetchedFile {
	out := make([]FetchedFile, 0, len(files))
	for _, f := range files {
		out = append(out, FetchedFile{
			URL: f.URL, Path: f.Path, Type: f.ContentType, Bytes: int64(len(f.Data)),
			OriginalBytes: f.OriginalBytes, Resized: f.Resized,
		})
	}
	return out
}

// checkWritePath validates a path an assistant writes: a clean relative path
// that is neither a service file the pipeline drops nor a secret-looking file.
func checkWritePath(raw string) (string, error) {
	p, err := cleanInlinePath(raw)
	if err != nil {
		return "", err
	}
	if archive.IsIgnored(p) {
		return "", invalidFile(p, "service files such as .git/, node_modules/ or .DS_Store are never published")
	}
	if archive.IsSensitive(p) {
		return "", invalidFile(p, "this looks like a secret (.env, key or credentials file) and is never published from chat")
	}
	return p, nil
}
