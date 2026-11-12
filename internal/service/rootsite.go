package service

import (
	"context"
	"strings"

	"github.com/b0balYson/dropsite/internal/domain"
)

// Root site management for AI assistants (MCP). Actor.IsAdmin is false for
// MCP actors, so an assistant never gets administrator powers over other
// users' sites. The one exception is the base domain root: an
// administrator's assistant may bind it to any site and clear it, with an
// explicit confirmation. canManageRoot is used by these functions only.

var errRootAdminOnly = domain.E(domain.KindInvalid, domain.CodeInvalidInput,
	"only an administrator of this server can change the root domain; tell the user to ask the administrator")

// canManageRoot reports whether the actor may bind the base domain root: an
// administrator, directly or through an assistant.
func canManageRoot(a Actor) bool { return a.User.IsAdmin() }

// confirms reports whether confirm names the site by ID or slug.
func (s *Service) confirms(ctx context.Context, confirm string, site domain.Site) (bool, error) {
	c := strings.ToLower(strings.TrimSpace(confirm))
	if c == "" {
		return false, nil
	}
	if c == site.ID {
		return true, nil
	}
	v, err := s.view(ctx, site)
	if err != nil {
		return false, err
	}
	return v.Slug != "" && c == v.Slug, nil
}

func confirmError(want string) error {
	return domain.E(domain.KindInvalid, domain.CodeInvalidInput,
		"not changed: ask the user to confirm, then repeat the call with confirm set to "+want)
}

// SetRootSite binds the base domain root to a site of any user. Only an
// administrator may do it; confirm must repeat the site's ID or slug.
func (s *Service) SetRootSite(ctx context.Context, a Actor, ref, confirm string) (SiteView, error) {
	if !canManageRoot(a) {
		return SiteView{}, errRootAdminOnly
	}
	site, err := s.findSite(ctx, ref)
	if err != nil {
		return SiteView{}, err
	}
	ok, err := s.confirms(ctx, confirm, site)
	if err != nil {
		return SiteView{}, err
	}
	if !ok {
		return SiteView{}, confirmError("the slug or ID of the site to put on the root domain")
	}
	return s.setApex(ctx, a, site)
}

// ClearRootSite unbinds the base domain root. Only an administrator may do
// it; confirm must repeat the ID or slug of the site currently on the root.
func (s *Service) ClearRootSite(ctx context.Context, a Actor, confirm string) (SiteView, error) {
	root, ok, err := s.RootSite(ctx, a)
	if err != nil {
		return SiteView{}, err
	}
	if !ok {
		return SiteView{}, domain.E(domain.KindInvalid, domain.CodeInvalidInput,
			"the root domain is not assigned to any site; nothing to clear")
	}
	if ok, err := s.confirms(ctx, confirm, root.Site); err != nil {
		return SiteView{}, err
	} else if !ok {
		return SiteView{}, confirmError("the slug or ID of the site now on the root domain: " + root.Site.ID)
	}
	return s.clearApex(ctx, a, root.Site)
}

// RootSite returns the site bound to the base domain root (administrators
// only, directly or through an assistant); ok is false when there is none.
func (s *Service) RootSite(ctx context.Context, a Actor) (SiteView, bool, error) {
	if !canManageRoot(a) {
		return SiteView{}, false, errRootAdminOnly
	}
	id, err := s.apexOwner(ctx)
	if err != nil || id == "" {
		return SiteView{}, false, err
	}
	site, err := s.repo.GetSite(ctx, id)
	if err != nil {
		return SiteView{}, false, err
	}
	v, err := s.view(ctx, site)
	return v, err == nil, err
}
