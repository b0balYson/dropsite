package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// SiteLayout is the desired on-disk state of one site, taken from the database.
type SiteLayout struct {
	SiteID          string
	CurrentDeployID *string // nil: current points at the shared empty directory
	Hostnames       []string
	ReadyDeployIDs  []string
	Noindex         bool // every hostname carries the noindex marker
	Protected       bool // every hostname carries the protected marker
}

// Retained lists soft-deleted sites whose files must be kept until purge.
type Retained struct {
	SiteIDs []string
}

// ReconcileReport lists what Reconcile changed. All slices are sorted.
// Entries are site IDs, hostnames or paths relative to the storage root.
type ReconcileReport struct {
	RemovedTemp     []string // staging dirs and leftover temporary links
	RemovedReleases []string // "<site_id>/<entry>"
	RemovedSites    []string // entries of sites/ that were removed
	LinkedHosts     []string // by-host entries created or repointed
	UnlinkedHosts   []string // by-host entries removed
	FixedCurrent    []string // sites whose current link was created or repointed
	SetFlags        []string // "<flag>/<hostname>" markers created or repaired
	ClearedFlags    []string // "<flag>/<hostname>" markers removed
	// MissingCurrent lists sites whose CurrentDeployID has no release
	// directory on disk; their current points at the empty directory. Unlike
	// the other fields it is reported on every call while the mismatch with
	// the database persists, so a restart does not hide the emergency.
	MissingCurrent []string
}

// Empty reports whether the report contains no changes and no anomalies.
func (r ReconcileReport) Empty() bool {
	return len(r.RemovedTemp)+len(r.RemovedReleases)+len(r.RemovedSites)+
		len(r.LinkedHosts)+len(r.UnlinkedHosts)+len(r.FixedCurrent)+len(r.MissingCurrent)+
		len(r.SetFlags)+len(r.ClearedFlags) == 0
}

func (r *ReconcileReport) sort() {
	for _, s := range []*[]string{
		&r.RemovedTemp, &r.RemovedReleases, &r.RemovedSites, &r.LinkedHosts,
		&r.UnlinkedHosts, &r.FixedCurrent, &r.MissingCurrent, &r.SetFlags, &r.ClearedFlags,
	} {
		slices.Sort(*s)
	}
}

// Reconcile brings the disk in line with the database (desired). It removes
// staging directories and temporary links, releases unknown to desired,
// sites that are neither desired nor retained, and stray by-host entries; it
// repairs current and by-host links and the per-host flag markers. Invalid input is rejected before
// anything on disk is touched. A second call in a row changes nothing.
func (f *FS) Reconcile(ctx context.Context, desired []SiteLayout, keep Retained) (ReconcileReport, error) {
	var rep ReconcileReport
	want, hosts, err := indexDesired(desired)
	if err != nil {
		return rep, err
	}
	kept := make(map[string]bool, len(keep.SiteIDs))
	for _, id := range keep.SiteIDs {
		if err := validateSiteID(id); err != nil {
			return rep, err
		}
		kept[id] = true
	}
	if err := f.Init(ctx); err != nil {
		return rep, err
	}
	if err := f.reconcileSites(ctx, want, kept, &rep); err != nil {
		return rep, err
	}
	// Flags before links, so a newly linked host is never served unflagged.
	if err := f.reconcileFlags(ctx, desiredFlags(desired), &rep); err != nil {
		return rep, err
	}
	if err := f.reconcileHosts(ctx, hosts, &rep); err != nil {
		return rep, err
	}
	rep.sort()
	return rep, nil
}

// indexDesired validates desired and indexes it by site ID and by hostname.
func indexDesired(desired []SiteLayout) (map[string]SiteLayout, map[string]string, error) {
	want := make(map[string]SiteLayout, len(desired))
	hosts := map[string]string{}
	for _, s := range desired {
		if err := validateSiteID(s.SiteID); err != nil {
			return nil, nil, err
		}
		if _, dup := want[s.SiteID]; dup {
			return nil, nil, fmt.Errorf("%w: duplicate site %s", ErrInvalidName, s.SiteID)
		}
		if s.CurrentDeployID != nil {
			if err := validateDeployID(*s.CurrentDeployID); err != nil {
				return nil, nil, err
			}
		}
		for _, d := range s.ReadyDeployIDs {
			if err := validateDeployID(d); err != nil {
				return nil, nil, err
			}
		}
		for _, h := range s.Hostnames {
			if err := validateHostname(h); err != nil {
				return nil, nil, err
			}
			if other, dup := hosts[h]; dup && other != s.SiteID {
				return nil, nil, fmt.Errorf("%w: hostname %s claimed by %s and %s", ErrInvalidName, h, other, s.SiteID)
			}
			hosts[h] = s.SiteID
		}
		want[s.SiteID] = s
	}
	return want, hosts, nil
}

