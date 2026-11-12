//go:build e2e

package e2e

import (
	"net/http"
	"strings"
	"testing"
)

// TestQualityReport: a publication returns a short quality report and the
// release keeps the full one.
func TestQualityReport(t *testing.T) {
	s := up(t)
	admin := newUI(t)
	admin.login(adminEmail, adminPass)
	owner := &api{s: s, token: admin.issueToken("e2e-quality")}

	status, body := owner.json(http.MethodPost, "/sites", map[string]string{"title": "Checked"})
	must(t, status, http.StatusCreated, body, "create site")
	siteID := str(body, "id")
	status, body = owner.deploy(siteID, makeZip(t, site(map[string]string{
		"index.html": `<html><body><a href="/abuot">x</a><img src="missing.png"></body></html>`,
	})...))
	must(t, status, http.StatusCreated, body, "deploy")
	q, _ := body["quality"].(map[string]any)
	if q == nil || !strings.Contains(str(q, "summary"), "broken link") {
		t.Fatalf("deploy result quality: %v", body)
	}

	status, body = owner.json(http.MethodGet, "/sites/"+siteID+"/deploys/current", nil)
	must(t, status, http.StatusOK, body, "current release report")
	report, _ := body["quality"].(map[string]any)
	counts, _ := report["counts"].(map[string]any)
	if counts["broken_links"] != float64(1) || counts["missing_assets"] != float64(1) {
		t.Fatalf("report: %v", body)
	}
}
