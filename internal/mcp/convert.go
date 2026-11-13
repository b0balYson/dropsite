package mcp

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/idna"

	"github.com/b0balYson/dropsite/internal/quality"
)

// Conversions and plain-language texts shared by the remote backend
// (internal/mcp/remote, from service types) and the local stdio backend
// (cmd/dropsite, from API responses), so both servers answer alike. Inputs
// are plain values: this package must not import the server.

// Not-found texts of the tools.
const (
	NotFoundSite = "not found: no such site or release among this user's sites; call list_sites " +
		"(or list_deploys) to see valid IDs and slugs"
	NotFoundChannel = "not found: no such site or channel among this user's sites; " +
		"call list_sites (or list_channels) to see valid IDs"
	NotFoundDomain = "not found: no such site among this user's sites, or the domain is not attached to it; " +
		"call list_domains"
	NotFoundConnection = "not found: no such active connection; call list_connections to see valid kinds and IDs"
	// InternalErrorText replaces every internal error.
	InternalErrorText = "internal server error; try again in a minute, and if it persists tell the user to " +
		"contact the administrator"
)

// RateLimitedError tells the model when to retry.
func RateLimitedError(secs int64) error {
	return fmt.Errorf("too many requests: retry in %d seconds", max(secs, 1))
}

// CodedError renders a domain error: its message, its parameters in key
// order and its code, e.g. "slug is taken (slug: promo) [slug_taken]".
func CodedError(message, code string, params map[string]any) error {
	msg := message
	if len(params) > 0 {
		keys := make([]string, 0, len(params))
		for k := range params {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s: %v", k, params[k]))
		}
		msg += " (" + strings.Join(parts, ", ") + ")"
	}
	return errors.New(msg + " [" + code + "]")
}

