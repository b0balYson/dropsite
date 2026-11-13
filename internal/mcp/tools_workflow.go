package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type cloneArgs struct {
	Site      string `json:"site" jsonschema:"site ID or slug to copy"`
	Title     string `json:"title,omitempty" jsonschema:"name of the new site; default: the old name with (copy)"`
	Slug      string `json:"slug,omitempty" jsonschema:"optional memorable address of the new site (a-z, 0-9, hyphens)"`
	Workspace string `json:"workspace,omitempty" jsonschema:"optional workspace_id from list_workspaces: create the site in that shared workspace"`
}

type listTemplatesArgs struct {
	Lang string `json:"lang,omitempty" jsonschema:"ru (default) or en: language of the titles and descriptions"`
}

type fromTemplateArgs struct {
	Template  string `json:"template" jsonschema:"template name from list_templates, e.g. cafe"`
	Title     string `json:"title,omitempty" jsonschema:"site name shown on the page and in the interface"`
	Slug      string `json:"slug,omitempty" jsonschema:"optional memorable address (a-z, 0-9, hyphens)"`
	Lang      string `json:"lang,omitempty" jsonschema:"ru (default) or en: language of the template texts"`
	Workspace string `json:"workspace,omitempty" jsonschema:"optional workspace_id from list_workspaces: create the site in that shared workspace"`
}

type scheduleArgs struct {
	PublishAt string `json:"publish_at" jsonschema:"when to go live: RFC 3339 with the user's time zone, e.g. 2026-12-01T09:00:00+03:00"`
	UploadID  string `json:"upload_id,omitempty" jsonschema:"an open upload from begin_upload: it becomes a prepared version that goes live at publish_at"`
	Site      string `json:"site,omitempty" jsonschema:"site ID or slug, with deploy_id"`
	DeployID  string `json:"deploy_id,omitempty" jsonschema:"an existing version of the site that is not being served (from list_deploys)"`
}

type listSchedulesArgs struct {
	Site string `json:"site,omitempty" jsonschema:"site ID or slug; omit for every site of the user"`
}

type cancelScheduleArgs struct {
	ScheduleID string `json:"schedule_id" jsonschema:"from list_scheduled_publishes or schedule_publish"`
}

type screenshotArgs struct {
	Site     string `json:"site" jsonschema:"site ID or slug"`
	DeployID string `json:"deploy_id,omitempty" jsonschema:"version to show; empty means the one being served"`
	Device   string `json:"device,omitempty" jsonschema:"desktop (default, 1280x800) or mobile (390x844)"`
}

type compareArgs struct {
	Site         string `json:"site" jsonschema:"site ID or slug"`
	FromDeployID string `json:"from_deploy_id" jsonschema:"the earlier version (from list_deploys)"`
	ToDeployID   string `json:"to_deploy_id,omitempty" jsonschema:"the later version; empty means the one being served"`
	Device       string `json:"device,omitempty" jsonschema:"desktop (default) or mobile"`
}

type templatesOut struct {
	Templates []Template `json:"templates"`
}

type schedulesOut struct {
	Schedules []Schedule `json:"schedules" jsonschema:"pending and recently finished publishes, by time"`
}

var (
	errNoWorkflow    = errors.New("this server cannot copy sites, use templates, schedule publishes or take screenshots")
	errScheduleInput = errors.New("pass publish_at and either upload_id, or site with deploy_id [schedule_invalid]")
)

const rendererAdvice = " If screenshots are not available on this server, say so briefly and give the user the link to look at."

