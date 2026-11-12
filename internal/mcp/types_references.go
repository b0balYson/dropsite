package mcp

import (
	"context"

	"github.com/b0balYson/dropsite/internal/designref"
)

// ReferenceResult is the result of inspect_reference.
type ReferenceResult struct {
	designref.Brief
	Screenshot string `json:"screenshot" jsonschema:"included (the picture is attached) or unavailable"`
	Advice     string `json:"advice" jsonschema:"how to use the brief; follow it"`
	// PNG is the first screen when the renderer took it.
	PNG []byte `json:"-"`
}

// ReferenceBackend inspects reference pages. The tool fails when the
// backend does not implement it.
type ReferenceBackend interface {
	InspectReference(ctx context.Context, url string) (ReferenceResult, error)
}

// ReferenceAdvice is returned with every brief.
const ReferenceAdvice = "Use this as inspiration for an original site: take the palette, type pairing, density, " +
	"composition and mood, adapted to the user's business. Never copy the reference's texts, photos, logos, " +
	"trademarks or its exact layout; write your own content. Tell the user the new site is in the spirit of the " +
	"reference, not a copy. More: get_guide topic references."

// NewReferenceResult builds the tool result; both servers use it.
func NewReferenceResult(b designref.Brief, status string, png []byte) ReferenceResult {
	if status == "" {
		status = designref.ShotUnavailable
	}
	return ReferenceResult{Brief: b, Screenshot: status, Advice: ReferenceAdvice, PNG: png}
}
