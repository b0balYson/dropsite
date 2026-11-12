package domain

// PublishSettings are a site's options for the publication pipeline.
type PublishSettings struct {
	SiteID string
	// Optimize adds WebP variants and fallback robots.txt, sitemap.xml and
	// favicon.ico at publish time (when the installation allows it).
	Optimize bool
}

// QualityScore is the score and grade of a release's quality report.
type QualityScore struct {
	Score int
	Grade string
}

// DefaultPublishSettings are the settings of a site without a stored row.
func DefaultPublishSettings(siteID string) PublishSettings {
	return PublishSettings{SiteID: siteID, Optimize: true}
}
