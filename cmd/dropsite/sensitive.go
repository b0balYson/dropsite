package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/b0balYson/dropsite/internal/archive"
)

// sensitiveMode decides what happens to secret-looking files (.env, keys,
// credentials) in a folder being published.
type sensitiveMode int

const (
	// sensitiveRefuse stops before anything is uploaded and lists them.
	sensitiveRefuse sensitiveMode = iota
	// sensitiveSkip publishes the folder without them.
	sensitiveSkip
	// sensitiveInclude publishes them (CLI only, explicit flag).
	sensitiveInclude
)

// maxListedSensitive bounds the list printed in the refusal.
const maxListedSensitive = 10

// checkSensitive looks for secret-looking files in o.dir and returns the
// skip rule for the packer, or a refusal that lists them.
func (a *app) checkSensitive(ctx context.Context, o deployOpts) (func(string) bool, error) {
	if o.sensitive == sensitiveInclude {
		return nil, nil
	}
	found, err := archive.FindSensitive(ctx, o.dir)
	if err != nil {
		return nil, fmt.Errorf("check %s for secret files: %w", o.dir, err)
	}
	if len(found) == 0 {
		return nil, nil
	}
	listed := found
	if len(listed) > maxListedSensitive {
		listed = append(listed[:maxListedSensitive:maxListedSensitive], fmt.Sprintf("and %d more", len(found)-maxListedSensitive))
	}
	if o.sensitive == sensitiveSkip {
		fmt.Fprintf(a.stderr, "Not publishing %d secret-looking file(s): %s\n", len(found), strings.Join(listed, ", "))
		return archive.IsSensitive, nil
	}
	hints := []string{"Publish only the build output folder (dist, build, public), not the project folder."}
	hints = append(hints, o.sensitiveHints...)
	return nil, withHint(fmt.Errorf("%s contains files that look like secrets, nothing was published: %s",
		o.dir, strings.Join(listed, ", ")), hints...)
}

// cliSensitiveHints explain the deploy flags.
var cliSensitiveHints = []string{
	"Pass --skip-sensitive to publish without them, or --include-sensitive if they are really meant to be public.",
}

// mcpSensitiveHints explain the deploy_folder option.
var mcpSensitiveHints = []string{
	"Call deploy_folder again with skip_sensitive_files=true to publish without them.",
}

// errBroadFolder refuses folders that are far too broad to be a site.
var errBroadFolder = errors.New("refusing to publish this folder: it is the home folder, a folder directly inside it or a " +
	"file system root, which hold personal files and secrets. Pass the site's build output folder " +
	"(the one with index.html, e.g. ~/projects/site/dist)")

// checkFolderScope refuses the home folder, its direct children and file
// system roots (symbolic links resolved) for publication by an assistant.
func (a *app) checkFolderScope(dir string) error {
	real := resolvedPath(dir)
	if filepath.Dir(real) == real {
		return errBroadFolder
	}
	home, err := a.homeDir()
	if err != nil || home == "" {
		return nil //nolint:nilerr // without a home folder only the root check applies
	}
	home = resolvedPath(home)
	if real == home || filepath.Dir(real) == home {
		return errBroadFolder
	}
	return nil
}

func resolvedPath(p string) string {
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	return p
}
