package main

import (
	"context"

	"github.com/b0balYson/dropsite/internal/mcp"
)

// Reference inspection of the local server, through the API.

var _ mcp.ReferenceBackend = localBackend{}

func (b localBackend) InspectReference(ctx context.Context, url string) (mcp.ReferenceResult, error) {
	r, err := b.c.InspectReference(ctx, url)
	if err != nil {
		return mcp.ReferenceResult{}, toolError(err)
	}
	png, err := r.PNG()
	if err != nil {
		return mcp.ReferenceResult{}, toolError(err)
	}
	return mcp.NewReferenceResult(r.Brief, r.Screenshot.Status, png), nil
}
