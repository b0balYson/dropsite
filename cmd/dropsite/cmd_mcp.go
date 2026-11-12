package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/b0balYson/dropsite/internal/brand"
	"github.com/b0balYson/dropsite/internal/client"
	"github.com/b0balYson/dropsite/internal/mcp"
)

const localInstructions = "Publish static websites from folders on this computer to the user's " + brand.DisplayName +
	" server and manage them. To publish, call deploy_folder with the absolute path of the site's build output " +
	"folder that contains index.html (build the project first if it has a build step; never pass a project, " +
	"home or system folder: everything in the folder becomes public); it returns the link to give to the user. " +
	"Never delete a site unless the user explicitly asked and confirmed."

var errMCPNotConnected = errors.New(brand.Name + " is not connected on this computer: ask the user to open the " +
	"Connect AI page of the " + brand.Name + " web interface, press Connect and run the command shown there, then retry")

func cmdMCP(ctx context.Context, a *app, args []string) error {
	fs := a.newFlagSet("mcp", "",
		"Serve the "+brand.Name+" tools to a desktop AI client (Claude Desktop) over stdio\n"+
			"(Model Context Protocol). It uses the saved login; 'setup' configures Claude Desktop.")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if err := wantArgs(fs, pos, 0, 0); err != nil {
		return err
	}
	server := a.mcpServer()
	return server.Run(ctx, &sdk.IOTransport{Reader: nopCloser{a.stdin}, Writer: nopWriteCloser{a.stdout}})
}

// mcpServer builds the local server; the login is read on every call, so a
// later 'dropsite login' takes effect without restarting the client.
func (a *app) mcpServer() *sdk.Server {
	resolve := func(context.Context, *sdk.CallToolRequest) (mcp.Backend, error) {
		c, err := a.connect()
		if err != nil {
			return nil, errMCPNotConnected
		}
		return localBackend{a: a, c: c}, nil
	}
	return mcp.NewServer(brand.Version, resolve, mcp.Options{Folder: true, Instructions: localInstructions})
}

type nopCloser struct{ io.Reader }

func (nopCloser) Close() error { return nil }

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// localBackend runs the tools through the HTTP API with the saved login.
type localBackend struct {
	a *app
	c *client.Client
}

func stampTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func fromDeploy(d client.Deploy) mcp.Deploy {
	return mcp.Deploy{
		ID: d.ID, Source: d.Source, SizeBytes: d.SizeBytes, FileCount: d.FileCount, Status: d.Status,
		CreatedAt: stampTime(&d.CreatedAt), Current: d.Current, PreviewURL: deref(d.PreviewURL),
	}
}

func fromSite(s client.Site) mcp.Site {
	out := mcp.Site{ID: s.ID, Title: s.Title, URL: s.URL, URLs: s.URLs, Slug: deref(s.Slug), UpdatedAt: stampTime(&s.UpdatedAt)}
	if s.CurrentDeploy != nil {
		d := fromDeploy(*s.CurrentDeploy)
		out.CurrentDeploy = &d
	}
	return out
}

func fromResult(r *client.DeployResult) mcp.DeployResult {
	return mcp.DeployResult{SiteID: r.Site.ID, DeployID: r.DeployID, URL: r.URL, URLs: r.URLs}
}

// toolError turns CLI errors into one actionable line for the model.
func toolError(err error) error {
	if err == nil {
		return nil
	}
	var ae *client.APIError
	if errors.As(err, &ae) {
		switch {
		case ae.IsUnauthorized():
			return errMCPNotConnected
		case ae.IsNotFound():
			return errors.New("not found: no such site or release among this user's sites; call list_sites (or list_deploys) to see valid IDs and slugs")
		}
	}
	msg, hints := describeError(err)
	if len(hints) > 0 {
		msg += ". " + strings.Join(hints, " ")
	}
	return errors.New(msg)
}

