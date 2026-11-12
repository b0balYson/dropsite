package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/b0balYson/dropsite/internal/brand"
)

const projectFilePerm = 0o644

// project links a directory to a site; it lives in brand.ProjectFile.
type project struct {
	Server string `json:"server"`
	SiteID string `json:"site_id"`
}

func projectPath(dir string) string { return filepath.Join(dir, brand.ProjectFile) }

// readProject returns the link, or ok=false when the file does not exist.
func readProject(dir string) (project, bool, error) {
	path := projectPath(dir)
	b, err := os.ReadFile(path) //nolint:gosec // the user's own project directory
	if errors.Is(err, fs.ErrNotExist) {
		return project{}, false, nil
	}
	if err != nil {
		return project{}, false, fmt.Errorf("read %s: %w", path, err)
	}
	var p project
	if err := json.Unmarshal(b, &p); err != nil || p.SiteID == "" || p.Server == "" {
		return project{}, false, withHint(fmt.Errorf("%s is not a valid %s project file", path, brand.Name),
			`Expected {"server": "...", "site_id": "..."}. Fix or delete it, or pass --site.`)
	}
	return p, true, nil
}

func writeProject(dir string, p project) error {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("encode project file: %w", err)
	}
	if err := writeFileAtomic(projectPath(dir), append(b, '\n'), projectFilePerm); err != nil {
		return fmt.Errorf("write %s: %w", projectPath(dir), err)
	}
	return nil
}
