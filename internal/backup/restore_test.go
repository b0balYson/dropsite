package backup

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

const (
	testSite   = "abcdefghij"
	testDeploy = "20261112-100000-abc123"
)

func symlinkHdr(name, target string) *tar.Header {
	return &tar.Header{Name: sitesPrefix + name, Linkname: target, Typeflag: tar.TypeSymlink}
}

func dirHdr(name string) *tar.Header {
	return &tar.Header{Name: sitesPrefix + name, Typeflag: tar.TypeDir, Mode: 0o755}
}

func writeReg(tw *tar.Writer, name, body string) {
	_ = tw.WriteHeader(&tar.Header{Name: sitesPrefix + name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))})
	_, _ = tw.Write([]byte(body))
}

// restoreInto restores data into a fresh state/sites pair below a private
// directory and returns that directory.
func restoreInto(t *testing.T, data []byte) (string, error) {
	t.Helper()
	dir := t.TempDir()
	state, sites := filepath.Join(dir, "state"), filepath.Join(dir, "sites")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Restore(context.Background(), bytes.NewReader(data), state, sites, true)
	return dir, err
}

// The reviewer's reproduction: a link that is lexically inside the sites tree
// becomes the parent of a deeper link, which together escape the staging dir.
func TestRestoreRejectsChainedLinkEscape(t *testing.T) {
	data := archiveOf(t, func(tw *tar.Writer) {
		_ = tw.WriteHeader(dirHdr("a/b/"))
		_ = tw.WriteHeader(symlinkHdr("a/b/c", "../.."))
		_ = tw.WriteHeader(dirHdr("a/b/c/d/e/"))
		_ = tw.WriteHeader(symlinkHdr("a/b/c/d/e/f", "../../../.."))
		writeReg(tw, "a/b/c/d/e/f/PWNED", "owned")
	})
	dir, err := restoreInto(t, data)
	if err == nil {
		t.Fatal("chained link escape must be rejected")
	}
	for _, p := range []string{filepath.Join(dir, "PWNED"), filepath.Join(dir, "state", "PWNED"), filepath.Join(dir, "d")} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Fatalf("%s was created outside staging", p)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "state", "state.db")); !os.IsNotExist(err) {
		t.Fatal("nothing may be installed from a rejected archive")
	}
}

func TestRestoreLinkWhitelist(t *testing.T) {
	cases := []struct {
		name, target string
		ok           bool
	}{
		{"sites/" + testSite + "/current", "releases/" + testDeploy, true},
		{"sites/" + testSite + "/current", "../../empty", true},
		{"by-host/a.pp.test", "../sites/" + testSite + "/current", true},
		{"by-host/xn--80ak6aa92e.pp.test", "../sites/" + testSite + "/current", true},

		{"a/b/c", "../..", false},
		{"sites/" + testSite + "/current", "releases/../../../x", false},
		{"sites/" + testSite + "/current", "releases/r1", false},
		{"sites/" + testSite + "/current", "../../empty/x", false},
		{"sites/" + testSite + "/current", "../" + testSite + "/releases/" + testDeploy, false},
		{"sites/" + testSite + "/other", "releases/" + testDeploy, false},
		{"sites/ABCDEFGHIJ/current", "releases/" + testDeploy, false},
		{"sites/" + testSite + "/releases/" + testDeploy, "../../../empty", false},
		{"by-host/a.pp.test", "../sites/" + testSite + "/releases", false},
		{"by-host/a.pp.test", "../sites/bad/current", false},
		{"by-host/a.pp.test", "/etc", false},
		{"by-host/10.0.0.1", "../sites/" + testSite + "/current", false},
		{"by-host/a..b", "../sites/" + testSite + "/current", false},
		{"by-host/sub/a.pp.test", "../../sites/" + testSite + "/current", false},
		{"empty", "sites", false},
	}
	for _, tc := range cases {
		t.Run(tc.name+"->"+tc.target, func(t *testing.T) {
			if got := allowedLink(tc.name, tc.target); got != tc.ok {
				t.Fatalf("allowedLink = %v, want %v", got, tc.ok)
			}
			_, err := restoreInto(t, archiveOf(t, func(tw *tar.Writer) { _ = tw.WriteHeader(symlinkHdr(tc.name, tc.target)) }))
			if (err == nil) != tc.ok {
				t.Fatalf("restore err = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

// Entries below a restored link must not be written through it.
func TestRestoreRejectsWritesThroughLinks(t *testing.T) {
	data := archiveOf(t, func(tw *tar.Writer) {
		_ = tw.WriteHeader(dirHdr("sites/" + testSite + "/releases/" + testDeploy + "/"))
		_ = tw.WriteHeader(symlinkHdr("sites/"+testSite+"/current", "releases/"+testDeploy))
		writeReg(tw, "sites/"+testSite+"/current/index.html", "via link")
	})
	dir, err := restoreInto(t, data)
	if err == nil {
		t.Fatal("writing through a link must be rejected")
	}
	if _, err := os.Stat(filepath.Join(dir, "sites", "sites", testSite, "releases", testDeploy, "index.html")); !os.IsNotExist(err) {
		t.Fatal("file was written through the link")
	}
}
