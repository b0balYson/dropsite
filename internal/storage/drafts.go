package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// Draft previews (change staged-publish):
//
//	<root>/sites/<site_id>/draft -> releases/<deploy_id> | ../../empty
//	<root>/by-host/<draft host>  -> ../sites/<site_id>/draft
//
// The host entry stays while the site has no draft and serves the empty
// directory (404), so the certificate Caddy issued for it stays usable and a
// new draft only repoints one link.

const draftLink = "draft"

func (f *FS) draftPath(siteID string) string {
	return filepath.Join(f.sitePath(siteID), draftLink)
}

// draftHostTarget is the by-host target of a draft preview, relative to
// by-host/.
func draftHostTarget(siteID string) string {
	return "../" + sitesDir + "/" + siteID + "/" + draftLink
}

// LinkDraft atomically points sites/<siteID>/draft at releases/<deployID>,
// which must exist, or at the shared empty directory when deployID is nil.
func (f *FS) LinkDraft(siteID string, deployID *string) error {
	if err := validateSiteID(siteID); err != nil {
		return err
	}
	if err := os.MkdirAll(f.sitePath(siteID), dirPerm); err != nil {
		return fmt.Errorf("storage: create site dir: %w", err)
	}
	if deployID == nil {
		return replaceSymlink(emptyTarget, f.draftPath(siteID))
	}
	if err := validateDeployID(*deployID); err != nil {
		return err
	}
	ok, err := isDir(f.releasePath(siteID, *deployID))
	if err != nil {
		return fmt.Errorf("storage: stat release: %w", err)
	}
	if !ok {
		return fmt.Errorf("%w: release %s/%s", ErrNotFound, siteID, *deployID)
	}
	return replaceSymlink(releaseTarget(*deployID), f.draftPath(siteID))
}

// LinkDraftHost atomically points by-host/<hostname> at sites/<siteID>/draft.
// The draft link is created (empty) first when missing, so the entry never
// dangles.
func (f *FS) LinkDraftHost(hostname, siteID string) error {
	if err := validateHostname(hostname); err != nil {
		return err
	}
	if err := validateSiteID(siteID); err != nil {
		return err
	}
	if _, err := os.Lstat(f.draftPath(siteID)); os.IsNotExist(err) {
		if err := f.LinkDraft(siteID, nil); err != nil {
			return err
		}
	} else if err != nil {
		return fmt.Errorf("storage: stat draft link: %w", err)
	}
	return replaceSymlink(draftHostTarget(siteID), f.hostPath(hostname))
}

// fixDraft makes sites/<id>/draft match the layout: pointing at the draft
// release (or the empty directory when there is none or its directory is
// missing) for a site with a draft host, absent otherwise.
func (f *FS) fixDraft(s SiteLayout, rep *ReconcileReport) error {
	link := f.draftPath(s.SiteID)
	if s.DraftHost == "" {
		if _, err := os.Lstat(link); os.IsNotExist(err) {
			return nil
		}
		if err := os.RemoveAll(link); err != nil {
			return fmt.Errorf("storage: remove draft link of %s: %w", s.SiteID, err)
		}
		rep.FixedCurrent = append(rep.FixedCurrent, s.SiteID+"/"+draftLink)
		return nil
	}
	target := emptyTarget
	if s.DraftDeployID != nil {
		ok, err := isDir(f.releasePath(s.SiteID, *s.DraftDeployID))
		if err != nil {
			return fmt.Errorf("storage: stat release: %w", err)
		}
		if ok {
			target = releaseTarget(*s.DraftDeployID)
		} else {
			rep.MissingPreviews = append(rep.MissingPreviews, s.DraftHost)
		}
	}
	changed, err := ensureSymlink(target, link)
	if err != nil {
		return fmt.Errorf("storage: fix draft of %s: %w", s.SiteID, err)
	}
	if changed {
		rep.FixedCurrent = append(rep.FixedCurrent, s.SiteID+"/"+draftLink)
	}
	return nil
}

// indexDraftHost adds a site's draft preview to hosts; it must not collide
// with any other entry. The draft release must be one of the ready ones.
func indexDraftHost(s SiteLayout, hosts map[string]hostLink) error {
	if s.DraftHost == "" {
		return nil
	}
	if err := validateHostname(s.DraftHost); err != nil {
		return err
	}
	if s.DraftDeployID != nil && !slices.Contains(s.ReadyDeployIDs, *s.DraftDeployID) {
		return fmt.Errorf("%w: draft %s of %s is not ready", ErrInvalidName, *s.DraftDeployID, s.SiteID)
	}
	if _, dup := hosts[s.DraftHost]; dup {
		return fmt.Errorf("%w: draft preview %s collides with another hostname", ErrInvalidName, s.DraftHost)
	}
	hosts[s.DraftHost] = hostLink{siteID: s.SiteID, draft: true}
	return nil
}
