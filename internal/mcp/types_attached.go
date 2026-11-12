package mcp

import (
	"context"
	"fmt"
	"strings"
)

// SiteAttached is what is attached to a site besides its files; get_site
// returns it so an assistant in a new chat sees the whole picture. Access,
// expiry and own domains are the other fields of Site.
//
// Extension point (change site-collections): add the site's collections
// here (names and document counts) and fill them in both backends.
type SiteAttached struct {
	Forms           []string   `json:"forms" jsonschema:"forms of the site that received requests still stored"`
	Channels        int        `json:"channels" jsonschema:"delivery channels for form requests (Telegram, e-mail, webhook)"`
	WorkingChannels int        `json:"working_channels" jsonschema:"channels that are confirmed, turned on and not paused"`
	RequireAltcha   bool       `json:"require_altcha" jsonschema:"true when forms require the anti-robot check"`
	Counters        CounterIDs `json:"counters" jsonschema:"the site's own analytics counters"`
	AdminCounters   bool       `json:"admin_counters" jsonschema:"true when an administrator adds counters to every site"`
	Summary         string     `json:"summary" jsonschema:"what is attached, in plain words"`
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
	return strings.Join(parts, " ")
}