// registerWorkflow adds the tools for copies, templates, scheduled
// publishes and screenshots (o.Workflow); the backend must implement
// WorkflowBackend.
func (t tools) registerWorkflow(s *sdk.Server, o Options) {
	if !o.Workflow {
		return
	}
	sdk.AddTool(s, &sdk.Tool{Name: "clone_site", Annotations: writes("Copy site", false, false),
		Description: "Create a new site from the version being served of an existing site, e.g. \"make the same " +
			"site for our second shop\". Only the files are copied: forms and where requests go, site data, own " +
			"domains, the address, password, lifetime and analytics counters are not; tell the user that in one " +
			"line. To change the existing site instead, use update_site_files. Reply with the new link."}, t.cloneSite)
	sdk.AddTool(s, &sdk.Tool{Name: "list_templates", Annotations: readOnly("List templates"),
		Description: "List the built-in starter sites (cafe, services, event, portfolio, link page). Only when the " +
			"user asks for a template or ready designs (\"quickly, from a template\") or wants a site online within a " +
			"minute: by default build every site with its own design, because templates make sites look alike."}, t.listTemplates)
	sdk.AddTool(s, &sdk.Tool{Name: "create_site_from_template", Annotations: writes("Create site from template", false, false),
		Description: "Create and publish a new site from the starter site the user chose in list_templates, with " +
			"their site name, then replace every sample text, price and contact with the user's own (list_site_files, " +
			"read_site_files, update_site_files). Reply with the link, and say in one line that requests from the " +
			"site's form appear under the site's Requests and that connecting Telegram or e-mail (add_channel) sends " +
			"them on."}, t.createFromTemplate)
	sdk.AddTool(s, &sdk.Tool{Name: "schedule_publish", Annotations: writes("Schedule publish", false, false),
		Description: "Make a version go live at a given time, e.g. \"publish the sale on Monday at 9\". Either pass " +
			"upload_id of an upload (begin_upload, upload_files) instead of calling commit_upload: it becomes a " +
			"prepared version that visitors do not see yet; or pass site and deploy_id of an existing version that " +
			"is not being served. publish_at needs the user's time zone; ask if unsure. The site keeps showing " +
			"the current version until then. Tell the user the date and time in their words and, if returned, the " +
			"preview link to check the new version. Undo with cancel_scheduled_publish. To show a change first " +
			"and publish it when the user says yes, use a draft instead (draft true, then publish_draft)."}, t.schedulePublish)
	sdk.AddTool(s, &sdk.Tool{Name: "list_scheduled_publishes", Annotations: readOnly("List scheduled publishes"),
		Description: "List versions waiting to go live and those that went live, failed or were cancelled in the " +
			"last 7 days, for one site or all sites (\"what is scheduled?\", \"did the sale go live?\")."}, t.listSchedules)
	sdk.AddTool(s, &sdk.Tool{Name: "cancel_scheduled_publish", Annotations: writes("Cancel scheduled publish", false, true),
		Description: "Cancel a publish that is still waiting, when the user changed their mind. The site keeps " +
			"its current version; the prepared version stays in the version list."}, t.cancelSchedule)
	sdk.AddTool(s, &sdk.Tool{Name: "screenshot_site", Annotations: readOnly("Screenshot site"),
		InputSchema: withEnums[screenshotArgs](map[string][]string{"device": {"", "desktop", "mobile"}}),
		Description: "Get a picture of the first screen of a site version, as a phone (mobile) or a computer " +
			"(desktop) shows it. Use it to check your own work before telling the user it is ready: layout, " +
			"overflowing text, contrast, missing images. Pages are shot from the site's own files, without " +
			"other servers." + rendererAdvice}, t.screenshot)
	sdk.AddTool(s, &sdk.Tool{Name: "compare_versions", Annotations: readOnly("Compare versions"),
		InputSchema: withEnums[compareArgs](map[string][]string{"device": {"", "desktop", "mobile"}}),
		Description: "Compare the first screen of two versions of a site: returns a picture \"before | after\" " +
			"with the changes highlighted and the share of changed pixels. Use it to show the user what changed " +
			"or to make sure an edit touched only what was asked." + rendererAdvice}, t.compareVersions)
}

func (t tools) workflowBackend(ctx context.Context, req *sdk.CallToolRequest) (WorkflowBackend, error) {
	b, err := t.resolve(ctx, req)
	if err != nil {
		return nil, err
	}
	wb, ok := b.(WorkflowBackend)
	if !ok {
		return nil, errNoWorkflow
	}
	return wb, nil
}

func (t tools) cloneSite(ctx context.Context, req *sdk.CallToolRequest, in cloneArgs) (*sdk.CallToolResult, DeployResult, error) {
	b, err := t.workflowBackend(ctx, req)
	if err != nil {
		return nil, DeployResult{}, err
	}
	if in.Workspace != "" {
		wb, err := t.inWorkspace(ctx, req)
		if err != nil {
			return nil, DeployResult{}, err
		}
		r, err := wb.CloneSiteIn(ctx, in.Workspace, in.Site, in.Title, in.Slug)
		return nil, r, err
	}
	r, err := b.CloneSite(ctx, in.Site, in.Title, in.Slug)
	return nil, r, err
}

