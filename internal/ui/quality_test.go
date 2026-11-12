package ui

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/testenv"
)

func TestQualityReportScreen(t *testing.T) {
	h := newHarness(t)
	alice := h.loginUser("alice@pp.test")
	v := h.createSite(alice, "Docs", "docs")
	page := `<html><body><a href="/abuot">x</a><link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Inter"></body></html>`
	res, err := h.env.Svc.Deploy(context.Background(), alice, v.Site.ID,
		bytes.NewReader(testenv.Zip(t, map[string]string{"index.html": page})), domain.SourceUI)
	if err != nil {
		t.Fatal(err)
	}
	report := "/sites/" + v.Site.ID + "/deploys/" + res.Deploy.ID
	site := h.get("/sites/" + v.Site.ID)
	wantContains(t, site.body, `href="`+report+`"`, "release__quality--")

	got := h.get(report)
	wantStatus(t, got, http.StatusOK)
	wantContains(t, got.body, "Отчёт о качестве", "Битые ссылки", "abuot", "googleapis",
		"Нет заголовка", "favicon.ico")
	wantStatus(t, h.get("/sites/"+v.Site.ID+"/deploys/nope"), http.StatusNotFound)

	h.c = h.newClient()
	h.loginUser("bob@pp.test")
	wantStatus(t, h.get(report), http.StatusNotFound)
}
