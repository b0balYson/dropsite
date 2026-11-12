package mcp

import (
	"context"
	"errors"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type referenceArgs struct {
	URL string `json:"url" jsonschema:"https:// address of the site the user likes"`
}

var errNoReferences = errors.New("this server cannot inspect reference sites; ask the user for a screenshot instead")

// registerReferences adds inspect_reference (o.Workflow); the backend must
// implement ReferenceBackend.
func (t tools) registerReferences(s *sdk.Server, o Options) {
	if !o.Workflow {
		return
	}
	// It reads a page on the internet, so it is read-only but open-world.
	ann := &sdk.ToolAnnotations{Title: "Inspect reference site", ReadOnlyHint: true, OpenWorldHint: boolPtr(true)}
	sdk.AddTool(s, &sdk.Tool{Name: "inspect_reference", Annotations: ann,
		Description: "Get a design brief of a site the user likes, when they give a link and say \"make it like " +
			"this\" or \"in this style\": palette with roles, CSS variables, fonts and size scale, spacing, corners, " +
			"shadows, light or dark, the order of sections and notable components (hero type, cards, gallery, " +
			"sticky header). It never returns the page's texts or images. If the server has screenshots, a " +
			"picture of the first screen is attached. Build an original site in a similar style, never a copy: " +
			"tell the user in one line that it will be in the spirit of the reference with their own texts and " +
			"photos. For a picture the user attached, you need no tool: see get_guide topic references."}, t.inspectReference)
}

func (t tools) inspectReference(ctx context.Context, req *sdk.CallToolRequest, in referenceArgs) (*sdk.CallToolResult, ReferenceResult, error) {
	b, err := t.resolve(ctx, req)
	if err != nil {
		return nil, ReferenceResult{}, err
	}
	rb, ok := b.(ReferenceBackend)
	if !ok {
		return nil, ReferenceResult{}, errNoReferences
	}
	r, err := rb.InspectReference(ctx, in.URL)
	if err != nil {
		return nil, ReferenceResult{}, err
	}
	text := r.Summary + " " + r.Advice
	if len(r.PNG) > 0 {
		return imageResult(r.PNG, "First screen of the reference, without its pictures and web fonts. "+text), r, nil
	}
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{
		Text: text + " No screenshot is available on this server; if the look matters, ask the user for one.",
	}}}, r, nil
}
