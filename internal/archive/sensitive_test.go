package archive

import (
	"bytes"
	"context"
	"reflect"
	"sort"
	"testing"
)

func TestIsSensitive(t *testing.T) {
	for rel, want := range map[string]bool{
		".env": true, ".env.local": true, "sub/.ENV.production": true, ".npmrc": true, ".netrc": true,
		".git-credentials": true, ".ssh/config": true, "a/.aws/credentials": true, ".ssh": true,
		"certs/server.pem": true, "tls/site.KEY": true, "id_rsa": true, "keys/id_ed25519.pub": true,
		"index.html": false, ".well-known/security.txt": false, ".well-known/assetlinks.json": false,
		"key.js": false, "keyboard.png": false, "env.js": false, "docs/ssh.html": false, "": false,
	} {
		if got := IsSensitive(rel); got != want {
			t.Errorf("%q: %v", rel, got)
		}
	}
}

func TestFindSensitiveAndSkip(t *testing.T) {
	dir := mkTree(t, map[string]string{
		"index.html":               page,
		".env":                     "SECRET=1",
		".well-known/security.txt": "Contact: x",
		".ssh/id_rsa":              "key",
		"node_modules/.env":        "ignored anyway",
		"cert/site.pem":            "pem",
	})
	found, err := FindSensitive(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(found)
	if want := []string{".env", ".ssh/", "cert/site.pem"}; !reflect.DeepEqual(found, want) {
		t.Fatalf("found %v, want %v", found, want)
	}
	var buf bytes.Buffer
	res, err := WriteTarGzWith(context.Background(), dir, &buf, bigLimits, PackOptions{Skip: IsSensitive})
	if err != nil {
		t.Fatal(err)
	}
	src := writeFile(t, buf.Bytes())
	_, dest := sandbox(t)
	if _, err := Extract(context.Background(), src, dest, bigLimits, testNow); err != nil {
		t.Fatal(err)
	}
	got := readTree(t, dest)
	for _, p := range []string{".env", ".ssh/", ".ssh/id_rsa", "cert/site.pem"} {
		if _, ok := got[p]; ok {
			t.Errorf("%s was packed", p)
		}
	}
	if _, ok := got[".well-known/security.txt"]; !ok || res.Files != 2 {
		t.Fatalf("packed %v (%d files)", got, res.Files)
	}
	if _, err := FindSensitive(context.Background(), dir+"/missing"); err == nil {
		t.Fatal("missing directory accepted")
	}
}
