package config

import (
	"strings"
	"testing"
)

func TestFormSettings(t *testing.T) {
	c, err := Load(env(base()))
	if err != nil {
		t.Fatal(err)
	}
	if c.TelegramAPIURL != "https://api.telegram.org" || len(c.FormWebhookAllow) != 0 {
		t.Fatalf("defaults %q %v", c.TelegramAPIURL, c.FormWebhookAllow)
	}
	kv := base()
	kv["TELEGRAM_API_URL"] = "http://127.0.0.1:9000/"
	kv["FORM_WEBHOOK_ALLOW_CIDRS"] = "172.16.0.0/12, 10.1.2.3/8"
	c, err = Load(env(kv))
	if err != nil {
		t.Fatal(err)
	}
	if c.TelegramAPIURL != "http://127.0.0.1:9000" || len(c.FormWebhookAllow) != 2 || c.FormWebhookAllow[1].String() != "10.0.0.0/8" {
		t.Fatalf("overrides %q %v", c.TelegramAPIURL, c.FormWebhookAllow)
	}
	for k, v := range map[string]string{"TELEGRAM_API_URL": "ftp://x", "FORM_WEBHOOK_ALLOW_CIDRS": "nope"} {
		kv := base()
		kv[k] = v
		if _, err := Load(env(kv)); err == nil || !strings.Contains(err.Error(), k) {
			t.Errorf("%s=%s: %v", k, v, err)
		}
	}
}
