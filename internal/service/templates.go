package service

import (
	"context"
	"errors"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/templates"
)

// TemplateInfo describes a built-in starter template.
type TemplateInfo = templates.Info

func errTemplateNotFound(name string) error {
	return domain.E(domain.KindNotFound, domain.CodeTemplateNotFound,
		"there is no such template; call list_templates").WithParams(map[string]any{"template": name})
}

// ListTemplates returns the built-in templates described in lang (ru or
// en; anything else means ru). Every signed-in user may list them.
func (s *Service) ListTemplates(lang string) []TemplateInfo {
	return templates.List(lang)
}

// CreateSiteFromTemplate creates a site of the actor and publishes the
// rendered template as its first release. An empty title uses the
// template's sample title. When the publication fails the site is deleted.
func (s *Service) CreateSiteFromTemplate(ctx context.Context, a Actor, name, title, slug, lang string,
	source domain.DeploySource) (DeployResult, error) {
	if !source.Valid() {
		source = domain.SourceAPI
	}
	lang = templates.Lang(lang)
	info, err := templates.Get(name, lang)
	if errors.Is(err, templates.ErrNotFound) {
		return DeployResult{}, errTemplateNotFound(name)
	}
	if err != nil {
		return DeployResult{}, err
	}
	if title == "" {
		title = info.SiteTitle
	}
	title, err = domain.ValidateTitle(title)
	if err != nil {
		return DeployResult{}, err
	}
	rendered, err := templates.Render(name, lang, title)
	if err != nil {
		return DeployResult{}, err
	}
	in := make([]InlineFile, 0, len(rendered))
	for _, f := range rendered {
		in = append(in, InlineFile{Path: f.Path, Content: string(f.Data)})
	}
	archive, err := buildInlineArchive(in)
	if err != nil {
		return DeployResult{}, err
	}
	upload, err := s.writeUpload(archive)
	if err != nil {
		return DeployResult{}, err
	}
	defer s.removeUpload(upload)
	if ok, retry := s.deployLimit.Allow(userKey(a.User.ID)); !ok {
		return DeployResult{}, domain.ErrRateLimited.WithRetryAfter(retry).
			WithParams(map[string]any{"limit": DeploysPerHour})
	}
	res, err := s.publishNewSite(ctx, a, title, slug, upload, source, map[string]any{
		"reason": "template", "template": name, "lang": lang,
	})
	if err != nil {
		return DeployResult{}, err
	}
	s.audit(ctx, a, "site.template", siteTarget(res.Site.Site.ID), map[string]any{"template": name, "lang": lang})
	return res, nil
}