// Stamp formats a time as RFC 3339 UTC; nil or zero gives "".
func Stamp(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// LastDay is the last day a site with this expiry works (the expiry is the
// first instant after that day).
func LastDay(at *time.Time) string {
	if at == nil {
		return ""
	}
	return at.UTC().Add(-time.Nanosecond).Format(time.DateOnly)
}

// Host is an address of a site as the converters need it.
type Host struct {
	Name   string
	Kind   string // default, slug, apex or external
	Status string // active, pending or rejected
}

// NewSiteExtras builds the access flags and own domains of a site.
func NewSiteExtras(noindex, protected bool, expiresAt *time.Time, hosts []Host) SiteExtras {
	x := SiteExtras{HiddenFromSearch: noindex, PasswordProtected: protected, ExpiresAt: Stamp(expiresAt)}
	for _, h := range hosts {
		switch h.Kind {
		case "apex":
			x.RootDomain = true
		case "external":
			x.OwnDomains = append(x.OwnDomains, DomainBrief{Domain: h.Name, Status: h.Status})
		}
	}
	return x
}

// NewSiteAccess is set_site_access output (without the summary).
func NewSiteAccess(siteID, url string, noindex, protected bool, expiresAt *time.Time) SiteAccess {
	return SiteAccess{
		SiteID: siteID, URL: url, HiddenFromSearch: noindex, PasswordProtected: protected,
		ExpiresAt: Stamp(expiresAt), LastDay: LastDay(expiresAt),
	}
}

// NewDomain describes an own domain; url is the site's address on it.
func NewDomain(host, status, rejectReason, url string, addedAt time.Time) Domain {
	d := Domain{Domain: host, Status: status, RejectReason: rejectReason, URL: url, AddedAt: Stamp(&addedAt)}
	if u, err := idna.Display.ToUnicode(host); err == nil && u != host {
		d.DisplayName = u
	}
	return d
}

// NewDNSSetup turns the DNS targets of a site into records and plain steps.
func NewDNSSetup(cnameTarget string, serverIPs []string) DNSSetup {
	d := DNSSetup{
		Subdomain:      []DNSRecord{{Type: "CNAME", Name: "<your subdomain, e.g. shop.example.com>", Value: cnameTarget}},
		RootDomain:     []DNSRecord{},
		ServerIPsKnown: len(serverIPs) > 0,
	}
	for _, ip := range serverIPs {
		typ := "A"
		if strings.Contains(ip, ":") {
			typ = "AAAA"
		}
		d.RootDomain = append(d.RootDomain, DNSRecord{Type: typ, Name: "<your domain, e.g. example.com>", Value: ip})
	}
	d.Steps = "At the company where the domain is registered (its DNS settings), create a CNAME record pointing " +
		"to " + cnameTarget + " for a subdomain such as shop.example.com"
	if d.ServerIPsKnown {
		d.Steps += ", or for a root domain such as example.com the A/AAAA records from for_root_domain"
	} else {
		d.Steps += "; for a root domain such as example.com ask the administrator for the server's IP address " +
			"and create an A record with it"
	}
	d.Steps += ". DNS changes can take up to a few hours. The domain starts opening the site after an " +
		"administrator approves it; HTTPS is set up automatically on the first visit."
	return d
}

// NewStats builds get_site_stats output (without the summary) and finds the
// busiest day.
func NewStats(siteID, from, to string, views, visitors int64, daily []DayViews, pages, sources []NamedViews) SiteStats {
	out := SiteStats{
		SiteID: siteID, From: from, To: to, TotalViews: views, TotalVisitors: visitors,
		Daily: make([]DayViews, 0, len(daily)), TopPages: nonNilViews(pages), TopSources: nonNilViews(sources),
	}
	for _, d := range daily {
		out.Daily = append(out.Daily, d)
		if d.Views > 0 && (out.BusiestDay == nil || d.Views > out.BusiestDay.Views) {
			dv := d
			out.BusiestDay = &dv
		}
	}
	return out
}

func nonNilViews(v []NamedViews) []NamedViews {
	if v == nil {
		return []NamedViews{}
	}
	return v
}

// ChannelWords names a delivery channel in words.
func ChannelWords(kind, target string) string {
	switch kind {
	case "telegram":
		if target == "" {
			return "Telegram (not linked yet)"
		}
		return "Telegram chat " + target
	case "email":
		return "e-mail " + target
	case "sheets":
		return "Google Sheet " + target
	}
	return "webhook " + target
}

// ChannelInput is a delivery channel as stored.
type ChannelInput struct {
	ID       string
	Kind     string
	Target   string
	Status   string // pending or active
	Enabled  bool
	PausedAt *time.Time
	Failures int
}

// NewChannel describes a channel with its state in plain words.
func NewChannel(c ChannelInput) Channel {
	out := Channel{
		ID: c.ID, Kind: c.Kind, Target: c.Target, Enabled: c.Enabled, PausedAt: Stamp(c.PausedAt), Failures: c.Failures,
	}
	what := ChannelWords(c.Kind, c.Target)
	switch {
	case c.Status == "pending":
		out.State = "waiting_for_confirmation"
		if c.Kind == "telegram" && c.Target == "" {
			out.Summary = what + ": waiting until the user opens the bot link and presses Start; " +
				"if the link expired, delete the channel and add it again"
		} else {
			out.Summary = what + ": waiting for the confirmation code (verify_channel)"
		}
	case c.PausedAt != nil:
		out.State = "paused"
		out.Summary = fmt.Sprintf("%s: paused at %s after %d failed deliveries in a row; submissions wait. "+
			"Fix the receiver, then turn the channel on (set_channel_enabled) to resume",
			what, Stamp(c.PausedAt), c.Failures)
	case !c.Enabled:
		out.State = "off"
		out.Summary = what + ": turned off, nothing is sent here"
	default:
		out.State = "active"
		out.Summary = what + ": active, new submissions are delivered here"
	}
	return out
}

// NewChannelAdded is add_channel output with the next step to tell the user.
func NewChannelAdded(c Channel, link, secret string) ChannelAdded {
	out := ChannelAdded{Channel: c, Link: link, Secret: secret}
	id := c.ID
	switch {
	case link != "":
		out.NextStep = "Give the user this link and ask them to open it in Telegram and press Start in the chat " +
			"that should receive the submissions (for a group: add the bot to the group first, then send there " +
			"the /start command with the code from the link). The link works for 30 minutes. Then call " +
			"list_channels to check that channel " + id + " is active."
	case secret != "":
		out.NextStep = "The webhook is active. Give the user the signing secret now: it is shown only this once. " +
			"The receiving system checks the X-Dropsite-Signature header (sha256=HMAC-SHA256 of the body with " +
			"this secret). If the secret is lost, rotate_channel_secret issues a new one."
	case c.Kind == "sheets":
		out.NextStep = "The Google Sheet is connected: the header row was written and every new submission is " +
			"added as a row; new form fields get new columns on the right. Tell the user to keep the sheet shared " +
			"with the service account as Editor."
	case c.Kind == "email":
		out.NextStep = "A 6-digit code was sent to " + c.Target + ". Ask the user for it (it may be in the " +
			"spam folder) and call verify_channel with channel_id " + id + ". The code works 30 minutes."
	default:
		out.NextStep = "The bot sent a 6-digit code to Telegram chat " + c.Target + ". Ask the user for it " +
			"and call verify_channel with channel_id " + id + ". The code works 30 minutes."
	}
	return out
}

// NewSecretRotated is rotate_channel_secret output.
func NewSecretRotated(c Channel, secret string) ChannelAdded {
	return ChannelAdded{Channel: c, Secret: secret,
		NextStep: "Give the user the new signing secret now: it is shown only this once, and the old secret " +
			"no longer works, so the receiving system must be updated."}
}

// DeliveryInput is the state of a submission on one channel.
type DeliveryInput struct {
	ChannelID     string
	Kind          string
	Target        string
	Status        string // pending, sent, failed or cancelled
	Attempts      int
	LastError     string
	NextAttemptAt *time.Time
	SentAt        *time.Time
}

// NewDelivery describes a delivery in plain words.
func NewDelivery(d DeliveryInput) Delivery {
	return Delivery{ChannelID: d.ChannelID, Channel: ChannelWords(d.Kind, d.Target), Status: d.Status,
		Summary: deliveryWords(d), Attempts: d.Attempts}
}

func deliveryWords(d DeliveryInput) string {
	switch d.Status {
	case "sent":
		return "delivered at " + Stamp(d.SentAt)
	case "failed":
		return fmt.Sprintf("not delivered after %d attempts: %s", d.Attempts, d.LastError)
	case "cancelled":
		return "not sent: the channel was turned off or removed"
	}
	if d.Attempts == 0 {
		return "waiting to be sent"
	}
	return fmt.Sprintf("not delivered yet: %d attempts failed (%s); next try at %s",
		d.Attempts, d.LastError, Stamp(d.NextAttemptAt))
}

// NewExport is export_submissions output with its summary.
func NewExport(csv string, rows, total int, truncated bool, cabinetURL string) SubmissionsExport {
	out := SubmissionsExport{CSV: csv, Rows: rows, Total: total, Truncated: truncated, CabinetURL: cabinetURL}
	switch {
	case total == 0:
		out.Summary = "There are no submissions for this filter."
	case truncated:
		out.Summary = fmt.Sprintf("Only the %d newest of %d submissions are included. The full CSV can be "+
			"downloaded on the submissions page of the web interface: %s", rows, total, cabinetURL)
	default:
		out.Summary = fmt.Sprintf("All %d submissions are included.", rows)
	}
	return out
}

// FormEndpoint is where the forms of a site post, with <name> as a placeholder.
func FormEndpoint(siteURL string) string {
	if siteURL == "" {
		return ""
	}
	return strings.TrimSuffix(siteURL, "/") + "/_form/<name>"
}

// FilesOutcome is a new release made by update_site_files or commit_upload.
type FilesOutcome struct {
	SiteID    string
	DeployID  string
	Base      string
	URL       string
	URLs      []string
	Written   int
	Deleted   int
	FileCount int
	Fetched   []FetchedFile
	Quality   *quality.Summary
	// Draft and PreviewURL describe a draft (change staged-publish).
	Draft      bool
	PreviewURL string
}

// NewFilesResult builds the tool output with a summary starting with verb
// ("Site updated", "Site published").
func NewFilesResult(o FilesOutcome, verb string) FilesResult {
	if o.Draft {
		verb = "Draft saved"
	}
	parts := []string{fmt.Sprintf("%s: %d file(s) written", verb, o.Written)}
	if o.Deleted > 0 {
		parts = append(parts, fmt.Sprintf("%d removed", o.Deleted))
	}
	resized := 0
	for _, f := range o.Fetched {
		if f.Resized {
			resized++
		}
	}
	if len(o.Fetched) > 0 {
		parts = append(parts, fmt.Sprintf("%d downloaded (%d photo(s) scaled down)", len(o.Fetched), resized))
	}
	summary := strings.Join(parts, ", ") + fmt.Sprintf("; the site now has %d files and is live at %s. "+
		"The previous version can be restored with rollback.", o.FileCount, o.URL)
	if o.Draft {
		summary = strings.Join(parts, ", ") + draftSummary(o.FileCount, o.URL, o.PreviewURL)
	}
	if o.Quality != nil {
		summary += " " + o.Quality.Summary
	}
	fetched := o.Fetched
	if len(fetched) == 0 {
		fetched = nil
	}
	return FilesResult{
		SiteID: o.SiteID, DeployID: o.DeployID, BaseDeployID: o.Base, URL: o.URL, URLs: o.URLs,
		Written: o.Written, Deleted: o.Deleted, FileCount: o.FileCount, Fetched: fetched, Summary: summary,
		Quality: o.Quality, Draft: o.Draft, PreviewURL: o.PreviewURL,
	}
}
