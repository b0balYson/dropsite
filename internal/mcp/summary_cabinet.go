package mcp

import (
	"fmt"
	"strconv"
	"strings"
)

// Short plain-language summaries the assistant can pass on to the user.

// HumanBytes formats a size in binary units, e.g. 50 MB.
func HumanBytes(n int64) string {
	const unit = 1024
	switch {
	case n < unit:
		return strconv.FormatInt(n, 10) + " bytes"
	case n < unit*unit:
		return trimUnit(float64(n)/unit) + " KB"
	case n < unit*unit*unit:
		return trimUnit(float64(n)/(unit*unit)) + " MB"
	default:
		return trimUnit(float64(n)/(unit*unit*unit)) + " GB"
	}
}

func trimUnit(v float64) string {
	return strings.TrimSuffix(strconv.FormatFloat(v, 'f', 1, 64), ".0")
}

func accountSummary(a Account) string {
	s := fmt.Sprintf("%s has %d of %d sites. One site may be up to %s (at most %d files, %s per file); "+
		"the last %d releases of each site are kept for rollback. Publishing through this chat takes up to %s at once.",
		a.Email, a.SitesUsed, a.SitesLimit, a.MaxSiteSize, a.MaxFiles, a.MaxFileSize, a.KeepReleases,
		HumanBytes(a.DeployFilesMaxBytes))
	if a.IsAdmin {
		s += " As an administrator you can put a site on the root domain (set_root_site)"
		if a.RootSite != nil {
			s += fmt.Sprintf("; it now shows %q (%s)", a.RootSite.Title, a.RootSite.ID)
		} else {
			s += "; it shows nothing now"
		}
		s += ". Other administration is done in the web interface."
		if a.UpdateAvailable {
			s += fmt.Sprintf(" Dropsite %s is available (what's new: %s); an administrator updates in the web "+
				"interface (Settings, Updates), see check_updates.", a.LatestVersion, a.ReleaseNotesURL)
		}
	}
	return s
}

func accessSummary(a SiteAccess) string {
	parts := make([]string, 0, 3)
	if a.PasswordProtected {
		parts = append(parts, "visitors must enter a password to open the site")
	} else {
		parts = append(parts, "the site opens for everyone")
	}
	if a.HiddenFromSearch {
		parts = append(parts, "search engines are asked not to index it")
	} else {
		parts = append(parts, "search engines may index it")
	}
	if a.LastDay != "" {
		parts = append(parts, "it works until the end of "+a.LastDay+" (UTC)")
	} else {
		parts = append(parts, "it has no expiry")
	}
	s := strings.Join(parts, "; ") + "."
	return strings.ToUpper(s[:1]) + s[1:]
}

var statusWords = map[string]string{
	"active":   "works",
	"pending":  "waits for an administrator to approve it",
	"rejected": "was rejected",
}

func domainsSummary(ds []Domain) string {
	if len(ds) == 0 {
		return "The site has no own domains yet."
	}
	items := make([]string, 0, len(ds))
	for _, d := range ds {
		w := statusWords[d.Status]
		if d.RejectReason != "" {
			w += " (" + d.RejectReason + ")"
		}
		items = append(items, d.Domain+" "+w)
	}
	return "Own domains: " + strings.Join(items, "; ") + "."
}

func statsSummary(st SiteStats) string {
	s := fmt.Sprintf("From %s to %s: %d views by %d visitors.", st.From, st.To, st.TotalViews, st.TotalVisitors)
	if st.TotalViews == 0 {
		return s + " No visits yet in this period."
	}
	if st.BusiestDay != nil {
		s += fmt.Sprintf(" Busiest day: %s with %d views.", st.BusiestDay.Date, st.BusiestDay.Views)
	}
	if len(st.TopPages) > 0 {
		s += fmt.Sprintf(" Most viewed page: %s (%d views).", st.TopPages[0].Name, st.TopPages[0].Views)
	}
	for _, src := range st.TopSources {
		if src.Name != "" {
			s += fmt.Sprintf(" Top source: %s (%d views).", src.Name, src.Views)
			break
		}
	}
	return s
}

func counterList(c CounterIDs) []string {
	var out []string
	if c.YandexMetricaID != "" {
		out = append(out, "Yandex Metrica "+c.YandexMetricaID)
	}
	if c.GoogleAnalyticsID != "" {
		out = append(out, "Google Analytics "+c.GoogleAnalyticsID)
	}
	return out
}

func countersSummary(c Counters) string {
	s := "The site has no own analytics counters."
	if own := counterList(c.Own); len(own) > 0 {
		s = "The site's counters: " + strings.Join(own, ", ") + "."
	}
	if global := counterList(c.Global); len(global) > 0 {
		s += " Added to every site by the administrator: " + strings.Join(global, ", ") + "."
	}
	return s
}

func connectionsSummary(cs []Connection) string {
	if len(cs) == 0 {
		return "No connected apps or API tokens."
	}
	items := make([]string, 0, len(cs))
	for _, c := range cs {
		kind := "app"
		if c.Kind == ConnToken {
			kind = "API token"
		}
		item := fmt.Sprintf("%q (%s", c.Name, kind)
		if c.Current {
			item += ", this assistant"
		}
		items = append(items, item+")")
	}
	return fmt.Sprintf("%d connections: %s.", len(cs), strings.Join(items, ", "))
}
