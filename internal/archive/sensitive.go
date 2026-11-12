package archive

import (
	"context"
	"io/fs"
	"os"
	"path"
	"strings"
)

// sensitiveDirs are directories that hold credentials (any depth).
var sensitiveDirs = map[string]bool{".ssh": true, ".aws": true, ".gnupg": true}

// sensitiveFiles are credential files matched by exact name (any depth).
var sensitiveFiles = map[string]bool{".npmrc": true, ".netrc": true, ".git-credentials": true, ".pypirc": true}

// sensitivePrefixes are private SSH key names (id_rsa, id_rsa.pub, ...).
var sensitivePrefixes = []string{"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519"}

// sensitiveExts are private key and certificate bundle extensions.
var sensitiveExts = []string{".pem", ".key"}

// IsSensitive reports whether a slash-separated relative path looks like a
// secret that must not be published by accident: .env files, package
// manager and git credentials, SSH and cloud credentials and private keys.
// Other dotfiles (e.g. .well-known/) are ordinary site content.
func IsSensitive(rel string) bool {
	rel = strings.Trim(rel, "/")
	if rel == "" {
		return false
	}
	segs := strings.Split(rel, "/")
	for _, seg := range segs[:len(segs)-1] {
		if sensitiveDirs[strings.ToLower(seg)] {
			return true
		}
	}
	return sensitiveName(strings.ToLower(segs[len(segs)-1]))
}

func sensitiveName(name string) bool {
	if sensitiveDirs[name] || sensitiveFiles[name] || strings.HasPrefix(name, ".env") {
		return true
	}
	for _, p := range sensitivePrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	ext := path.Ext(name)
	for _, e := range sensitiveExts {
		if ext == e {
			return true
		}
	}
	return false
}

// FindSensitive lists the paths under dir that IsSensitive matches, skipping
// what IsIgnored skips. A sensitive directory is listed once, with a
// trailing slash, and not descended into.
func FindSensitive(ctx context.Context, dir string) ([]string, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, internalErr("open directory", err)
	}
	defer func() { _ = root.Close() }()
	var found []string
	err = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return internalErr("walk directory", err)
		}
		if err := ctxErr(ctx); err != nil {
			return err
		}
		if p == "." || IsIgnored(p) {
			if p != "." && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !IsSensitive(p) {
			return nil
		}
		if d.IsDir() {
			found = append(found, p+"/")
			return fs.SkipDir
		}
		found = append(found, p)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return found, nil
}
