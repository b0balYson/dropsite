package service

import (
	"context"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/secrets"
)

// maxListedSecrets bounds the findings named in the refusal.
const maxListedSecrets = 20

// checkSecrets refuses a release whose text files contain secret keys. The
// error names files, lines and kinds of secrets, never their values.
func checkSecrets(ctx context.Context, dir string) error {
	found, err := secrets.Scan(ctx, dir)
	if err != nil {
		return err
	}
	if len(found) == 0 {
		return nil
	}
	return domain.E(domain.KindInvalid, domain.CodeSecretsFound,
		"the site contains secret keys that must never be public, nothing was published: remove them from "+
			"these files, put only public keys on a site and replace (rotate) the exposed keys").
		WithParams(map[string]any{"found": secrets.Describe(found, maxListedSecrets), "count": len(found)})
}
