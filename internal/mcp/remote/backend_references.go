package remote

import (
	"context"

	"github.com/b0balYson/dropsite/internal/mcp"
)

var _ mcp.ReferenceBackend = backend{}

func (b backend) InspectReference(ctx context.Context, url string) (mcp.ReferenceResult, error) {
	r, err := b.svc.InspectReference(ctx, b.actor, url)
	if err != nil {
		return mcp.ReferenceResult{}, b.toolError(err)
	}
	return mcp.NewReferenceResult(r.Brief, r.Shot.Status, r.Shot.PNG), nil
}
