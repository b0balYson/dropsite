package api

import (
	"encoding/base64"
	"net/http"
	"time"

	"github.com/b0balYson/dropsite/internal/service"
)

// Reading and editing the files of published sites, and upload sessions
// (spec site-files). The same service methods back the MCP tools.

type siteFileDTO struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Type   string `json:"type"`
}

type siteFileListDTO struct {
	SiteID     string        `json:"site_id"`
	URL        string        `json:"url"`
	DeployID   string        `json:"deploy_id"`
	Current    bool          `json:"current"`
	Files      []siteFileDTO `json:"files"`
	TotalFiles int           `json:"total_files"`
	TotalBytes int64         `json:"total_bytes"`
	Truncated  bool          `json:"truncated"`
}

type siteFileContentDTO struct {
	siteFileDTO
	// Encoding is utf8 or base64; empty (and Content empty) when the
	// content is not included, Note then says why.
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
	Note     string `json:"note"`
}

type siteFileReadDTO struct {
	SiteID    string               `json:"site_id"`
	DeployID  string               `json:"deploy_id"`
	Current   bool                 `json:"current"`
	Files     []siteFileContentDTO `json:"files"`
	Missing   []string             `json:"missing"`
	Truncated bool                 `json:"truncated"`
}

type inlineFileDTO struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
}

type fetchSpecDTO struct {
	URL  string `json:"url"`
	Path string `json:"path"`
}

type fetchedFileDTO struct {
	URL           string `json:"url"`
	Path          string `json:"path"`
	Type          string `json:"type"`
	Bytes         int64  `json:"bytes"`
	OriginalBytes int64  `json:"original_bytes"`
	Resized       bool   `json:"resized"`
}

type filesResultDTO struct {
	deployResultDTO
	BaseDeployID string           `json:"base_deploy_id"`
	Written      int              `json:"written"`
	Deleted      int              `json:"deleted"`
	Fetched      []fetchedFileDTO `json:"fetched"`
}

type uploadDTO struct {
	ID           string           `json:"id"`
	Mode         string           `json:"mode"`
	SiteID       string           `json:"site_id"`
	BaseDeployID string           `json:"base_deploy_id"`
	Files        int              `json:"files"`
	Bytes        int64            `json:"bytes"`
	ExpiresAt    time.Time        `json:"expires_at"`
	MaxBytes     int64            `json:"max_bytes"`
	MaxFiles     int              `json:"max_files"`
	MaxFileBytes int64            `json:"max_file_bytes"`
	MaxCallBytes int              `json:"max_call_bytes"`
	Fetched      []fetchedFileDTO `json:"fetched"`
}

func toSiteFile(f service.SiteFile) siteFileDTO {
	return siteFileDTO{Path: f.Path, Size: f.Size, SHA256: f.SHA256, Type: f.Type}
}

func toInline(in []inlineFileDTO) []service.InlineFile {
	out := make([]service.InlineFile, 0, len(in))
	for _, f := range in {
		out = append(out, service.InlineFile{Path: f.Path, Content: f.Content, Encoding: f.Encoding})
	}
	return out
}

func toFetchSpecs(in []fetchSpecDTO) []service.FetchSpec {
	out := make([]service.FetchSpec, 0, len(in))
	for _, f := range in {
		out = append(out, service.FetchSpec{URL: f.URL, Path: f.Path})
	}
	return out
}

func toFetched(in []service.FetchedFile) []fetchedFileDTO {
	out := make([]fetchedFileDTO, 0, len(in))
	for _, f := range in {
		out = append(out, fetchedFileDTO{URL: f.URL, Path: f.Path, Type: f.Type, Bytes: f.Bytes,
			OriginalBytes: f.OriginalBytes, Resized: f.Resized})
	}
	return out
}

func toFilesResult(r service.UpdateResult) filesResultDTO {
	return filesResultDTO{deployResultDTO: toDeployResult(r.DeployResult), BaseDeployID: r.Base,
		Written: r.Written, Deleted: r.Deleted, Fetched: toFetched(r.Fetched)}
}

func toUpload(i service.UploadInfo, fetched []service.FetchedFile) uploadDTO {
	return uploadDTO{
		ID: i.ID, Mode: i.Mode, SiteID: i.SiteID, BaseDeployID: i.Base, Files: i.Files, Bytes: i.Bytes,
		ExpiresAt: utc(i.ExpiresAt), MaxBytes: i.MaxBytes, MaxFiles: i.MaxFiles, MaxFileBytes: i.MaxFileBytes,
		MaxCallBytes: i.MaxCallBytes, Fetched: toFetched(fetched),
	}
}

