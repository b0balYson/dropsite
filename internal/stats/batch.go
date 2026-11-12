package stats

// Key identifies one site on one UTC day (YYYY-MM-DD).
type Key struct {
	SiteID string
	Day    string
}

// Batch accumulates page views from log lines until they are persisted.
type Batch struct {
	Views     map[Key]int64
	Pages     map[Key]map[string]int64
	Referrers map[Key]map[string]int64 // "" is a direct visit
	// Visitors holds visitor hashes (as strings) of days that still have a
	// salt; days without one count views only.
	Visitors map[Key]map[string]struct{}
	// Malformed counts lines that were not valid access-log entries.
	Malformed int
	// SkipHost, when set, excludes requests to matching hosts from page views
	// (release previews); such hosts still count as the site's own referrers.
	SkipHost func(host string) bool
}

// NewBatch returns an empty batch.
func NewBatch() *Batch {
	return &Batch{
		Views:     map[Key]int64{},
		Pages:     map[Key]map[string]int64{},
		Referrers: map[Key]map[string]int64{},
		Visitors:  map[Key]map[string]struct{}{},
	}
}

// Empty reports whether nothing was counted.
func (b *Batch) Empty() bool { return len(b.Views) == 0 }

// SiteFunc maps a normalized hostname to its site ID.
type SiteFunc func(host string) (siteID string, ok bool)

// SaltFunc returns the visitor salt of a day, or nil when visitors of that
// day are no longer counted.
type SaltFunc func(day string) ([]byte, error)

// AddLine parses one log line and counts it if it is a page view of a known
// site. It returns an error only when the salt lookup fails.
func (b *Batch) AddLine(line []byte, site SiteFunc, salt SaltFunc) error {
	h, parseErr := ParseLine(line)
	if parseErr != nil {
		b.Malformed++ // a broken line never stops collection
		return nil    //nolint:nilerr // malformed lines are counted, not fatal
	}
	if !h.IsPageView() {
		return nil
	}
	siteID, ok := site(h.Host)
	if !ok || (b.SkipHost != nil && b.SkipHost(h.Host)) {
		return nil
	}
	k := Key{SiteID: siteID, Day: h.Day()}
	b.Views[k]++
	inc(b.Pages, k, h.PagePath())
	ref := h.RefererHost()
	if refSite, own := site(ref); ref != "" && own && refSite == siteID {
		ref = "" // navigation inside the site is not a source
	}
	if ref != "" || h.Referer == "" {
		inc(b.Referrers, k, ref)
	}
	s, err := salt(k.Day)
	if err != nil {
		return err
	}
	if s != nil {
		set := b.Visitors[k]
		if set == nil {
			set = map[string]struct{}{}
			b.Visitors[k] = set
		}
		set[string(VisitorHash(s, h.ClientIP, h.UserAgent))] = struct{}{}
	}
	return nil
}

func inc(m map[Key]map[string]int64, k Key, name string) {
	inner := m[k]
	if inner == nil {
		inner = map[string]int64{}
		m[k] = inner
	}
	inner[name]++
}
