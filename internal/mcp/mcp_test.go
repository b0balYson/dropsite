package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeBackend struct {
	sites   map[string]Site
	deleted []string
	files   []File
	folder  string
}

func newFake() *fakeBackend {
	return &fakeBackend{sites: map[string]Site{
		"abc": {ID: "abc", Title: "A", URL: "https://promo.example/", URLs: []string{"https://promo.example/"}, Slug: "promo"},
	}}
}

var errMissing = errors.New("site not found: call list_sites to see the IDs and slugs of your sites")

func (f *fakeBackend) find(ref string) (Site, error) {
	for _, s := range f.sites {
		if s.ID == ref || s.Slug == ref {
			return s, nil
		}
	}
	return Site{}, errMissing
}

func (f *fakeBackend) ListSites(context.Context) ([]Site, error) {
	out := []Site{}
	for _, s := range f.sites {
		out = append(out, s)
	}
	return out, nil
}

func (f *fakeBackend) CreateSite(_ context.Context, title, slug string) (Site, error) {
	s := Site{ID: "new", Title: title, Slug: slug, URL: "https://new.example/", URLs: []string{"https://new.example/"}}
	f.sites[s.ID] = s
	return s, nil
}

func (f *fakeBackend) GetSite(_ context.Context, ref string) (Site, error) { return f.find(ref) }

func (f *fakeBackend) SetSlug(_ context.Context, ref, slug string) (Site, error) {
	s, err := f.find(ref)
	s.Slug = slug
	f.sites[s.ID] = s
	return s, err
}

func (f *fakeBackend) ListDeploys(_ context.Context, ref string) ([]Deploy, error) {
	if _, err := f.find(ref); err != nil {
		return nil, err
	}
	return []Deploy{{ID: "d2", Current: true}, {ID: "d1"}}, nil
}

func (f *fakeBackend) Rollback(_ context.Context, ref, deployID string) (DeployResult, error) {
	if deployID == "" {
		deployID = "d1"
	}
	return DeployResult{SiteID: ref, DeployID: deployID, URL: "https://promo.example/"}, nil
}

func (f *fakeBackend) DeleteSite(_ context.Context, ref string) error {
	f.deleted = append(f.deleted, ref)
	return nil
}

func (f *fakeBackend) DeployFiles(_ context.Context, ref, _ string, files []File) (DeployResult, error) {
	f.files = files
	return DeployResult{SiteID: ref, DeployID: "d3", URL: "https://promo.example/"}, nil
}

type folderBackend struct{ *fakeBackend }

// plainBackend hides the publisher methods of the fake.
type plainBackend struct{ Backend }

func (f folderBackend) DeployFolder(_ context.Context, path, _, _ string) (DeployResult, error) {
	f.folder = path
	return DeployResult{SiteID: "abc", DeployID: "d4", URL: "https://promo.example/"}, nil
}

func connect(t *testing.T, s *sdk.Server) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	ct, st := sdk.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func call(t *testing.T, cs *sdk.ClientSession, name string, args any) (map[string]any, bool, string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	text := ""
	if len(res.Content) > 0 {
		if tc, ok := res.Content[0].(*sdk.TextContent); ok {
			text = tc.Text
		}
	}
	var out map[string]any
	if res.StructuredContent != nil {
		b, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(b, &out)
	}
	return out, res.IsError, text
}