func (t tools) listTemplates(ctx context.Context, req *sdk.CallToolRequest, in listTemplatesArgs) (*sdk.CallToolResult, templatesOut, error) {
	b, err := t.workflowBackend(ctx, req)
	if err != nil {
		return nil, templatesOut{}, err
	}
	ts, err := b.ListTemplates(ctx, in.Lang)
	if ts == nil {
		ts = []Template{}
	}
	return nil, templatesOut{Templates: ts}, err
}

func (t tools) createFromTemplate(ctx context.Context, req *sdk.CallToolRequest, in fromTemplateArgs) (*sdk.CallToolResult, DeployResult, error) {
	b, err := t.workflowBackend(ctx, req)
	if err != nil {
		return nil, DeployResult{}, err
	}
	if in.Workspace != "" {
		wb, err := t.inWorkspace(ctx, req)
		if err != nil {
			return nil, DeployResult{}, err
		}
		r, err := wb.CreateSiteFromTemplateIn(ctx, in.Workspace, in.Template, in.Title, in.Slug, in.Lang)
		return nil, r, err
	}
	r, err := b.CreateSiteFromTemplate(ctx, in.Template, in.Title, in.Slug, in.Lang)
	return nil, r, err
}

func (t tools) schedulePublish(ctx context.Context, req *sdk.CallToolRequest, in scheduleArgs) (*sdk.CallToolResult, Schedule, error) {
	b, err := t.workflowBackend(ctx, req)
	if err != nil {
		return nil, Schedule{}, err
	}
	byUpload := strings.TrimSpace(in.UploadID) != ""
	byRelease := strings.TrimSpace(in.Site) != "" || strings.TrimSpace(in.DeployID) != ""
	if byUpload == byRelease || strings.TrimSpace(in.PublishAt) == "" {
		return nil, Schedule{}, errScheduleInput
	}
	r, err := b.SchedulePublish(ctx, ScheduleRequest{Site: in.Site, DeployID: in.DeployID, UploadID: in.UploadID, PublishAt: in.PublishAt})
	return nil, r, err
}

func (t tools) listSchedules(ctx context.Context, req *sdk.CallToolRequest, in listSchedulesArgs) (*sdk.CallToolResult, schedulesOut, error) {
	b, err := t.workflowBackend(ctx, req)
	if err != nil {
		return nil, schedulesOut{}, err
	}
	rows, err := b.ListSchedules(ctx, in.Site)
	if rows == nil {
		rows = []Schedule{}
	}
	return nil, schedulesOut{Schedules: rows}, err
}

func (t tools) cancelSchedule(ctx context.Context, req *sdk.CallToolRequest, in cancelScheduleArgs) (*sdk.CallToolResult, Schedule, error) {
	b, err := t.workflowBackend(ctx, req)
	if err != nil {
		return nil, Schedule{}, err
	}
	r, err := b.CancelSchedule(ctx, in.ScheduleID)
	return nil, r, err
}

func imageResult(png []byte, text string) *sdk.CallToolResult {
	return &sdk.CallToolResult{Content: []sdk.Content{
		&sdk.ImageContent{Data: png, MIMEType: "image/png"},
		&sdk.TextContent{Text: text},
	}}
}

func (t tools) screenshot(ctx context.Context, req *sdk.CallToolRequest, in screenshotArgs) (*sdk.CallToolResult, Screenshot, error) {
	b, err := t.workflowBackend(ctx, req)
	if err != nil {
		return nil, Screenshot{}, err
	}
	shot, err := b.Screenshot(ctx, in.Site, in.DeployID, in.Device)
	if err != nil {
		return nil, Screenshot{}, err
	}
	text := fmt.Sprintf("First screen of version %s on %s (%dx%d).", shot.DeployID, shot.Device, shot.Width, shot.Height)
	return imageResult(shot.PNG, text), shot, nil
}

func (t tools) compareVersions(ctx context.Context, req *sdk.CallToolRequest, in compareArgs) (*sdk.CallToolResult, VisualDiff, error) {
	b, err := t.workflowBackend(ctx, req)
	if err != nil {
		return nil, VisualDiff{}, err
	}
	d, err := b.CompareVersions(ctx, in.Site, in.FromDeployID, in.ToDeployID, in.Device)
	if err != nil {
		return nil, VisualDiff{}, err
	}
	d.Summary = DiffSummary(d.ChangedPercent)
	return imageResult(d.PNG, "Left: "+d.FromDeployID+", right: "+d.ToDeployID+". "+d.Summary), d, nil
}