func (a *api) listSiteFiles(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	l, err := a.svc.ListSiteFiles(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), q.Get("deploy_id"), q.Get("prefix"))
	if err != nil {
		return err
	}
	out := siteFileListDTO{
		SiteID: l.Site.Site.ID, URL: l.Site.URL, DeployID: l.Deploy.ID, Current: l.Current,
		Files: make([]siteFileDTO, 0, len(l.Files)), TotalFiles: l.TotalFiles, TotalBytes: l.TotalBytes, Truncated: l.Truncated,
	}
	for _, f := range l.Files {
		out.Files = append(out.Files, toSiteFile(f))
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

type readSiteFilesRequest struct {
	DeployID string   `json:"deploy_id"`
	Paths    []string `json:"paths"`
}

func (a *api) readSiteFiles(w http.ResponseWriter, r *http.Request) error {
	var req readSiteFilesRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	res, err := a.svc.ReadSiteFiles(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), req.DeployID, req.Paths)
	if err != nil {
		return err
	}
	out := siteFileReadDTO{
		SiteID: res.Site.Site.ID, DeployID: res.Deploy.ID, Current: res.Current, Missing: nonNil(res.Missing),
		Truncated: res.Truncated, Files: make([]siteFileContentDTO, 0, len(res.Files)),
	}
	for _, f := range res.Files {
		c := siteFileContentDTO{siteFileDTO: toSiteFile(f.SiteFile), Note: f.Note}
		switch {
		case f.Content == nil:
		case f.Text:
			c.Encoding, c.Content = "utf8", string(f.Content)
		default:
			c.Encoding, c.Content = "base64", base64.StdEncoding.EncodeToString(f.Content)
		}
		out.Files = append(out.Files, c)
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

type updateSiteFilesRequest struct {
	BaseDeployID string          `json:"base_deploy_id"`
	Files        []inlineFileDTO `json:"files"`
	Delete       []string        `json:"delete"`
	Fetch        []fetchSpecDTO  `json:"fetch"`
	Draft        bool            `json:"draft"`
}

func (a *api) updateSiteFiles(w http.ResponseWriter, r *http.Request) error {
	var req updateSiteFilesRequest
	if err := decodeFilesJSON(w, r, &req); err != nil {
		return err
	}
	res, err := a.svc.UpdateSiteFilesWith(r.Context(), actorFrom(r.Context()), r.PathValue("ref"), req.BaseDeployID,
		service.FileChanges{Put: toInline(req.Files), Delete: req.Delete, Fetch: toFetchSpecs(req.Fetch)},
		service.PublishOptions{Draft: req.Draft})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toFilesResult(res))
	return nil
}

type beginUploadRequest struct {
	Site         string `json:"site"`
	Title        string `json:"title"`
	Mode         string `json:"mode"`
	BaseDeployID string `json:"base_deploy_id"`
}

func (a *api) beginUpload(w http.ResponseWriter, r *http.Request) error {
	var req beginUploadRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	i, err := a.svc.BeginUpload(r.Context(), actorFrom(r.Context()), req.Site, req.Title, req.Mode, req.BaseDeployID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, toUpload(i, nil))
	return nil
}

type uploadFilesRequest struct {
	Files []inlineFileDTO `json:"files"`
	Fetch []fetchSpecDTO  `json:"fetch"`
}

func (a *api) uploadFiles(w http.ResponseWriter, r *http.Request) error {
	var req uploadFilesRequest
	if err := decodeFilesJSON(w, r, &req); err != nil {
		return err
	}
	i, fetched, err := a.svc.UploadFiles(r.Context(), actorFrom(r.Context()), r.PathValue("id"),
		toInline(req.Files), toFetchSpecs(req.Fetch))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toUpload(i, fetched))
	return nil
}

type commitUploadRequest struct {
	Delete []string `json:"delete"`
	Draft  bool     `json:"draft"`
}

func (a *api) commitUpload(w http.ResponseWriter, r *http.Request) error {
	var req commitUploadRequest
	if err := decodeOptionalJSON(w, r, &req); err != nil {
		return err
	}
	res, err := a.svc.CommitUploadWith(r.Context(), actorFrom(r.Context()), r.PathValue("id"), req.Delete,
		service.PublishOptions{Draft: req.Draft})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, toFilesResult(res))
	return nil
}
