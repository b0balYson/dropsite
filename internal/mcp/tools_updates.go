package mcp

import (
	"context"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Version facts (change admin-update-check). The tool only reads: no MCP
// tool can start an update, so content an assistant reads (a page, a form
// submission) can never make the server update itself. Updating is done by
// an administrator in the web interface or on the server.

// UpdateFacts is what a backend reports about the version.
type UpdateFacts struct {
	Current      string
	Latest       string
	Available    bool
	ReleaseURL   string
	CheckedAt    string
	CheckEnabled bool
	IsAdmin      bool
	// HelperPresent and PanelURL are known for administrators only.
	HelperPresent bool
	PanelURL      string
}

// UpdatesBackend reports the version; every Cabinet backend implements it.
type UpdatesBackend interface {
	CheckUpdates(ctx context.Context, refresh bool) (UpdateFacts, error)
}

// UpdateCheck is the result of check_updates.
type UpdateCheck struct {
	CurrentVersion  string `json:"current_version" jsonschema:"version of the dropsite server: X.Y.Z, or dev+<commit> for a development build"`
	LatestVersion   string `json:"latest_version,omitempty" jsonschema:"newest stable release found by the last check"`
	UpdateAvailable bool   `json:"update_available"`
	ReleaseNotesURL string `json:"release_notes_url,omitempty" jsonschema:"what's new in the latest release: give this link to the user"`
	CheckedAt       string `json:"checked_at,omitempty" jsonschema:"when the server last asked for the latest release"`
	CheckEnabled    bool   `json:"check_enabled" jsonschema:"false when the administrator turned the check off or the server is a development build"`
	IsAdmin         bool   `json:"is_admin" jsonschema:"true when the user is an administrator of this server"`
	HowToUpdate     string `json:"how_to_update" jsonschema:"what the user does to update: an administrator in the web interface or on the server; others ask their administrator"`
	UpdatePanelURL  string `json:"update_panel_url,omitempty" jsonschema:"administrators: the Updates section of the web interface settings"`
	UpdateCommand   string `json:"update_command,omitempty" jsonschema:"administrators without the update button: the command to run on the server"`
	Summary         string `json:"summary" jsonschema:"short summary to show the user"`
}

type checkUpdatesArgs struct {
	Refresh bool `json:"refresh,omitempty" jsonschema:"administrators only: ask GitHub for the latest release now instead of using the last check (rate limited); ignored for other users"`
}

// manualUpdateCommand is how an administrator updates without the button.
const manualUpdateCommand = "ssh <server> dropsite-update"

func registerUpdateTools(t tools, s *sdk.Server) {
	sdk.AddTool(s, &sdk.Tool{Name: "check_updates",
		Annotations: &sdk.ToolAnnotations{Title: "Check for Dropsite updates", ReadOnlyHint: true, OpenWorldHint: boolPtr(true)},
		Description: "Show the Dropsite server's version, whether a newer release exists, its what's-new link and how " +
			"to update. Read-only: it never updates anything. Call it when the user asks about the version or " +
			"updates. An administrator updates in the web interface (Settings, Updates) or on the server; other " +
			"users ask their administrator. Mention an available update to an administrator once per chat, " +
			"never block their task."},
		cabinet(t, func(ctx context.Context, c Cabinet, in checkUpdatesArgs) (UpdateCheck, error) {
			f, err := c.CheckUpdates(ctx, in.Refresh)
			if err != nil {
				return UpdateCheck{}, err
			}
			return NewUpdateCheck(f), nil
		}))
}

// NewUpdateCheck builds the tool result, the same on both servers.
func NewUpdateCheck(f UpdateFacts) UpdateCheck {
	out := UpdateCheck{
		CurrentVersion: f.Current, LatestVersion: f.Latest, UpdateAvailable: f.Available,
		ReleaseNotesURL: f.ReleaseURL, CheckedAt: f.CheckedAt, CheckEnabled: f.CheckEnabled, IsAdmin: f.IsAdmin,
	}
	switch {
	case !f.IsAdmin:
		out.HowToUpdate = "Ask your Dropsite administrator: only an administrator can update the server."
	case f.HelperPresent && f.PanelURL != "":
		out.UpdatePanelURL = f.PanelURL
		out.HowToUpdate = "Open " + f.PanelURL + " (Settings, Updates) and press Update; the server backs up first " +
			"and the sites keep working."
	default:
		out.UpdateCommand = manualUpdateCommand
		out.HowToUpdate = "Run on the server: " + manualUpdateCommand + " (it backs up first; dropsite-rollback undoes it)."
	}
	out.Summary = updateSummary(out)
	return out
}

func updateSummary(u UpdateCheck) string {
	switch {
	case u.UpdateAvailable:
		s := fmt.Sprintf("Dropsite %s is available (this server runs %s). What's new: %s.", u.LatestVersion,
			u.CurrentVersion, u.ReleaseNotesURL)
		return s + " " + u.HowToUpdate
	case !u.CheckEnabled:
		return fmt.Sprintf("This server runs Dropsite %s; it does not check for new releases.", u.CurrentVersion)
	case u.LatestVersion == "":
		return fmt.Sprintf("This server runs Dropsite %s; it has not checked for new releases yet.", u.CurrentVersion)
	}
	return fmt.Sprintf("This server runs Dropsite %s, the latest release.", u.CurrentVersion)
}
