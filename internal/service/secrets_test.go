package service

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
)

// Credentials are assembled at run time so the repository holds no strings
// that look like real keys.
func fakeStripeKey() string { return "sk" + "_live_" + strings.Repeat("aB3dE5fG7hJ9kL2m", 3) }

func fakeJWT(role string) string {
	enc := base64.RawURLEncoding
	return enc.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." +
		enc.EncodeToString([]byte(`{"iss":"supabase","ref":"abc","role":"`+role+`"}`)) + "." +
		strings.Repeat("Q7W2E9R4T6Y1U8I3", 3)
}

func wantSecretsFound(t *testing.T, err error, found string, secret string) {
	t.Helper()
	wantCode(t, err, domain.CodeSecretsFound)
	de := domain.AsError(err)
	got, _ := de.Params["found"].(string)
	if !strings.Contains(got, found) {
		t.Fatalf("found = %q, want %q", got, found)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(got, secret) {
		t.Fatal("the error echoes the secret")
	}
}

func TestDeployRefusesSecrets(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	id, host, _ := e.published(u, shop)

	key := fakeStripeKey()
	_, err := e.svc.Deploy(ctx, u, id, zipOf(t, map[string]string{
		"index.html": "<html>new</html>",
		"js/pay.js":  "// pay\nconst stripe = '" + key + "';",
	}), domain.SourceUI)
	wantSecretsFound(t, err, "js/pay.js:2 (Stripe secret key)", key)
	if got, _ := e.served(host, "index.html"); !strings.Contains(got, "Price 100") {
		t.Fatalf("the current release changed: %q", got)
	}
	if ds, _, _ := e.svc.ListDeploys(ctx, u, id); len(ds) != 1 {
		t.Fatalf("releases: %d", len(ds))
	}
}

func TestDeployAcceptsPublicKeys(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	id, _, _ := e.published(u, shop)
	_, err := e.svc.Deploy(ctx, u, id, zipOf(t, map[string]string{
		"index.html": "<html><script src=js/db.js></script></html>",
		"js/db.js": "const anon = '" + fakeJWT("anon") + "';\nconst stripe = '" + "pk" + "_live_" +
			strings.Repeat("aB3dE5fG7hJ9kL2m", 3) + "';",
	}), domain.SourceUI)
	if err != nil {
		t.Fatal(err)
	}
}

func TestEditsAndFileDeploysRefuseSecrets(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u@pp.test")
	id, _, base := e.published(u, shop)
	jwt := fakeJWT("service_role")

	_, err := e.svc.UpdateSiteFiles(ctx, u, id, base, FileChanges{Put: []InlineFile{
		{Path: "js/db.js", Content: "\n\nexport const key = '" + jwt + "'"},
	}})
	wantSecretsFound(t, err, "js/db.js:3 (Supabase service_role key)", jwt)

	_, err = e.svc.DeployFiles(ctx, u, id, "", []InlineFile{
		{Path: "index.html", Content: "<html>" + jwt + "</html>"},
	})
	wantSecretsFound(t, err, "index.html:1", jwt)

	up, err := e.svc.BeginUpload(ctx, u, id, "", UploadPatch, base)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.UploadFiles(ctx, u, up.ID, []InlineFile{{Path: "a.js", Content: jwt}}, nil); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.CommitUpload(ctx, u, up.ID, nil)
	wantSecretsFound(t, err, "a.js:1", jwt)
}
