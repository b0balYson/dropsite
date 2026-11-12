package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	goodCode  = "ABCD-EFGH-JKMNP"
	goodToken = "ds_good"
	userEmail = "dev@example.com"
)

// fakeServer implements the subset of API v1 the CLI uses, in memory.
type fakeServer struct {
	*httptest.Server
	mu       sync.Mutex
	sites    map[string]*fakeSite
	order    []string
	nextID   int
	requests []string
	// hooks
	deployErr  *apiErr // returned instead of accepting a deploy
	rateLimit  bool    // every request gets 429
	lastFiles  []string
	lastClient string
	lastPatch  map[string]any // body of the last PATCH /sites/{ref}
	patchErr   *apiErr        // returned instead of accepting a PATCH
	codeUsed   bool
	tokenName  string
}

type fakeSite struct {
	ID      string
	Title   string
	Slug    *string
	Deploys []fakeDeploy // oldest first
	Current int          // index into Deploys, -1 if none
	Owner   string       // owner_id; empty means the logged-in user "u1"
	Domains []map[string]any
}

type fakeDeploy struct {
	ID    string
	Files int
	At    time.Time
}

type apiErr struct {
	status  int
	code    string
	message string
	details map[string]any
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	f := &fakeServer{sites: map[string]*fakeSite{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/me", f.me)
	mux.HandleFunc("GET /api/v1/sites", f.listSites)
	mux.HandleFunc("POST /api/v1/sites", f.createSite)
	mux.HandleFunc("GET /api/v1/sites/{ref}", f.withSite(f.getSite))
	mux.HandleFunc("PATCH /api/v1/sites/{ref}", f.withSite(f.patchSite))
	mux.HandleFunc("DELETE /api/v1/sites/{ref}", f.withSite(f.deleteSite))
	mux.HandleFunc("POST /api/v1/sites/{ref}/deploys", f.withSite(f.deploy))
	mux.HandleFunc("GET /api/v1/sites/{ref}/deploys", f.withSite(f.listDeploys))
	mux.HandleFunc("POST /api/v1/sites/{ref}/rollback", f.withSite(f.rollback))
	mux.HandleFunc("GET /api/v1/sites/{ref}/hostnames", f.withSite(f.listHostnames))
	mux.HandleFunc("POST /api/v1/sites/{ref}/hostnames", f.withSite(f.addHostname))
	mux.HandleFunc("DELETE /api/v1/sites/{ref}/hostnames/{hostname}", f.withSite(f.removeHostname))
	f.Server = httptest.NewServer(f.auth(mux))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeServer) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		f.lastClient = r.Header.Get("X-Dropsite-Client")
		limited := f.rateLimit
		f.mu.Unlock()
		if limited {
			w.Header().Set("Retry-After", "42")
			fail(w, apiErr{429, "rate_limited", "too many requests", nil})
			return
		}
		if r.URL.Path == "/api/v1/connect/exchange" {
			f.exchange(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+goodToken {
			fail(w, apiErr{401, "unauthorized", "authentication required", nil})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (f *fakeServer) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *fakeServer) sawRequest(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.requests {
		if strings.HasPrefix(r, prefix) {
			return true
		}
	}
	return false
}

func (f *fakeServer) siteCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sites)
}

// addSite creates a site with n deploys directly.
func (f *fakeServer) addSite(title, slug string, deploys int) *fakeSite {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.newSiteLocked(title)
	if slug != "" {
		s.Slug = &slug
	}
	for range deploys {
		f.addDeployLocked(s, 1)
	}
	return s
}

func (f *fakeServer) newSiteLocked(title string) *fakeSite {
	f.nextID++
	s := &fakeSite{ID: fmt.Sprintf("site%02d", f.nextID), Title: title, Current: -1}
	f.sites[s.ID] = s
	f.order = append(f.order, s.ID)
	return s
}

func (f *fakeServer) addDeployLocked(s *fakeSite, files int) fakeDeploy {
	f.nextID++
	d := fakeDeploy{ID: fmt.Sprintf("dep%02d", f.nextID), Files: files, At: time.Date(2026, 5, 1, 10, f.nextID, 0, 0, time.UTC)}
	s.Deploys = append(s.Deploys, d)
	s.Current = len(s.Deploys) - 1
	return d
}

func (f *fakeServer) findLocked(ref string) *fakeSite {
	if s, ok := f.sites[ref]; ok {
		return s
	}
	for _, s := range f.sites {
		if s.Slug != nil && *s.Slug == ref {
			return s
		}
	}
	return nil
}

func (s *fakeSite) url() string {
	if s.Slug != nil {
		return "https://" + *s.Slug + ".sites.test"
	}
	return "https://" + s.ID + ".sites.test"
}

func (s *fakeSite) json() map[string]any {
	var cur any
	if s.Current >= 0 {
		cur = s.deployJSON(s.Current)
	}
	owner := s.Owner
	if owner == "" {
		owner = "u1"
	}
	return map[string]any{"id": s.ID, "title": s.Title, "url": s.url(), "urls": []string{s.url()},
		"slug": s.Slug, "owner_id": owner, "current_deploy": cur, "hostnames": s.hostnames()}
}

func (s *fakeSite) hostnames() []map[string]any {
	out := []map[string]any{{"hostname": s.ID + ".sites.test", "kind": "default", "verified": true, "status": "active"}}
	if s.Slug != nil {
		out = append(out, map[string]any{"hostname": *s.Slug + ".sites.test", "kind": "slug", "verified": true, "status": "active"})
	}
	return append(out, s.Domains...)
}

func (f *fakeServer) listHostnames(w http.ResponseWriter, _ *http.Request, s *fakeSite) {
	hs := s.hostnames()
	reply(w, 200, map[string]any{"items": hs, "total": len(hs)})
}

func (f *fakeServer) addHostname(w http.ResponseWriter, r *http.Request, s *fakeSite) {
	var in struct {
		Hostname string `json:"hostname"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	if !strings.Contains(in.Hostname, ".") {
		fail(w, apiErr{400, "domain_invalid", "domain must be a name such as promo.example.com", nil})
		return
	}
	h := map[string]any{"hostname": strings.ToLower(in.Hostname), "kind": "external", "status": "pending", "reason": nil}
	s.Domains = append(s.Domains, h)
	reply(w, 201, h)
}

func (f *fakeServer) removeHostname(w http.ResponseWriter, r *http.Request, s *fakeSite) {
	for i, d := range s.Domains {
		if d["hostname"] == r.PathValue("hostname") {
			s.Domains = append(s.Domains[:i], s.Domains[i+1:]...)
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	fail(w, apiErr{404, "not_found", "not found", nil})
}

func (s *fakeSite) deployJSON(i int) map[string]any {
	d := s.Deploys[i]
	return map[string]any{"id": d.ID, "site_id": s.ID, "source": "cli", "size_bytes": 2048,
		"file_count": d.Files, "status": "ready", "created_at": d.At, "current": i == s.Current}
}

func (f *fakeServer) withSite(h func(http.ResponseWriter, *http.Request, *fakeSite)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		s := f.findLocked(r.PathValue("ref"))
		if s == nil {
			fail(w, apiErr{404, "not_found", "not found", nil})
			return
		}
		h(w, r, s)
	}
}

func (f *fakeServer) me(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	n := len(f.sites)
	f.mu.Unlock()
	reply(w, 200, map[string]any{
		"user":       map[string]any{"id": "u1", "email": userEmail, "role": "user", "status": "active"},
		"limits":     map[string]any{"max_site_bytes": 1 << 20, "max_file_bytes": 1 << 19, "max_files": 100},
		"site_count": n,
	})
}

func (f *fakeServer) listSites(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := []any{}
	for _, id := range f.order {
		s, ok := f.sites[id]
		if !ok {
			continue
		}
		item := s.json()
		if s.Current >= 0 {
			item["last_deploy_at"] = s.Deploys[s.Current].At
		}
		items = append(items, item)
	}
	reply(w, 200, map[string]any{"items": items, "total": len(items)})
}

func (f *fakeServer) createSite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Title == "" {
		fail(w, apiErr{400, "title_invalid", "title is required", nil})
		return
	}
	f.mu.Lock()
	s := f.newSiteLocked(in.Title)
	out := s.json()
	f.mu.Unlock()
	reply(w, 201, out)
}

func (f *fakeServer) getSite(w http.ResponseWriter, _ *http.Request, s *fakeSite) {
	reply(w, 200, s.json())
}

// exchange trades goodCode (once) for goodToken.
func (f *fakeServer) exchange(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	f.mu.Lock()
	ok := in.Code == goodCode && !f.codeUsed
	f.codeUsed = f.codeUsed || ok
	f.tokenName = in.Name
	f.mu.Unlock()
	if !ok || r.Header.Get("Authorization") != "" {
		fail(w, apiErr{400, "connect_code_invalid", "the connect code is invalid, already used or expired", nil})
		return
	}
	reply(w, 201, map[string]any{"value": goodToken, "user": map[string]any{"id": "u1", "email": userEmail},
		"token": map[string]any{"id": "1", "name": in.Name}})
}

func (f *fakeServer) patchSite(w http.ResponseWriter, r *http.Request, s *fakeSite) {
	if f.patchErr != nil {
		fail(w, *f.patchErr)
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, apiErr{400, "invalid_input", "bad json", nil})
		return
	}
	f.lastPatch = body
	if v, ok := body["slug"].(string); ok {
		if v == "www" {
			fail(w, apiErr{400, "slug_reserved", "this address is reserved", map[string]any{"slug": "www"}})
			return
		}
		s.Slug = &v
	}
	out := s.json()
	out["expires_at"] = body["expires_at"]
	reply(w, 200, out)
}

func (f *fakeServer) deleteSite(w http.ResponseWriter, _ *http.Request, s *fakeSite) {
	delete(f.sites, s.ID)
	w.WriteHeader(http.StatusNoContent)
}

func (f *fakeServer) deploy(w http.ResponseWriter, r *http.Request, s *fakeSite) {
	if f.deployErr != nil {
		fail(w, *f.deployErr)
		return
	}
	files, err := readUpload(r)
	if err != nil {
		fail(w, apiErr{400, "archive_corrupt", err.Error(), nil})
		return
	}
	f.lastFiles = files
	d := f.addDeployLocked(s, len(files))
	reply(w, 201, map[string]any{"deploy_id": d.ID, "url": s.url(), "urls": []string{s.url()},
		"deploy": s.deployJSON(s.Current), "site": s.json()})
}

func readUpload(r *http.Request) ([]string, error) {
	file, hdr, err := r.FormFile("archive")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if hdr.Filename != "site.tar.gz" {
		return nil, fmt.Errorf("unexpected filename %q", hdr.Filename)
	}
	gz, err := gzip.NewReader(file)
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	var names []string
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg {
			names = append(names, h.Name)
		}
	}
	sort.Strings(names)
	return names, nil
}

func (f *fakeServer) listDeploys(w http.ResponseWriter, _ *http.Request, s *fakeSite) {
	items := []any{}
	for i := len(s.Deploys) - 1; i >= 0; i-- {
		items = append(items, s.deployJSON(i))
	}
	reply(w, 200, map[string]any{"items": items, "total": len(items)})
}

func (f *fakeServer) rollback(w http.ResponseWriter, r *http.Request, s *fakeSite) {
	var in struct {
		DeployID string `json:"deploy_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	target := -1
	for i, d := range s.Deploys {
		if (in.DeployID == "" && i == s.Current-1) || (in.DeployID != "" && d.ID == in.DeployID) {
			target = i
		}
	}
	if target < 0 {
		fail(w, apiErr{400, "no_previous_deploy", "there is no previous release to roll back to", nil})
		return
	}
	s.Current = target
	reply(w, 200, map[string]any{"deploy_id": s.Deploys[target].ID, "url": s.url(), "urls": []string{s.url()},
		"deploy": s.deployJSON(target), "site": s.json()})
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, e apiErr) {
	body := map[string]any{"code": e.code, "message": e.message}
	if e.details != nil {
		body["details"] = e.details
	}
	reply(w, e.status, map[string]any{"error": body})
}
