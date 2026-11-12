// Package quality computes a static quality report of a release at publish
// time: broken internal links and missing files, images without alt text,
// pages without title, description, viewport or language, page weight,
// resources loaded from other servers, and a simple score. It reads files
// only: no network, no rendering, no scripts.
//
// The report types are shared by the API, its client and both MCP servers,
// so the remote and local tools return identical data.
package quality

// Limits of one analysis and of the stored report.
const (
	MaxPages       = 1000
	MaxCSSFiles    = 500
	MaxPageBytes   = 2 << 20
	MaxCSSBytes    = 1 << 20
	MaxListItems   = 50
	HeavyPageBytes = 3 << 20
	topN           = 5
	maxIssues      = 5
)

// Issue is one problem found in a file.
type Issue struct {
	Page   string `json:"page" jsonschema:"the site file where it was found (page or stylesheet)"`
	Target string `json:"target" jsonschema:"the link, path or address as written in that file"`
	Kind   string `json:"kind,omitempty" jsonschema:"resource kind: image, stylesheet, script, font, icon, media or other"`
}

// PageSize is the weight of a page with its own files.
type PageSize struct {
	Page  string `json:"page"`
	Bytes int64  `json:"bytes" jsonschema:"HTML plus the local images, styles, scripts and fonts it loads"`
}

// FileSize is the size of one file.
type FileSize struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

// Counts are the full numbers of problems; the lists keep at most
// MaxListItems each.
type Counts struct {
	BrokenLinks        int `json:"broken_links"`
	MissingAssets      int `json:"missing_assets"`
	ImagesWithoutAlt   int `json:"images_without_alt"`
	MissingTitle       int `json:"missing_title"`
	MissingDescription int `json:"missing_description"`
	MissingViewport    int `json:"missing_viewport"`
	MissingLang        int `json:"missing_lang"`
	ExternalResources  int `json:"external_resources"`
	ExternalHosts      int `json:"external_hosts"`
	HeavyPages         int `json:"heavy_pages"`
}

// PagesMissing lists pages without a piece of metadata.
type PagesMissing struct {
	Title       []string `json:"title" jsonschema:"pages without a non-empty <title>"`
	Description []string `json:"description" jsonschema:"pages without <meta name=description>"`
	Viewport    []string `json:"viewport" jsonschema:"pages without <meta name=viewport> (layout breaks on phones)"`
	Lang        []string `json:"lang" jsonschema:"pages whose <html> has no lang attribute"`
}

// Optimization summarizes what publish-time optimization added.
type Optimization struct {
	ImagesConverted int      `json:"images_converted" jsonschema:"images that got a lighter WebP copy"`
	BytesSaved      int64    `json:"bytes_saved" jsonschema:"bytes saved for browsers that support WebP"`
	Generated       []string `json:"generated" jsonschema:"fallback files added: robots.txt, sitemap.xml, favicon.ico"`
	TimedOut        bool     `json:"timed_out" jsonschema:"true when some images were left because of the time budget"`
}

// Report is the quality report of one release.
type Report struct {
	Score             int           `json:"score" jsonschema:"0 to 100"`
	Grade             string        `json:"grade" jsonschema:"A (90+), B (75+), C (50+) or D"`
	Summary           string        `json:"summary" jsonschema:"one sentence to relay to the user"`
	Pages             int           `json:"pages" jsonschema:"HTML pages in the release"`
	Files             int           `json:"files"`
	TotalBytes        int64         `json:"total_bytes"`
	Counts            Counts        `json:"counts"`
	BrokenLinks       []Issue       `json:"broken_links" jsonschema:"links to pages that do not exist in the site"`
	MissingAssets     []Issue       `json:"missing_assets" jsonschema:"images, styles, scripts or fonts referenced but not in the site"`
	ImagesWithoutAlt  []Issue       `json:"images_without_alt"`
	ExternalResources []Issue       `json:"external_resources" jsonschema:"files loaded from other servers; ship them with the site instead"`
	ExternalHosts     []string      `json:"external_hosts"`
	PagesMissing      PagesMissing  `json:"pages_missing"`
	HeaviestPages     []PageSize    `json:"heaviest_pages"`
	LargestFiles      []FileSize    `json:"largest_files"`
	Optimization      *Optimization `json:"optimization,omitempty" jsonschema:"what publish-time optimization added, when it ran"`
	Truncated         bool          `json:"truncated" jsonschema:"true when the site was too large to check completely"`
}

// Summary is the short form of a report included in publication results.
type Summary struct {
	Score   int      `json:"score"`
	Grade   string   `json:"grade"`
	Summary string   `json:"summary"`
	Issues  []string `json:"issues" jsonschema:"up to five main problems to fix, most important first"`
}

// Normalize replaces nil lists with empty ones so the JSON form is stable
// (arrays, never null).
func (r *Report) Normalize() {
	for _, l := range []*[]Issue{&r.BrokenLinks, &r.MissingAssets, &r.ImagesWithoutAlt, &r.ExternalResources} {
		if *l == nil {
			*l = []Issue{}
		}
	}
	for _, l := range []*[]string{&r.ExternalHosts, &r.PagesMissing.Title, &r.PagesMissing.Description,
		&r.PagesMissing.Viewport, &r.PagesMissing.Lang} {
		if *l == nil {
			*l = []string{}
		}
	}
	if r.HeaviestPages == nil {
		r.HeaviestPages = []PageSize{}
	}
	if r.LargestFiles == nil {
		r.LargestFiles = []FileSize{}
	}
	if r.Optimization != nil && r.Optimization.Generated == nil {
		r.Optimization.Generated = []string{}
	}
}
