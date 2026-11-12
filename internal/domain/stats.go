package domain

// DayStat is the traffic of one site on one UTC day (YYYY-MM-DD).
type DayStat struct {
	Day      string
	Views    int64
	Visitors int64
}

// NamedCount is a page path or a referrer host with its number of views.
// The name "*" stands for everything beyond the stored rows ("other"); an
// empty referrer host is a direct visit.
type NamedCount struct {
	Name  string
	Views int64
}

// Counters are third-party analytics IDs injected into published HTML.
type Counters struct {
	MetrikaID string // Yandex Metrica counter number
	GA4ID     string // Google Analytics 4 measurement ID (G-XXXX)
}

// Empty reports whether no counter is set.
func (c Counters) Empty() bool { return c.MetrikaID == "" && c.GA4ID == "" }
