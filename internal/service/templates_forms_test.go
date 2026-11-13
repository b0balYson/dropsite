package service

import (
	"context"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/forms"
)

var (
	servedForm  = regexp.MustCompile(`action="/_form/([a-z0-9_-]+)"`)
	servedField = regexp.MustCompile(`<(?:input|select|textarea) id="[^"]+" name="([^"]+)"`)
	servedNext  = regexp.MustCompile(`name="_next" value="([^"]+)"`)
)

// A site created from any template accepts a post of the form it was
// published with and stores the submission under the form's name.
func TestTemplateSitesAcceptTheirForms(t *testing.T) {
	e, _ := formEnv(t)
	ctx := context.Background()
	ann := e.user("ann@pp.test")
	for _, info := range e.svc.ListTemplates("ru") {
		t.Run(info.Name, func(t *testing.T) {
			res, err := e.svc.CreateSiteFromTemplate(ctx, ann, info.Name, "", "", "en", domain.SourceUI)
			if err != nil {
				t.Fatal(err)
			}
			host := res.Site.Hostnames[0].Hostname
			body, err := e.served(host, "index.html")
			if err != nil {
				t.Fatal(err)
			}
			form, next := servedForm.FindStringSubmatch(body), servedNext.FindStringSubmatch(body)
			if form == nil || next == nil {
				t.Fatalf("no form or _next in index.html")
			}
			if _, err := e.served(host, strings.TrimPrefix(next[1], "/")); err != nil {
				t.Fatalf("_next page %s: %v", next[1], err)
			}
			vals := url.Values{"_hp": {""}, "_next": {next[1]},
				"_t": {strconv.FormatInt(time.Now().Add(-time.Minute).UnixMilli(), 10)}}
			for _, f := range servedField.FindAllStringSubmatch(body, -1) {
				vals.Set(f[1], "sample "+f[1])
			}
			data, err := forms.Parse("application/x-www-form-urlencoded", strings.NewReader(vals.Encode()))
			if err != nil || data.Next != next[1] {
				t.Fatalf("parse %+v %v", data, err)
			}
			out, err := e.svc.SubmitForm(ctx, FormPost{Host: host, Form: form[1], Origin: "https://" + host,
				FetchSite: "same-origin", IP: "203.0.113.9", Data: data})
			if err != nil || !out.Stored {
				t.Fatalf("submit %+v %v", out, err)
			}
			subs, total, err := e.svc.ListFormSubmissions(ctx, ann, res.Site.Site.ID, form[1], Page{})
			if err != nil || total != 1 || len(subs[0].Fields) < 3 {
				t.Fatalf("submissions %+v %v", subs, err)
			}
			for _, f := range subs[0].Fields {
				if strings.HasPrefix(f.Name, "_") {
					t.Errorf("service field %s stored", f.Name)
				}
			}
		})
	}
}
