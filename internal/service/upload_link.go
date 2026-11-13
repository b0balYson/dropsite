package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

// Upload links (spec upload-link). begin_upload returns a capability URL
// https://<API_HOST>/u/<token> through which the files of that one upload
// session arrive by plain HTTP: a tar stream from a terminal or a page that
// takes files by drag and drop. The token grants nothing else: no account
// access, no reading, no commit (commit_upload still needs the assistant's
// own authorization), so MCP still never hands out credentials.
const (
	// UploadLinkPath is where links live on the API host.
	UploadLinkPath = "/u/"
	// UploadLinkTTL is how long an unused link works; every use extends it,
	// never beyond the session itself.
	UploadLinkTTL = time.Hour
	// UploadLinkRequestsPerHour caps requests per link (at least twice the
	// owner's file limit, so a page sending one file per request fits).
	UploadLinkRequestsPerHour = 3000
	// UploadLinkRequestsPerIPHour caps link requests per client network.
	UploadLinkRequestsPerIPHour = 6000
	// UploadLinkFailuresPerIP caps unknown or expired tokens per client
	// network within FailedLoginsWindow, against guessing.
	UploadLinkFailuresPerIP = 30

	uploadLinkBytes    = 32 // 256 bits
	uploadLinkTokenLen = 43 // unpadded base64url of uploadLinkBytes
	stagingPrefix      = ".incoming-"
)

// LinkInfo is what a link holder may see about its upload: counts and
// limits, nothing about the account or the site.
type LinkInfo struct {
	Files        int
	Bytes        int64
	MaxBytes     int64
	MaxFiles     int
	MaxFileBytes int64
	ExpiresAt    time.Time
}

// SkippedFile is a file of an archive that was not taken.
type SkippedFile struct {
	Path   string
	Reason string
}

// LinkReceipt reports what one link request stored.
type LinkReceipt struct {
	Files   int
	Bytes   int64
	Skipped []SkippedFile
	Upload  LinkInfo
}

func errUploadLink() error {
	return domain.E(domain.KindNotFound, domain.CodeUploadLinkInvalid,
		"this upload link has expired, was already used for a published upload, or does not exist; "+
			"ask the assistant for a new link")
}

