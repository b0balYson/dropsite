package secrets

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixtures are assembled at run time so the repository itself never
// contains strings that look like real credentials.

const alnum = "aB3dE5fG7hJ9kL2mN4pQ6rS8tU0vW1xY"

// randomish repeats alnum to n characters: varied enough to pass the
// placeholder check, obviously not a real key.
func randomish(n int) string {
	return strings.Repeat(alnum, n/len(alnum)+1)[:n]
}

func upper(n int) string {
	return strings.Repeat("Q7W2E9R4T6Y1U8I3", n/16+1)[:n]
}

func jwt(payload string) string {
	enc := base64.RawURLEncoding
	return enc.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." +
		enc.EncodeToString([]byte(payload)) + "." + randomish(43)
}

func pemBlock(kind string) string {
	return "-----BEGIN " + kind + "PRIVATE KEY-----\n" + strings.Repeat("MIIEvQIBADANBgkqhkiG9w0BAQEFAASC", 3) +
		"\n-----END " + kind + "PRIVATE KEY-----"
}

func TestScanTextFindsSecrets(t *testing.T) {
	cases := []struct {
		name string
		text string
		kind string
	}{
		{"supabase service role", `const key = "` + jwt(`{"iss":"supabase","ref":"abc","role":"service_role","iat":1}`) + `";`, KindServiceRole},
		{"supabase secret key", "sb_" + "secret_" + randomish(32), KindSupabaseSecret},
		{"stripe live", "sk" + "_live_" + randomish(40), KindStripe},
		{"stripe restricted", "rk" + "_live_" + randomish(40), KindStripe},
		{"openai project", "sk" + "-proj-" + randomish(80), KindOpenAI},
		{"openai legacy", "sk-" + randomish(20) + "T3Blbk" + "FJ" + randomish(20), KindOpenAI},
		{"anthropic", "sk" + "-ant-api03-" + randomish(90), KindAnthropic},
		{"telegram", "token: 1234567890:" + "AA" + randomish(33), KindTelegram},
		{"aws", "AWS_KEY=" + "AK" + "IA" + upper(16), KindAWS},
		{"pem rsa", pemBlock("RSA "), KindPrivateKey},
		{"pem pkcs8 in json", `{"type":"service_account","private_key":"` +
			strings.ReplaceAll(pemBlock(""), "\n", `\n`) + `"}`, KindPrivateKey},
		{"github classic", "gh" + "p_" + randomish(36), KindGitHub},
		{"github fine grained", "github" + "_pat_" + randomish(82), KindGitHub},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ScanText("a.js", []byte("line one\n"+c.text+"\n"))
			if len(got) != 1 {
				t.Fatalf("want one finding, got %+v", got)
			}
			if got[0].Kind != c.kind || got[0].Line != 2 || got[0].Path != "a.js" || got[0].Label == "" {
				t.Fatalf("finding: %+v", got[0])
			}
		})
	}
}

func TestScanTextIgnoresPublicKeysAndPlaceholders(t *testing.T) {
	for name, text := range map[string]string{
		"supabase anon":        jwt(`{"iss":"supabase","ref":"abc","role":"anon","iat":1}`),
		"jwt without role":     jwt(`{"sub":"1","name":"x"}`),
		"broken jwt payload":   "eyJhbGciOiJIUzI1NiJ9.eyJ!!!notbase64.abcdefghijklmnopqrstu",
		"stripe publishable":   "pk" + "_live_" + randomish(40),
		"stripe test pk":       "pk" + "_test_" + randomish(40),
		"supabase publishable": "sb_" + "publishable_" + randomish(32),
		"firebase web key":     "AI" + "za" + randomish(35),
		"stripe placeholder":   "sk" + "_live_" + strings.Repeat("x", 40),
		"aws docs example":     "AK" + "IA" + "IOSFODNN7" + "EXAMPLE",
		"pem placeholder":      "-----BEGIN " + "PRIVATE KEY-----\n...\n-----END PRIVATE KEY-----",
		"short sk-":            "sk-" + "abc",
		"telegram placeholder": "123456789:" + "AA" + strings.Repeat("A", 33),
	} {
		if got := ScanText("a.js", []byte(text)); len(got) != 0 {
			t.Errorf("%s: false positive %+v", name, got)
		}
	}
}

func TestDescribeNeverEchoesTheSecret(t *testing.T) {
	key := "sk" + "_live_" + randomish(40)
	got := ScanText("js/pay.js", []byte(key))
	text := Describe(got, 20)
	if strings.Contains(text, key[8:]) || !strings.Contains(text, "js/pay.js:1 (Stripe secret key)") {
		t.Fatalf("describe: %q", text)
	}
}

func TestDescribeLimitsTheList(t *testing.T) {
	var fs []Finding
	for i := 1; i <= 25; i++ {
		fs = append(fs, Finding{Path: "a.js", Line: i, Kind: KindAWS, Label: "AWS access key"})
	}
	text := Describe(fs, 20)
	if strings.Count(text, "a.js:") != 20 || !strings.HasSuffix(text, "and 5 more") {
		t.Fatalf("describe: %q", text)
	}
}

func TestScanDirectory(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	key := "gh" + "p_" + randomish(36)
	write("index.html", []byte("<html>\n<script>\nconst t = '"+key+"'\n</script>"))
	write("js/app.js", []byte("const pub = '"+"pk"+"_live_"+randomish(40)+"';"))
	write("img/photo.png", append([]byte{0x89, 'P', 'N', 'G', 0, 0, 0}, []byte(key)...))
	write("keys/server.pem", []byte(pemBlock("EC ")))

	got, err := Scan(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("findings: %+v", got)
	}
	if got[0].Path != "index.html" || got[0].Line != 3 || got[0].Kind != KindGitHub {
		t.Errorf("first: %+v", got[0])
	}
	if got[1].Path != "keys/server.pem" || got[1].Line != 1 || got[1].Kind != KindPrivateKey {
		t.Errorf("second: %+v", got[1])
	}
}

func TestScanHonoursCancellation(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Scan(ctx, dir); err == nil {
		t.Fatal("a cancelled scan must fail")
	}
}
