package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

// Upload sessions (spec site-files).
const (
	MaxUploadSessions = 50
	UploadSessionTTL  = 24 * time.Hour
	uploadSessionsDir = "sessions"
	uploadIDPrefix    = "up_"
)

// Upload modes.
const (
	UploadReplace = "replace"
	UploadPatch   = "patch"
)

// UploadInfo describes an open upload session.
type UploadInfo struct {
	ID           string
	Mode         string
	SiteID       string
	Base         string
	Files        int
	Bytes        int64
	ExpiresAt    time.Time
	MaxBytes     int64
	MaxFiles     int
	MaxFileBytes int64
	MaxCallBytes int
}

type uploadSession struct {
	mu      sync.Mutex
	id      string
	userID  int64
	siteID  string
	title   string
	mode    string
	base    string
	dir     string
	lim     domain.Limits
	files   map[string]int64
	bytes   int64
	touched time.Time
	// closed is set once the session was committed or expired.
	closed bool
}

type uploadRegistry struct {
	mu       sync.Mutex
	sessions map[string]*uploadSession
}

func errUploadExpired() error {
	return domain.E(domain.KindInvalid, domain.CodeUploadExpired,
		"the upload session has expired or does not exist; start again with begin_upload")
}

func (s *Service) sessionsRoot() string { return filepath.Join(s.uploadDir, uploadSessionsDir) }

func (u *uploadSession) info() UploadInfo {
	return UploadInfo{
		ID: u.id, Mode: u.mode, SiteID: u.siteID, Base: u.base, Files: len(u.files), Bytes: u.bytes,
		ExpiresAt: u.touched.Add(UploadSessionTTL), MaxBytes: u.lim.MaxSiteBytes, MaxFiles: u.lim.MaxFiles,
		MaxFileBytes: u.lim.MaxFileBytes, MaxCallBytes: MaxInlineBytes,
	}
}

// BeginUpload opens an upload session. With mode replace the committed
// release holds only the uploaded files (ref empty: a new site titled title);
// with mode patch it is base + uploads − deletes, and ref and base are required.
func (s *Service) BeginUpload(ctx context.Context, a Actor, ref, title, mode, base string) (UploadInfo, error) {
	if mode == "" {
		mode = UploadReplace
	}
	if mode != UploadReplace && mode != UploadPatch {
		return UploadInfo{}, domain.E(domain.KindInvalid, domain.CodeInvalidInput, `mode must be "replace" or "patch"`)
	}
	sess := &uploadSession{userID: a.User.ID, mode: mode, title: strings.TrimSpace(title), files: map[string]int64{}}
	if ref == "" {
		if mode == UploadPatch {
			return UploadInfo{}, domain.E(domain.KindInvalid, domain.CodeInvalidInput, "patch mode needs site and base_deploy_id")
		}
		lim, err := s.LimitsFor(ctx, a.User.ID)
		if err != nil {
			return UploadInfo{}, err
		}
		sess.lim = lim
	} else {
		site, err := s.resolveSite(ctx, a, ref)
		if err != nil {
			return UploadInfo{}, err
		}
		if mode == UploadPatch {
			if err := requireBase(base); err != nil {
				return UploadInfo{}, err
			}
			if _, err := s.checkBase(ctx, site.ID, base); err != nil {
				return UploadInfo{}, err
			}
			sess.base = base
		}
		if sess.lim, err = s.siteOwnerLimits(ctx, site); err != nil {
			return UploadInfo{}, err
		}
		sess.siteID = site.ID
	}
	now := s.now()
	s.purgeUploads(now)
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return UploadInfo{}, fmt.Errorf("upload id: %w", err)
	}
	sess.id = uploadIDPrefix + hex.EncodeToString(raw[:])
	sess.dir = filepath.Join(s.sessionsRoot(), sess.id)
	sess.touched = now
	reg := &s.uploads
	reg.mu.Lock()
	defer reg.mu.Unlock()
	open := 0
	for _, u := range reg.sessions {
		if u.userID == a.User.ID {
			open++
		}
	}
	if open >= MaxUploadSessions {
		return UploadInfo{}, domain.E(domain.KindLimit, domain.CodeUploadLimit,
			"too many unfinished uploads; commit one with commit_upload or wait until they expire").
			WithParams(map[string]any{"limit": MaxUploadSessions, "minutes": int(UploadSessionTTL.Minutes())})
	}
	if err := os.MkdirAll(sess.dir, dirPerm); err != nil {
		return UploadInfo{}, fmt.Errorf("create upload session: %w", err)
	}
	reg.sessions[sess.id] = sess
	return sess.info(), nil
}

