package mcp

import (
	"context"
	"errors"
	"fmt"
)

// Draft releases (change staged-publish).

// Draft is a site's draft release.
type Draft struct {
	DeployID   string `json:"deploy_id" jsonschema:"the draft release; use it as base_deploy_id to keep working on the draft"`
	CreatedAt  string `json:"created_at"`
	PreviewURL string `json:"preview_url,omitempty" jsonschema:"address that shows the draft (not indexed by search engines); give it to the user"`
}

// DraftBackend publishes and discards drafts and closes uploads into
// drafts. publish_draft and discard_draft fail when the backend does not
// implement it.
type DraftBackend interface {
	PublishDraft(ctx context.Context, ref string) (DeployResult, error)
	DiscardDraft(ctx context.Context, ref string) (Site, error)
	CommitUploadDraft(ctx context.Context, uploadID string, deletes []string) (FilesResult, error)
}

// DraftFilesDeployer publishes files passed by value as a draft (remote
// server, with deploy_files).
type DraftFilesDeployer interface {
	DeployFilesDraft(ctx context.Context, ref, title string, files []File) (DeployResult, error)
}

// ErrDiscardConfirm is returned by discard_draft when confirm does not name
// the site.
var ErrDiscardConfirm = errors.New("not deleted: ask the user to confirm deleting the draft, then call discard_draft " +
	"again with confirm set to the site's slug or ID exactly as shown by get_site")

var errNoDrafts = errors.New("this server cannot save drafts")

// draftAdvice is shared by the tools that publish.
const draftAdvice = " Pass draft true to save a draft instead of going live: do so by default when the site is " +
	"already live (it has visits, an own domain, or is older than three days), unless the user asked to publish " +
	"directly; for a brand-new site publish directly. A draft is checked like any publication, visitors keep " +
	"seeing the current version, and preview_url shows the draft: reply \"Here is how it will look: " +
	"<preview_url>. Publish?\" and call publish_draft after a yes."

// draftSummary words the outcome of a change saved as a draft.
func draftSummary(files int, siteURL, previewURL string) string {
	where := "the draft has no preview address on this server; describe the change instead"
	if previewURL != "" {
		where = "preview: " + previewURL
	}
	return fmt.Sprintf("; saved as a draft with %d files (%s). Visitors still see the current version at %s. "+
		"Ask the user whether to publish it (publish_draft) or delete it (discard_draft).", files, where, siteURL)
}
