package mcp

import (
	"context"
	"fmt"
	"strings"
)

// SiteAttached is what is attached to a site besides its files; get_site
// returns it so an assistant in a new chat sees the whole picture. Access,
// expiry and own domains are the other fields of Site.
type SiteAttached struct {
	Forms           []string             `json:"forms" jsonschema:"forms of the site that received requests still stored"`
	Channels        int                  `json:"channels" jsonschema:"delivery channels for form requests (Telegram, e-mail, webhook)"`
	WorkingChannels int                  `json:"working_channels" jsonschema:"channels that are confirmed, turned on and not paused"`
	RequireAltcha   bool                 `json:"require_altcha" jsonschema:"true when forms require the anti-robot check"`
	Counters        CounterIDs           `json:"counters" jsonschema:"the site's own analytics counters"`
	AdminCounters   bool                 `json:"admin_counters" jsonschema:"true when an administrator adds counters to every site"`
	Collections     []AttachedCollection `json:"collections" jsonschema:"data collections the site's pages load (prices, menus, reviews)"`
	ProxyEndpoints  []string             `json:"proxy_endpoints" jsonschema:"names of API key proxy endpoints the site's pages call"`
	Summary         string               `json:"summary" jsonschema:"what is attached, in plain words"`
}

// AttachedCollection is one data collection of a site in SiteAttached.
type AttachedCollection struct {
	Name      string `json:"name" jsonschema:"collection name, loaded from /_dropsite/data/<name>"`
	Mode      string `json:"mode" jsonschema:"public (only the owner writes) or moderated (visitors propose)"`
	Documents int    `json:"documents" jsonschema:"number of documents"`
	Pending   int    `json:"pending" jsonschema:"documents waiting for approval"`
}

// SiteAttacher returns what is attached to a site; get_site adds it when the
// backend implements it.
type SiteAttacher interface {
	SiteAttachments(ctx context.Context, ref string) (SiteAttached, error)
}

// NewSiteAttached builds SiteAttached with its summary.
func NewSiteAttached(forms []string, channels, working int, altcha bool, own, global CounterIDs) SiteAttached {
	if forms == nil {
		forms = []string{}
	}
	a := SiteAttached{Forms: forms, Channels: channels, WorkingChannels: working, RequireAltcha: altcha, Counters: own,
		AdminCounters: global != CounterIDs{}}
	a.Collections = []AttachedCollection{}
	a.ProxyEndpoints = []string{}
	a.Summary = attachedSummary(a)
	return a
}

// WithData adds the site's collections and proxy endpoints and refreshes the
// summary.
func (a SiteAttached) WithData(cols []AttachedCollection, proxies []string) SiteAttached {
	if cols == nil {
		cols = []AttachedCollection{}
	}
	if proxies == nil {
		proxies = []string{}
	}
	a.Collections, a.ProxyEndpoints = cols, proxies
	a.Summary = attachedSummary(a)
	return a
}

func attachedSummary(a SiteAttached) string {
	var parts []string
	if len(a.Forms) > 0 {
		parts = append(parts, "Forms with requests: "+strings.Join(a.Forms, ", ")+".")
	} else {
		parts = append(parts, "No form requests stored.")
	}
	switch {
	case a.Channels == 0:
		parts = append(parts, "No delivery channels: requests are only stored.")
	case a.WorkingChannels == a.Channels:
		parts = append(parts, fmt.Sprintf("Delivery channels: %d, all working.", a.Channels))
	default:
		parts = append(parts, fmt.Sprintf("Delivery channels: %d, working: %d.", a.Channels, a.WorkingChannels))
	}
	if a.RequireAltcha {
		parts = append(parts, "Forms require the anti-robot check.")
	}
	var counters []string
	if a.Counters.YandexMetricaID != "" {
		counters = append(counters, "Yandex Metrica "+a.Counters.YandexMetricaID)
	}
	if a.Counters.GoogleAnalyticsID != "" {
		counters = append(counters, "Google Analytics "+a.Counters.GoogleAnalyticsID)
	}
	if len(counters) > 0 {
		parts = append(parts, "Analytics counters: "+strings.Join(counters, ", ")+".")
	} else {
		parts = append(parts, "No own analytics counters.")
	}
	if a.AdminCounters {
		parts = append(parts, "The administrator adds counters to every site.")
	}
	if len(a.Collections) > 0 {
		var cs []string
		for _, c := range a.Collections {
			d := fmt.Sprintf("%s (%s, %d documents", c.Name, c.Mode, c.Documents)
			if c.Pending > 0 {
				d += fmt.Sprintf(", %d waiting for approval", c.Pending)
			}
			cs = append(cs, d+")")
		}
		parts = append(parts, "Data collections: "+strings.Join(cs, ", ")+".")
	}
	if len(a.ProxyEndpoints) > 0 {
		parts = append(parts, "API key proxies: "+strings.Join(a.ProxyEndpoints, ", ")+".")
	}
	return strings.Join(parts, " ")
}