const dirPerm = 0o755

// session returns a live session of the actor; others are "expired".
func (s *Service) session(a Actor, id string, now time.Time) (*uploadSession, error) {
	reg := &s.uploads
	reg.mu.Lock()
	defer reg.mu.Unlock()
	u, ok := reg.sessions[id]
	if !ok || u.userID != a.User.ID {
		return nil, errUploadExpired()
	}
	if !u.mu.TryLock() {
		return u, nil // busy, so not idle
	}
	expired := now.Sub(u.touched) > UploadSessionTTL
	if expired {
		u.closed = true
	}
	u.mu.Unlock()
	if expired {
		delete(reg.sessions, id)
		s.dropSessionDir(u)
		return nil, errUploadExpired()
	}
	return u, nil
}

func (s *Service) dropSessionDir(u *uploadSession) {
	if err := os.RemoveAll(u.dir); err != nil {
		s.log.Warn("remove upload session failed", "dir", u.dir, "err", err)
	}
}

// UploadFiles adds files (inline or downloaded) to a session; a path sent
// again replaces the earlier file. Owner limits apply to the whole session.
func (s *Service) UploadFiles(ctx context.Context, a Actor, id string, files []InlineFile, fetch []FetchSpec) (UploadInfo, []FetchedFile, error) {
	u, err := s.session(a, id, s.now())
	if err != nil {
		return UploadInfo{}, nil, err
	}
	taken := map[string]bool{}
	overlay, err := decodePuts(files, taken)
	if err != nil {
		return UploadInfo{}, nil, err
	}
	reqs, err := checkFetchSpecs(fetch, taken)
	if err != nil {
		return UploadInfo{}, nil, err
	}
	if len(overlay)+len(reqs) == 0 {
		return UploadInfo{}, nil, domain.E(domain.KindInvalid, domain.CodeInvalidInput, "pass files or fetch entries")
	}
	got, err := s.fetchAssets(ctx, a, reqs, u.lim)
	if err != nil {
		return UploadInfo{}, nil, err
	}
	for _, f := range got {
		overlay[f.Path] = overlayFile{data: f.Data}
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.closed {
		return UploadInfo{}, nil, errUploadExpired()
	}
	if err := u.write(overlay); err != nil {
		return UploadInfo{}, nil, err
	}
	u.touched = s.now()
	return u.info(), fetchedReport(got), nil
}

// write stores files in the session directory within the owner limits.
// The caller holds u.mu.
func (u *uploadSession) write(overlay map[string]overlayFile) error {
	bytes, count := u.bytes, len(u.files)
	for p, f := range overlay {
		n := int64(len(f.data))
		if u.lim.MaxFileBytes > 0 && n > u.lim.MaxFileBytes {
			return domain.E(domain.KindTooLarge, domain.CodeArchiveFileTooBig, "file exceeds the size limit").
				WithParams(map[string]any{"limit": u.lim.MaxFileBytes, "path": p})
		}
		prev, had := u.files[p]
		bytes += n - prev
		if !had {
			count++
		}
	}
	if u.lim.MaxSiteBytes > 0 && bytes > u.lim.MaxSiteBytes {
		return domain.E(domain.KindTooLarge, domain.CodeArchiveTooLarge, "the uploaded files exceed the site size limit").
			WithParams(map[string]any{"limit": u.lim.MaxSiteBytes})
	}
	if u.lim.MaxFiles > 0 && count > u.lim.MaxFiles {
		return domain.E(domain.KindTooLarge, domain.CodeArchiveTooMany, "the upload has too many files").
			WithParams(map[string]any{"limit": u.lim.MaxFiles})
	}
	root, err := os.OpenRoot(u.dir)
	if err != nil {
		return fmt.Errorf("open upload session: %w", err)
	}
	defer func() { _ = root.Close() }()
	for p, f := range overlay {
		if dir := path.Dir(p); dir != "." {
			if err := root.MkdirAll(filepath.FromSlash(dir), dirPerm); err != nil {
				return invalidFile(p, "a file with the same name as this folder was uploaded earlier")
			}
		}
		if err := root.WriteFile(filepath.FromSlash(p), f.data, 0o644); err != nil {
			return invalidFile(p, "a folder with the same name as this file was uploaded earlier")
		}
		u.bytes += int64(len(f.data)) - u.files[p]
		u.files[p] = int64(len(f.data))
	}
	return nil
}

// CommitUpload publishes a session and closes it. A rate-limited commit
// keeps the session so it can be retried.
func (s *Service) CommitUpload(ctx context.Context, a Actor, id string, deletes []string) (UpdateResult, error) {
	return s.finishUpload(ctx, a, id, deletes, false)
}

// finishUpload closes a session into a new release: the current one, or with
// prepare a release the site is not switched to.
func (s *Service) finishUpload(ctx context.Context, a Actor, id string, deletes []string, prepare bool) (UpdateResult, error) {
	u, err := s.session(a, id, s.now())
	if err != nil {
		return UpdateResult{}, err
	}
	reg := &s.uploads
	reg.mu.Lock()
	if reg.sessions[id] != u {
		reg.mu.Unlock()
		return UpdateResult{}, errUploadExpired()
	}
	delete(reg.sessions, id)
	reg.mu.Unlock()
	u.mu.Lock()
	if u.closed {
		u.mu.Unlock()
		return UpdateResult{}, errUploadExpired()
	}
	res, err := s.commitSession(ctx, a, u, deletes, prepare)
	de := domain.AsError(err)
	retry := err != nil && (de.Kind == domain.KindRateLimited || de.Kind == domain.KindInternal)
	if retry {
		u.touched = s.now()
	} else {
		u.closed = true
	}
	u.mu.Unlock()
	if retry {
		reg.mu.Lock()
		reg.sessions[id] = u
		reg.mu.Unlock()
		return UpdateResult{}, err
	}
	s.dropSessionDir(u)
	return res, err
}

func (s *Service) commitSession(ctx context.Context, a Actor, u *uploadSession, deletes []string, prepare bool) (UpdateResult, error) {
	if u.mode == UploadReplace && len(deletes) > 0 {
		return UpdateResult{}, domain.E(domain.KindInvalid, domain.CodeInvalidInput,
			"delete is only for patch uploads: a replace upload publishes exactly the uploaded files")
	}
	if len(u.files) == 0 && len(deletes) == 0 {
		return UpdateResult{}, domain.E(domain.KindInvalid, domain.CodeInvalidInput, "nothing was uploaded")
	}
	taken := map[string]bool{}
	for p := range u.files {
		taken[p] = true
	}
	dels, err := checkDeletes(deletes, taken)
	if err != nil {
		return UpdateResult{}, err
	}
	if ok, retry := s.deployLimit.Allow(userKey(a.User.ID)); !ok {
		return UpdateResult{}, domain.ErrRateLimited.WithRetryAfter(retry).
			WithParams(map[string]any{"limit": DeploysPerHour})
	}
	root, err := os.OpenRoot(u.dir)
	if err != nil {
		return UpdateResult{}, fmt.Errorf("open upload session: %w", err)
	}
	defer func() { _ = root.Close() }()
	overlay := make(map[string]overlayFile, len(u.files))
	for p := range u.files {
		overlay[p] = overlayFile{root: root, name: filepath.FromSlash(p)}
	}
	siteID := u.siteID
	if siteID == "" {
		title := u.title
		if title == "" {
			title = "Untitled site"
		}
		v, err := s.CreateSite(ctx, a, title, "")
		if err != nil {
			return UpdateResult{}, err
		}
		siteID = v.Site.ID
	} else if _, err := s.resolveSite(ctx, a, siteID); err != nil {
		return UpdateResult{}, err
	}
	res, err := s.publishEditAs(ctx, a, siteID, u.base, dels, overlay, u.lim, "mcp_upload", prepare)
	if err != nil {
		return UpdateResult{}, err
	}
	res.Written = len(overlay)
	return res, nil
}

// purgeUploads closes sessions idle for longer than UploadSessionTTL.
func (s *Service) purgeUploads(now time.Time) {
	reg := &s.uploads
	reg.mu.Lock()
	var gone []*uploadSession
	for id, u := range reg.sessions {
		if u.mu.TryLock() {
			if now.Sub(u.touched) > UploadSessionTTL {
				u.closed = true
				delete(reg.sessions, id)
				gone = append(gone, u)
			}
			u.mu.Unlock()
		}
	}
	reg.mu.Unlock()
	for _, u := range gone {
		s.dropSessionDir(u)
	}
}

// cleanUploadSessions removes every session directory (start-up: sessions
// do not survive a restart).
func (s *Service) cleanUploadSessions() error {
	if err := os.RemoveAll(s.sessionsRoot()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove upload sessions: %w", err)
	}
	return nil
}
