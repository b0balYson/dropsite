package domain

import (
	"regexp"
	"time"
)

// Draft releases (change staged-publish).

// CodeNoDraft is returned when a site has no draft to publish or discard.
const CodeNoDraft = "no_draft"

// ErrNoDraft is the error of publishing or discarding a missing draft.
var ErrNoDraft = E(KindInvalid, CodeNoDraft, "this site has no draft; publish one with draft set first")

// draftPreviewPattern is the form of a site's draft preview label
// (DraftPreviewLabel). It is mirrored by the @preview matcher in
// deploy/Caddyfile.
var draftPreviewPattern = regexp.MustCompile(`^p-[a-z0-9]{10}$`)

// DraftPreviewLabel returns the host label of a site's stable draft preview:
// "p-<site id>". It never equals a site ID (no hyphen there) nor a release
// preview label (PreviewLabel, longer).
func DraftPreviewLabel(siteID string) string { return "p-" + siteID }

// IsDraftPreviewLabel reports whether label has the form of a draft preview
// label. Such labels are reserved and never accepted as slugs.
func IsDraftPreviewLabel(label string) bool { return draftPreviewPattern.MatchString(label) }

// SiteDraft is the draft state of a site. A row exists once the site had a
// draft (its preview address is allocated); DeployID is nil when there is no
// draft now.
type SiteDraft struct {
	SiteID    string
	DeployID  *string
	CreatedBy *int64
	CreatedAt time.Time
	UpdatedAt time.Time
}
