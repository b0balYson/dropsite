package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
)

func TestSiteAttachments(t *testing.T) {
	e, _ := formEnv(t)
	ctx := context.Background()
	ann := e.user("ann@pp.test")
	v := mkFormSite(t, e, ann)

	empty, err := e.svc.SiteAttachments(ctx, ann, v.Site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if empty.SiteID != v.Site.ID || empty.FormNames == nil || len(empty.FormNames) != 0 || empty.Channels != 0 ||
		empty.WorkingChannels != 0 || empty.RequireAltcha || !empty.Counters.Own.Empty() ||
		empty.Collections == nil || len(empty.Collections) != 0 || empty.ProxyEndpoints == nil {
		t.Fatalf("empty: %+v", empty)
	}

	if _, err := e.svc.AddFormChannel(ctx, ann, v.Site.ID, domain.ChannelWebhook, "https://hooks.example.com/a"); err != nil {
		t.Fatal(err)
	}
	off, err := e.svc.AddFormChannel(ctx, ann, v.Site.ID, domain.ChannelWebhook, "https://hooks.example.com/b")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SetFormChannelEnabled(ctx, ann, v.Site.ID, off.Channel.ID, false); err != nil {
		t.Fatal(err)
	}
	host := v.Site.ID + "." + testBase
	for _, form := range []string{"lead", "order"} {
		p := post(host, "name", "Ann")
		p.Form = form
		if _, err := e.svc.SubmitForm(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.svc.SetFormSiteSettings(ctx, ann, v.Site.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SetSiteCounters(ctx, ann, v.Site.ID, domain.Counters{MetrikaID: "12345678"}, domain.SourceUI); err != nil {
		t.Fatal(err)
	}

	if _, err := e.svc.CreateCollection(ctx, ann, v.Site.ID,
		CollectionInput{Name: "menu", Mode: domain.CollectionPublic}); err != nil {
		t.Fatal(err)
	}
	docs := []DocumentInput{{ID: "tea", Data: json.RawMessage(`{"price":100}`)}, {Data: json.RawMessage(`{"price":200}`)}}
	if _, err := e.svc.SaveDocuments(ctx, ann, v.Site.ID, "menu", docs, false); err != nil {
		t.Fatal(err)
	}

	got, err := e.svc.SiteAttachments(ctx, ann, v.Site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.FormNames) != 2 || got.FormNames[0] != "lead" || got.FormNames[1] != "order" || got.Channels != 2 ||
		got.WorkingChannels != 1 || !got.RequireAltcha || got.Counters.Own.MetrikaID != "12345678" {
		t.Fatalf("attachments: %+v", got)
	}
	if len(got.Collections) != 1 || got.Collections[0] != (AttachedCollection{Name: "menu", Mode: domain.CollectionPublic,
		Documents: 2}) {
		t.Fatalf("collections: %+v", got.Collections)
	}

	bob := e.user("bob@pp.test")
	_, err = e.svc.SiteAttachments(ctx, bob, v.Site.ID)
	wantNotFound(t, err)
}
