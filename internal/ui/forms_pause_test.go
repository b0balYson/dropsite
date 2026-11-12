package ui

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/forms"
	"github.com/b0balYson/dropsite/internal/testenv"
)

// downSender fails every webhook.
type downSender struct{ nopSender }

func (downSender) Webhook(context.Context, string, string, []byte) error {
	return forms.Temporary("webhook: HTTP 503")
}

func TestPausedChannelCanBeResumed(t *testing.T) {
	h := newHarness(t, testenv.WithFormSender(&downSender{}))
	alice := h.loginUser("alice@pp.test")
	v := h.createSite(alice, "Promo", "")
	ctx := context.Background()
	if _, err := h.env.Svc.AddFormChannel(ctx, alice, v.Site.ID, domain.ChannelWebhook, "https://down.example.com/"); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		submitTo(t, h, v.Site.ID, "name", "Ann")
		if _, err := h.env.Svc.DeliverDueForms(ctx); err != nil {
			t.Fatal(err)
		}
	}
	base := "/sites/" + v.Site.ID + "/forms"
	page := h.get(base)
	wantStatus(t, page, http.StatusOK)
	wantContains(t, page.body, "приостановлен", "Доставка приостановлена", "Возобновить", `name="resume" value="1"`)

	resume := h.submit(base, base+"/channels/1/toggle", url.Values{"enabled": {"1"}, "resume": {"1"}})
	wantStatus(t, resume, http.StatusSeeOther)
	after := h.follow(resume)
	wantContains(t, after.body, "Канал возобновлён")
	wantNotContains(t, after.body, "Доставка приостановлена")
}
