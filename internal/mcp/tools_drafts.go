package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type discardDraftArgs struct {
	Site    string `json:"site" jsonschema:"site ID or slug"`
	Confirm string `json:"confirm" jsonschema:"the site's slug or ID, typed again after the user explicitly agreed to delete the draft"`
}

// registerDrafts adds publish_draft and discard_draft (o.Drafts); the
// backend must implement DraftBackend.
func (t tools) registerDrafts(s *sdk.Server, o Options) {
	if !o.Drafts {
		return
	}
	sdk.AddTool(s, &sdk.Tool{Name: "publish_draft", Annotations: writes("Publish draft", false, false),
		Description: "Make the site's draft live, after the user looked at the preview and said yes (\"publish\", " +
			"\"looks good\"). Instant, like a rollback: the version visitors saw until now stays available for " +
			"rollback. Reply \"It is live: <url>\". Site data (collections, form requests) is shared by the " +
			"draft and the live site, so nothing else needs to be copied."}, t.publishDraft)
	sdk.AddTool(s, &sdk.Tool{Name: "discard_draft", Annotations: writes("Delete draft", true, true),
		Description: "Delete the site's draft when the user does not want it; the site stays as it is and the " +
			"preview link stops showing the draft. Only after the user explicitly agreed: say which site's draft " +
			"will be deleted, wait for yes, then pass the site's slug or ID again in confirm."}, t.discardDraft)
}

func (t tools) draftBackend(ctx context.Context, req *sdk.CallToolRequest) (Backend, DraftBackend, error) {
	b, err := t.resolve(ctx, req)
	if err != nil {
		return nil, nil, err
	}
	db, ok := b.(DraftBackend)
	if !ok {
		return nil, nil, errNoDrafts
	}
	return b, db, nil
}

func (t tools) publishDraft(ctx context.Context, req *sdk.CallToolRequest, in siteArgs) (*sdk.CallToolResult, DeployResult, error) {
	_, db, err := t.draftBackend(ctx, req)
	if err != nil {
		return nil, DeployResult{}, err
	}
	r, err := db.PublishDraft(ctx, in.Site)
	return nil, r, err
}

func (t tools) discardDraft(ctx context.Context, req *sdk.CallToolRequest, in discardDraftArgs) (*sdk.CallToolResult, Site, error) {
	b, db, err := t.draftBackend(ctx, req)
	if err != nil {
		return nil, Site{}, err
	}
	s, err := b.GetSite(ctx, in.Site)
	if err != nil {
		return nil, Site{}, err
	}
	if !sameRef(in.Confirm, s) {
		return nil, Site{}, ErrDiscardConfirm
	}
	out, err := db.DiscardDraft(ctx, s.ID)
	return nil, out, err
}
