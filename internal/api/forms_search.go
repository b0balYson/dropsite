package api

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/service"
)

// Submission search and the inline CSV export: the same service methods as
// the MCP tools list_submissions and export_submissions.

// maxSearchDays bounds the days filter (ten years).
const maxSearchDays = 3650

type submissionSearchDTO struct {
	Items      []submissionDTO `json:"items"`
	Total      int             `json:"total"`
	NextCursor *string         `json:"next_cursor"`
	ServerTime time.Time       `json:"server_time"`
	CabinetURL string          `json:"cabinet_url"`
}

type submissionExportDTO struct {
	CSV        string `json:"csv"`
	Rows       int    `json:"rows"`
	Total      int    `json:"total"`
	Truncated  bool   `json:"truncated"`
	CabinetURL string `json:"cabinet_url"`
}

// boundedInt reads an optional integer query parameter in [lo, hi].
func boundedInt(q url.Values, name string, lo, hi int) (int, error) {
	v := q.Get(name)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < lo || n > hi {
		return 0, invalidField(name, name+" must be an integer from "+strconv.Itoa(lo)+" to "+strconv.Itoa(hi))
	}
	return n, nil
}

// queryTime reads YYYY-MM-DD (UTC midnight; for until the next midnight, so
// the whole day is included) or an RFC 3339 time.
func queryTime(q url.Values, name string, endOfDay bool) (time.Time, error) {
	v := strings.TrimSpace(q.Get(name))
	if v == "" {
		return time.Time{}, nil
	}
	if d, err := time.Parse(time.DateOnly, v); err == nil {
		if endOfDay {
			d = d.AddDate(0, 0, 1)
		}
		return d, nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, invalidField(name, name+" must be a date (YYYY-MM-DD) or an RFC 3339 time")
}

// submissionQuery reads form, q, since, until and days; days (the last N
// days by the server clock) overrides since.
func (a *api) submissionQuery(q url.Values) (service.SubmissionQuery, error) {
	since, err := queryTime(q, "since", false)
	if err != nil {
		return service.SubmissionQuery{}, err
	}
	until, err := queryTime(q, "until", true)
	if err != nil {
		return service.SubmissionQuery{}, err
	}
	days, err := boundedInt(q, "days", 1, maxSearchDays)
	if err != nil {
		return service.SubmissionQuery{}, err
	}
	if days > 0 {
		since = a.svc.Now().AddDate(0, 0, -days)
	}
	return service.SubmissionQuery{Form: strings.TrimSpace(q.Get("form")), Text: q.Get("q"), Since: since, Until: until}, nil
}

func (a *api) searchSubmissions(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	sq, err := a.submissionQuery(q)
	if err != nil {
		return err
	}
	if sq.Limit, err = boundedInt(q, "limit", 1, service.MaxSubmissionPage); err != nil {
		return err
	}
	sq.Cursor = q.Get("cursor")
	ctx, actor := r.Context(), actorFrom(r.Context())
	v, err := a.svc.GetSite(ctx, actor, r.PathValue("ref"))
	if err != nil {
		return err
	}
	p, err := a.svc.SearchFormSubmissions(ctx, actor, v.Site.ID, sq)
	if err != nil {
		return err
	}
	out := submissionSearchDTO{Items: make([]submissionDTO, 0, len(p.Items)), Total: p.Total,
		NextCursor: optional(p.Next), ServerTime: utc(a.svc.Now()), CabinetURL: a.svc.FormsPageURL(v.Site.ID)}
	for _, s := range p.Items {
		out.Items = append(out.Items, toSubmission(s))
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (a *api) exportSubmissionsText(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	sq, err := a.submissionQuery(q)
	if err != nil {
		return err
	}
	maxRows, err := boundedInt(q, "max_rows", 1, service.MaxInlineExportRows)
	if err != nil {
		return err
	}
	ctx, actor := r.Context(), actorFrom(r.Context())
	v, err := a.svc.GetSite(ctx, actor, r.PathValue("ref"))
	if err != nil {
		return err
	}
	e, err := a.svc.ExportFormSubmissionsText(ctx, actor, v.Site.ID, sq, maxRows, 0)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, submissionExportDTO{CSV: e.CSV, Rows: e.Rows, Total: e.Total, Truncated: e.Truncated,
		CabinetURL: a.svc.FormsPageURL(v.Site.ID)})
	return nil
}