func (b localBackend) ListSites(ctx context.Context) ([]mcp.Site, error) {
	l, err := b.c.ListSites(ctx, client.ListOptions{Limit: 200})
	if err != nil {
		return nil, toolError(err)
	}
	out := make([]mcp.Site, 0, len(l.Items))
	for _, s := range l.Items {
		out = append(out, mcp.Site{ID: s.ID, Title: s.Title, URL: s.URL, URLs: s.URLs, Slug: deref(s.Slug),
			LastDeployAt: stampTime(s.LastDeployAt), UpdatedAt: stampTime(&s.UpdatedAt)})
	}
	return out, nil
}

func (b localBackend) CreateSite(ctx context.Context, title, slug string) (mcp.Site, error) {
	s, err := b.c.CreateSite(ctx, client.CreateSiteRequest{Title: title, Slug: slug})
	if err != nil {
		return mcp.Site{}, toolError(err)
	}
	return fromSite(*s), nil
}

func (b localBackend) GetSite(ctx context.Context, ref string) (mcp.Site, error) {
	s, err := b.c.GetSite(ctx, ref)
	if err != nil {
		return mcp.Site{}, toolError(err)
	}
	return fromSite(*s), nil
}

func (b localBackend) SetSlug(ctx context.Context, ref, slug string) (mcp.Site, error) {
	s, err := b.c.UpdateSite(ctx, ref, client.UpdateSiteRequest{Slug: &slug})
	if err != nil {
		return mcp.Site{}, toolError(err)
	}
	return fromSite(*s), nil
}

func (b localBackend) ListDeploys(ctx context.Context, ref string) ([]mcp.Deploy, error) {
	l, err := b.c.ListDeploys(ctx, ref)
	if err != nil {
		return nil, toolError(err)
	}
	out := make([]mcp.Deploy, 0, len(l.Items))
	for _, d := range l.Items {
		out = append(out, fromDeploy(d))
	}
	return out, nil
}

func (b localBackend) Rollback(ctx context.Context, ref, deployID string) (mcp.DeployResult, error) {
	r, err := b.c.Rollback(ctx, ref, deployID)
	if err != nil {
		return mcp.DeployResult{}, toolError(err)
	}
	return fromResult(r), nil
}

func (b localBackend) DeleteSite(ctx context.Context, ref string) error {
	return toolError(b.c.DeleteSite(ctx, ref))
}

// DeployFolder publishes a local folder exactly like 'dropsite deploy'.
// The assistant may be steered by untrusted content, so broad folders are
// refused and secret-looking files are refused or skipped, never published.
func (b localBackend) DeployFolder(ctx context.Context, f mcp.Folder) (mcp.DeployResult, error) {
	dir, err := b.a.expandPath(f.Path)
	if err != nil {
		return mcp.DeployResult{}, err
	}
	if err := b.a.checkFolderScope(dir); err != nil {
		return mcp.DeployResult{}, err
	}
	mode := sensitiveRefuse
	if f.SkipSensitive {
		mode = sensitiveSkip
	}
	r, err := b.a.publishDir(ctx, b.c, deployOpts{dir: dir, site: f.Site, title: f.Title,
		sensitive: mode, sensitiveHints: mcpSensitiveHints})
	if err != nil {
		return mcp.DeployResult{}, toolError(err)
	}
	return fromResult(r), nil
}

// expandPath accepts an absolute path or one starting with ~/.
func (a *app) expandPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if rest, ok := strings.CutPrefix(p, "~"); ok && (rest == "" || rest[0] == '/' || rest[0] == '\\') {
		home, err := a.homeDir()
		if err != nil {
			return "", fmt.Errorf("cannot expand ~: %w", err)
		}
		p = filepath.Join(home, rest)
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("path %q is not absolute: pass the full path of the folder, e.g. %s", p,
			filepath.Join(string(os.PathSeparator)+"Users", "me", "site", "dist"))
	}
	return filepath.Clean(p), nil
}