func (f *FS) reconcileSites(ctx context.Context, want map[string]SiteLayout, kept map[string]bool, rep *ReconcileReport) error {
	entries, err := os.ReadDir(f.sitesPath())
	if err != nil {
		return fmt.Errorf("storage: read sites: %w", err)
	}
	onDisk := map[string]bool{}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := e.Name()
		_, desired := want[name]
		if (!desired && !kept[name]) || !e.IsDir() || validateSiteID(name) != nil {
			if err := os.RemoveAll(filepath.Join(f.sitesPath(), name)); err != nil {
				return fmt.Errorf("storage: remove site %s: %w", name, err)
			}
			rep.RemovedSites = append(rep.RemovedSites, name)
			continue
		}
		onDisk[name] = true
		if err := f.removeTemp(name, rep); err != nil {
			return err
		}
	}
	for id, layout := range want {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Repoint current first so it never dangles while pruning.
		if err := f.fixCurrent(layout, rep); err != nil {
			return err
		}
		if onDisk[id] {
			if err := f.pruneReleases(layout, rep); err != nil {
				return err
			}
		}
	}
	return nil
}

// removeTemp removes leftover temporary links in sites/<id>/ and staging
// directories in sites/<id>/releases/.
func (f *FS) removeTemp(siteID string, rep *ReconcileReport) error {
	entries, err := os.ReadDir(f.sitePath(siteID))
	if err != nil {
		return fmt.Errorf("storage: read site %s: %w", siteID, err)
	}
	for _, e := range entries {
		if !isTempLink(e.Name()) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(f.sitePath(siteID), e.Name())); err != nil {
			return fmt.Errorf("storage: remove temp link: %w", err)
		}
		rep.RemovedTemp = append(rep.RemovedTemp, filepath.ToSlash(filepath.Join(sitesDir, siteID, e.Name())))
	}
	releases, err := os.ReadDir(f.releasesPath(siteID))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("storage: read releases of %s: %w", siteID, err)
	}
	for _, e := range releases {
		if !strings.HasSuffix(e.Name(), stagingExt) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(f.releasesPath(siteID), e.Name())); err != nil {
			return fmt.Errorf("storage: remove staging: %w", err)
		}
		rep.RemovedTemp = append(rep.RemovedTemp, filepath.ToSlash(filepath.Join(sitesDir, siteID, releasesDir, e.Name())))
	}
	return nil
}

// pruneReleases removes entries of releases/ that are neither ready nor current.
func (f *FS) pruneReleases(s SiteLayout, rep *ReconcileReport) error {
	entries, err := os.ReadDir(f.releasesPath(s.SiteID))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("storage: read releases of %s: %w", s.SiteID, err)
	}
	for _, e := range entries {
		name := e.Name()
		isCurrent := s.CurrentDeployID != nil && *s.CurrentDeployID == name
		if e.IsDir() && (isCurrent || slices.Contains(s.ReadyDeployIDs, name)) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(f.releasesPath(s.SiteID), name)); err != nil {
			return fmt.Errorf("storage: remove release: %w", err)
		}
		rep.RemovedReleases = append(rep.RemovedReleases, s.SiteID+"/"+name)
	}
	return nil
}

// fixCurrent points current at the desired release, or at the empty
// directory when there is none or its directory is missing.
func (f *FS) fixCurrent(s SiteLayout, rep *ReconcileReport) error {
	if err := os.MkdirAll(f.sitePath(s.SiteID), dirPerm); err != nil {
		return fmt.Errorf("storage: create site dir: %w", err)
	}
	target := emptyTarget
	if s.CurrentDeployID != nil {
		ok, err := isDir(f.releasePath(s.SiteID, *s.CurrentDeployID))
		if err != nil {
			return fmt.Errorf("storage: stat release: %w", err)
		}
		if ok {
			target = releaseTarget(*s.CurrentDeployID)
		} else {
			rep.MissingCurrent = append(rep.MissingCurrent, s.SiteID)
		}
	}
	changed, err := ensureSymlink(target, f.currentPath(s.SiteID))
	if err != nil {
		return fmt.Errorf("storage: fix current of %s: %w", s.SiteID, err)
	}
	if changed {
		rep.FixedCurrent = append(rep.FixedCurrent, s.SiteID)
	}
	return nil
}

func (f *FS) reconcileHosts(ctx context.Context, hosts map[string]string, rep *ReconcileReport) error {
	entries, err := os.ReadDir(f.byHostPath())
	if err != nil {
		return fmt.Errorf("storage: read by-host: %w", err)
	}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := e.Name()
		if _, ok := hosts[name]; ok {
			continue
		}
		if err := os.RemoveAll(f.hostPath(name)); err != nil {
			return fmt.Errorf("storage: remove host entry: %w", err)
		}
		if isTempLink(name) {
			rep.RemovedTemp = append(rep.RemovedTemp, byHostDir+"/"+name)
		} else {
			rep.UnlinkedHosts = append(rep.UnlinkedHosts, name)
		}
	}
	for host, siteID := range hosts {
		if err := ctx.Err(); err != nil {
			return err
		}
		changed, err := ensureSymlink(hostTarget(siteID), f.hostPath(host))
		if err != nil {
			return fmt.Errorf("storage: link host %s: %w", host, err)
		}
		if changed {
			rep.LinkedHosts = append(rep.LinkedHosts, host)
		}
	}
	return nil
}
