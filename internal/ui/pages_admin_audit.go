package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/b0balYson/dropsite/internal/service"
)

const dateLayout = "2006-01-02"

type metaItem struct {
	Key   string
	Value string
}

type auditRow struct {
	service.AuditRow
	Label string
	Items []metaItem
}

type auditFilter struct {
	User   string
	Action string
	From   string
	To     string
}

type auditData struct {
	Rows    []auditRow
	Filter  auditFilter
	Actions []string
	Users   []service.UserSummary
	Pager   pager
}

func (s *server) auditPage(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	n, page := pageParam(r)
	f, filter := parseAuditFilter(r)
	filter.Page = page
	rows, total, err := s.svc.ListAudit(r.Context(), rc.actor, filter)
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	actions, err := s.svc.ListAuditActions(r.Context(), rc.actor)
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	users, _, err := s.svc.ListUsers(r.Context(), rc.actor, service.UserFilter{Page: service.Page{Limit: service.MaxPageSize}})
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	data := auditData{Filter: f, Actions: actions, Users: users, Pager: newPager("/admin/audit", r.URL.Query(), n, total)}
	for _, row := range rows {
		data.Rows = append(data.Rows, auditRow{AuditRow: row, Label: actionLabel(rc.loc, row.Action), Items: metaItems(row.Meta)})
	}
	s.page(w, r, rc, http.StatusOK, "admin_audit", "audit", "audit.title", data)
}

// parseAuditFilter reads the filter form; invalid values are ignored.
func parseAuditFilter(r *http.Request) (auditFilter, service.AuditFilter) {
	q := r.URL.Query()
	f := auditFilter{User: q.Get("user"), Action: q.Get("action"), From: q.Get("from"), To: q.Get("to")}
	var out service.AuditFilter
	if id, err := strconv.ParseInt(f.User, 10, 64); err == nil && id > 0 {
		out.ActorID = &id
	} else {
		f.User = ""
	}
	out.Action = f.Action
	if t, err := time.Parse(dateLayout, f.From); err == nil {
		out.From = &t
	} else {
		f.From = ""
	}
	if t, err := time.Parse(dateLayout, f.To); err == nil {
		end := t.Add(day) // the store treats To as exclusive; the form means "through this day"
		out.To = &end
	} else {
		f.To = ""
	}
	return f, out
}

func actionLabel(l *locale, action string) string {
	if key := "audit.action." + action; l.Has(key) {
		return l.T(key)
	}
	return action
}

// metaItems renders audit metadata as sorted key/value pairs.
func metaItems(meta map[string]any) []metaItem {
	keys := make([]string, 0, len(meta))
	for k := range meta {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]metaItem, 0, len(keys))
	for _, k := range keys {
		out = append(out, metaItem{Key: k, Value: metaValue(meta[k])})
	}
	return out
}

func metaValue(v any) string {
	switch x := v.(type) {
	case nil:
		return "—"
	case string:
		if x == "" {
			return "—"
		}
		return x
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool, int, int64:
		return fmt.Sprint(x)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}