// issueLink creates the session's link token and keeps only its hash.
func (u *uploadSession) issueLink(now time.Time) (string, error) {
	var raw [uploadLinkBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("upload link: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	u.link = sha256.Sum256([]byte(token))
	u.linkUsed = now
	return token, nil
}

// linkExpires is when the link stops working: an hour after its last use,
// and never after the session itself expires. The caller holds u.mu or
// owns u exclusively.
func (u *uploadSession) linkExpires() time.Time {
	end := u.linkUsed.Add(UploadLinkTTL)
	if s := u.touched.Add(UploadSessionTTL); s.Before(end) {
		return s
	}
	return end
}

func (u *uploadSession) linkInfo() LinkInfo {
	return LinkInfo{
		Files: len(u.files), Bytes: u.bytes, MaxBytes: u.lim.MaxSiteBytes, MaxFiles: u.lim.MaxFiles,
		MaxFileBytes: u.lim.MaxFileBytes, ExpiresAt: u.linkExpires(),
	}
}

func (s *Service) uploadLinkURL(token string) string {
	return s.APIURL() + UploadLinkPath + token
}

// tokenShape reports whether token can be a link token at all.
func tokenShape(token string) bool {
	if len(token) != uploadLinkTokenLen {
		return false
	}
	for i := 0; i < len(token); i++ {
		c := token[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return false
		}
	}
	return true
}

// linkSession resolves a link to its live session. Lookups are limited per
// client network, failures more strictly, and requests per link.
func (s *Service) linkSession(ctx context.Context, token, ip string) (*uploadSession, error) {
	key := ipKey(ip)
	if ok, retry := s.linkIPLimit.Allow(key); !ok {
		return nil, domain.ErrRateLimited.WithRetryAfter(retry)
	}
	res, ok, retry := s.linkFailLimit.Reserve(key)
	if !ok {
		return nil, domain.ErrRateLimited.WithRetryAfter(retry)
	}
	u := s.lookupLink(token)
	if u == nil {
		return nil, errUploadLink()
	}
	owner, err := s.repo.GetUser(ctx, u.userID)
	if err != nil || owner.Status != domain.UserActive {
		return nil, errUploadLink()
	}
	res.Cancel()
	limit := max(UploadLinkRequestsPerHour, 2*u.lim.MaxFiles)
	if ok, retry := s.linkLimit.AllowMax("link:"+u.id, limit); !ok {
		return nil, domain.ErrRateLimited.WithRetryAfter(retry).WithParams(map[string]any{"limit": limit})
	}
	return u, nil
}

// lookupLink returns the open session of a live link, or nil.
func (s *Service) lookupLink(token string) *uploadSession {
	if !tokenShape(token) {
		return nil
	}
	h := sha256.Sum256([]byte(token))
	reg := &s.uploads
	reg.mu.Lock()
	u := reg.sessions[reg.links[h]]
	reg.mu.Unlock()
	if u == nil {
		return nil
	}
	now := s.now()
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.closed || u.link != h || !now.Before(u.linkExpires()) {
		return nil
	}
	return u
}

// UploadLinkInfo describes a live link for its upload page. It does not
// extend the link.
func (s *Service) UploadLinkInfo(ctx context.Context, token, ip string) (LinkInfo, error) {
	u, err := s.linkSession(ctx, token, ip)
	if err != nil {
		return LinkInfo{}, err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.linkInfo(), nil
}

// staged is a file received into a staging directory, not yet in the session.
type staged struct {
	name string // file name inside the staging directory
	size int64
}

// stage is a staging directory for one link request. Bodies are received
// there without holding the session lock, then moved in at once.
type stage struct {
	dir   string
	files map[string]staged
	order []string
	n     int
}

func (s *Service) newStage() (*stage, error) {
	if err := os.MkdirAll(s.sessionsRoot(), dirPerm); err != nil {
		return nil, fmt.Errorf("create upload staging: %w", err)
	}
	dir, err := os.MkdirTemp(s.sessionsRoot(), stagingPrefix)
	if err != nil {
		return nil, fmt.Errorf("create upload staging: %w", err)
	}
	return &stage{dir: dir, files: map[string]staged{}}, nil
}

func (st *stage) close(s *Service) {
	if err := os.RemoveAll(st.dir); err != nil {
		s.log.Warn("remove upload staging failed", "err", err)
	}
}

// receive copies r (at most maxFile bytes) into the stage as path p.
func (st *stage) receive(p string, r io.Reader, maxFile int64) error {
	st.n++
	name := strconv.Itoa(st.n)
	f, err := os.OpenFile(filepath.Join(st.dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644) //nolint:gosec // G304: a counter inside our own staging directory
	if err != nil {
		return fmt.Errorf("stage upload: %w", err)
	}
	src := r
	if maxFile > 0 {
		src = io.LimitReader(r, maxFile+1)
	}
	n, err := io.Copy(f, src)
	if cerr := f.Close(); err == nil && cerr != nil {
		err = fmt.Errorf("stage upload: %w", cerr)
	}
	if err != nil {
		return bodyError(err)
	}
	if maxFile > 0 && n > maxFile {
		return domain.E(domain.KindTooLarge, domain.CodeArchiveFileTooBig, "file exceeds the size limit").
			WithParams(map[string]any{"limit": maxFile, "path": p})
	}
	if _, dup := st.files[p]; !dup {
		st.order = append(st.order, p)
	}
	st.files[p] = staged{name: name, size: n}
	return nil
}

// bodyError turns a failed read of the request body into an error the
// client can act on.
func bodyError(err error) error {
	var de *domain.Error
	if errors.As(err, &de) {
		return de
	}
	var mb *http.MaxBytesError
	if errors.As(err, &mb) {
		return domain.E(domain.KindTooLarge, domain.CodeArchiveTooLarge, "the request body is larger than the upload allows").
			WithParams(map[string]any{"limit": mb.Limit})
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return domain.E(domain.KindInvalid, domain.CodeInvalidInput, "the upload was interrupted; send it again")
	}
	return fmt.Errorf("receive upload: %w", err)
}

// place moves the staged files into the session within the owner limits.
func (s *Service) place(u *uploadSession, st *stage) (LinkReceipt, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.closed {
		return LinkReceipt{}, errUploadLink()
	}
	sizes := make(map[string]int64, len(st.files))
	var total int64
	for p, f := range st.files {
		sizes[p] = f.size
		total += f.size
	}
	if err := u.admit(sizes); err != nil {
		return LinkReceipt{}, err
	}
	root, err := os.OpenRoot(u.dir)
	if err != nil {
		return LinkReceipt{}, fmt.Errorf("open upload session: %w", err)
	}
	defer func() { _ = root.Close() }()
	for _, p := range st.order {
		f := st.files[p]
		if err := makeParent(root, p); err != nil {
			return LinkReceipt{}, err
		}
		if err := os.Rename(filepath.Join(st.dir, f.name), filepath.Join(u.dir, filepath.FromSlash(p))); err != nil {
			return LinkReceipt{}, invalidFile(p, "a folder with the same name as this file was uploaded earlier")
		}
		u.record(p, f.size)
	}
	now := s.now()
	u.touched, u.linkUsed = now, now
	return LinkReceipt{Files: len(st.files), Bytes: total, Upload: u.linkInfo()}, nil
}

// PutLinkFile stores one file sent to an upload link; a path sent again
// replaces the file. The path rules are those of upload_files.
func (s *Service) PutLinkFile(ctx context.Context, token, ip, rawPath string, body io.Reader) (LinkReceipt, error) {
	u, err := s.linkSession(ctx, token, ip)
	if err != nil {
		return LinkReceipt{}, err
	}
	p, err := checkWritePath(rawPath)
	if err != nil {
		return LinkReceipt{}, err
	}
	st, err := s.newStage()
	if err != nil {
		return LinkReceipt{}, err
	}
	defer st.close(s)
	if err := st.receive(p, body, u.lim.MaxFileBytes); err != nil {
		return LinkReceipt{}, err
	}
	return s.place(u, st)
}
