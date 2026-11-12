package storage

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/b0balYson/dropsite/internal/domain"
)

var (
	// ErrInvalidName is returned for a site ID, deploy ID or hostname that
	// does not match its strict format. Nothing on disk is touched.
	ErrInvalidName = errors.New("storage: invalid name")
	// ErrCurrentRelease is returned when deleting the release current points to.
	ErrCurrentRelease = errors.New("storage: release is current")
	// ErrNotFound is returned when a required release or staging directory is missing.
	ErrNotFound = errors.New("storage: not found")
	// ErrExists is returned when a release directory already exists.
	ErrExists = errors.New("storage: already exists")
)

var (
	siteIDPattern   = regexp.MustCompile(fmt.Sprintf(`^[a-z0-9]{%d}$`, domain.SiteIDLength))
	deployIDPattern = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[a-z0-9]{6}$`)
)

func validateSiteID(id string) error {
	if !siteIDPattern.MatchString(id) {
		return fmt.Errorf("%w: site id %q", ErrInvalidName, id)
	}
	return nil
}

func validateDeployID(id string) error {
	if !deployIDPattern.MatchString(id) {
		return fmt.Errorf("%w: deploy id %q", ErrInvalidName, id)
	}
	return nil
}

func validateSiteAndDeploy(siteID, deployID string) error {
	if err := validateSiteID(siteID); err != nil {
		return err
	}
	return validateDeployID(deployID)
}

// validateHostname accepts names valid for Caddy's @site matcher, except
// IP-like names (numeric last label): Caddy asks about the container IP on
// handshakes without SNI, so such an entry must never exist in by-host.
func validateHostname(h string) error {
	if !domain.IsValidHostname(h) || isNumericLabel(h[strings.LastIndexByte(h, '.')+1:]) {
		return fmt.Errorf("%w: hostname %q", ErrInvalidName, h)
	}
	return nil
}

func isNumericLabel(label string) bool {
	for i := range len(label) {
		if label[i] < '0' || label[i] > '9' {
			return false
		}
	}
	return label != ""
}