func TestToolsOverInMemoryTransport(t *testing.T) {
	f := newFake()
	cs := connect(t, NewServer("test", func(context.Context, *sdk.CallToolRequest) (Backend, error) { return f, nil },
		Options{Files: true, Instructions: "hello"}))
	if cs.InitializeResult().Instructions != "hello" || cs.InitializeResult().ServerInfo.Name != "dropsite" {
		t.Fatalf("init: %+v", cs.InitializeResult())
	}
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
		if tool.Name == "delete_site" && (tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint) {
			t.Error("delete_site not destructive")
		}
		if tool.Name == "list_sites" && !tool.Annotations.ReadOnlyHint {
			t.Error("list_sites not read-only")
		}
	}
	sort.Strings(names)
	want := "create_site delete_site deploy_files get_site list_deploys list_sites rollback set_slug"
	if strings.Join(names, " ") != want {
		t.Fatalf("tools: %v", names)
	}

	out, isErr, _ := call(t, cs, "list_sites", map[string]any{})
	if isErr || len(out["sites"].([]any)) != 1 {
		t.Fatalf("list: %v", out)
	}
	out, isErr, _ = call(t, cs, "create_site", map[string]any{"title": "New"})
	if isErr || out["url"] != "https://new.example/" {
		t.Fatalf("create: %v", out)
	}
	out, _, _ = call(t, cs, "get_site", map[string]any{"site": "promo"})
	if out["id"] != "abc" {
		t.Fatalf("get: %v", out)
	}
	_, isErr, text := call(t, cs, "get_site", map[string]any{"site": "nope"})
	if !isErr || !strings.Contains(text, "list_sites") {
		t.Fatalf("missing site: %v %q", isErr, text)
	}
	out, _, _ = call(t, cs, "set_slug", map[string]any{"site": "abc", "slug": "may"})
	if out["slug"] != "may" {
		t.Fatalf("slug: %v", out)
	}
	out, _, _ = call(t, cs, "list_deploys", map[string]any{"site": "abc"})
	if len(out["deploys"].([]any)) != 2 {
		t.Fatalf("deploys: %v", out)
	}
	out, _, _ = call(t, cs, "rollback", map[string]any{"site": "abc"})
	if out["deploy_id"] != "d1" {
		t.Fatalf("rollback: %v", out)
	}
	_, isErr, text = call(t, cs, "delete_site", map[string]any{"site": "abc", "confirm": "other"})
	if !isErr || !strings.Contains(text, "confirm") || len(f.deleted) != 0 {
		t.Fatalf("unconfirmed delete: %v %q %v", isErr, text, f.deleted)
	}
	out, isErr, _ = call(t, cs, "delete_site", map[string]any{"site": "abc", "confirm": "MAY"})
	if isErr || out["deleted"] != "abc" || len(f.deleted) != 1 {
		t.Fatalf("delete: %v %v", out, f.deleted)
	}
	out, isErr, _ = call(t, cs, "deploy_files", map[string]any{"files": []map[string]string{{"path": "index.html", "content": "x"}}})
	if isErr || out["deploy_id"] != "d3" || len(f.files) != 1 {
		t.Fatalf("deploy_files: %v", out)
	}
	_, isErr, _ = call(t, cs, "deploy_files", map[string]any{})
	if !isErr {
		t.Fatal("deploy_files without files accepted")
	}
}

func TestFolderToolAndResolverError(t *testing.T) {
	f := folderBackend{newFake()}
	cs := connect(t, NewServer("test", func(context.Context, *sdk.CallToolRequest) (Backend, error) { return f, nil },
		Options{Folder: true}))
	out, isErr, _ := call(t, cs, "deploy_folder", map[string]any{"path": "/tmp/site"})
	if isErr || out["deploy_id"] != "d4" {
		t.Fatalf("deploy_folder: %v", out)
	}
	noLogin := errors.New("not connected: open the Connect AI page")
	cs = connect(t, NewServer("test", func(context.Context, *sdk.CallToolRequest) (Backend, error) { return nil, noLogin },
		Options{Folder: true}))
	for name, args := range map[string]map[string]any{
		"list_sites": {}, "create_site": {"title": "t"}, "get_site": {"site": "a"}, "set_slug": {"site": "a", "slug": "s"},
		"list_deploys": {"site": "a"}, "rollback": {"site": "a"}, "delete_site": {"site": "a", "confirm": "a"},
		"deploy_folder": {"path": "/x"},
	} {
		_, isErr, text := call(t, cs, name, args)
		if !isErr || !strings.Contains(text, "Connect AI") {
			t.Errorf("%s: %v %q", name, isErr, text)
		}
	}
	// A backend without the publisher interface reports it.
	cs = connect(t, NewServer("test", func(context.Context, *sdk.CallToolRequest) (Backend, error) { return newFake(), nil },
		Options{Folder: true}))
	if _, isErr, _ := call(t, cs, "deploy_folder", map[string]any{"path": "/x"}); !isErr {
		t.Fatal("folder deploy without support")
	}
	cs = connect(t, NewServer("test", func(context.Context, *sdk.CallToolRequest) (Backend, error) { return plainBackend{newFake()}, nil },
		Options{Files: true}))
	if _, isErr, _ := call(t, cs, "deploy_files", map[string]any{"files": []map[string]string{}}); !isErr {
		t.Fatal("files deploy without support")
	}
}
