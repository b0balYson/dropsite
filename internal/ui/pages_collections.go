package ui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/mcp"
	"github.com/b0balYson/dropsite/internal/service"
)

// Site data (collections): the list on the site page, a page with every
// collection of a site and a page per collection with its documents.

// collectionPageSize is how many documents a collection page shows.
const collectionPageSize = 50

func dataPath(siteID string) string { return sitePath(siteID) + "/data" }

func collectionPagePath(siteID, name string) string { return dataPath(siteID) + "/" + url.PathEscape(name) }

// collectionForm is a submitted create or edit form, kept for re-rendering.
type collectionForm struct {
	Name   string
	Title  string
	Mode   string
	Schema string
	Error  string
}

type collectionsPageData struct {
	V           service.SiteView
	Collections []service.CollectionView
	New         collectionForm
	Edit        collectionForm // the collection whose edit form failed
}

func (s *server) renderCollections(w http.ResponseWriter, r *http.Request, rc *reqCtx, status int, newForm, edit collectionForm) {
	v, cs, err := s.svc.ListCollections(r.Context(), rc.actor, r.PathValue("ref"))
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	if newForm.Mode == "" {
		newForm.Mode = string(domain.CollectionPublic)
	}
	s.page(w, r, rc, status, "collections", "sites", "data.title",
		collectionsPageData{V: v, Collections: cs, New: newForm, Edit: edit})
}

func (s *server) collectionsPage(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	s.renderCollections(w, r, rc, http.StatusOK, collectionForm{}, collectionForm{})
}

// describeData is describe plus the English detail of document and schema
// errors, which name the field that failed.
func (s *server) describeData(rc *reqCtx, err error) (string, int) {
	msg, status := s.describe(rc, err)
	de := domain.AsError(err)
	if de.Code == domain.CodeDocumentInvalid || de.Code == domain.CodeCollectionInvalid {
		msg += " " + de.Message
	}
	return msg, status
}

func schemaText(raw string) json.RawMessage {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return json.RawMessage(raw)
}

func (s *server) createCollection(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	f := collectionForm{Name: r.PostFormValue("name"), Title: r.PostFormValue("title"), Mode: r.PostFormValue("mode"),
		Schema: r.PostFormValue("schema")}
	c, err := s.svc.CreateCollection(r.Context(), rc.actor, r.PathValue("ref"), service.CollectionInput{
		Name: f.Name, Title: f.Title, Mode: domain.CollectionMode(f.Mode), Schema: schemaText(f.Schema),
	})
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r, rc)
			return
		}
		var status int
		f.Error, status = s.describeData(rc, err)
		s.renderCollections(w, r, rc, status, f, collectionForm{})
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("data.created", "name", c.Name))
	redirect(w, r, collectionPagePath(c.SiteID, c.Name))
}

func (s *server) updateCollection(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	name := r.PathValue("name")
	f := collectionForm{Name: name, Title: r.PostFormValue("title"), Mode: r.PostFormValue("mode"), Schema: r.PostFormValue("schema")}
	mode := domain.CollectionMode(f.Mode)
	schema := json.RawMessage(f.Schema)
	if strings.TrimSpace(f.Schema) == "" {
		schema = json.RawMessage("null")
	}
	c, err := s.svc.UpdateCollection(r.Context(), rc.actor, r.PathValue("ref"), name,
		service.CollectionUpdate{Title: &f.Title, Mode: &mode, Schema: &schema})
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r, rc)
			return
		}
		var status int
		f.Error, status = s.describeData(rc, err)
		s.renderCollections(w, r, rc, status, collectionForm{}, f)
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("data.saved"))
	redirect(w, r, dataPath(c.SiteID))
}

func (s *server) deleteCollection(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	ref := r.PathValue("ref")
	if err := s.svc.DeleteCollection(r.Context(), rc.actor, ref, r.PathValue("name"), r.PostFormValue("confirm")); err != nil {
		s.fail(w, r, rc, err, dataPath(ref))
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("data.deleted"))
	redirect(w, r, dataPath(ref))
}

// documentForm is the add or edit form of a document.
type documentForm struct {
	ID       string
	Position string
	Data     string
	Error    string
	Editing  bool
}

type collectionPageData struct {
	V        service.SiteView
	C        service.CollectionView
	Docs     []domain.Document
	Status   string
	Next     string
	First    string
	Form     documentForm
	Snippet  string
	Statuses []string
}

// prettyJSON indents a stored document for the editor.
func prettyJSON(raw json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return string(raw)
	}
	return buf.String()
}

func (s *server) renderCollection(w http.ResponseWriter, r *http.Request, rc *reqCtx, status int, form *documentForm) {
	ref, name := r.PathValue("ref"), r.PathValue("name")
	v, err := s.svc.GetSite(r.Context(), rc.actor, ref)
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	c, err := s.svc.GetCollection(r.Context(), rc.actor, v.Site.ID, name)
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	q := r.URL.Query()
	st := q.Get("status")
	if st != string(domain.DocumentApproved) && st != string(domain.DocumentPending) {
		st = "all"
	}
	p, err := s.svc.ListDocuments(r.Context(), rc.actor, v.Site.ID, c.Name,
		service.DocumentQuery{Status: st, Cursor: q.Get("cursor"), Limit: collectionPageSize})
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	base := collectionPagePath(v.Site.ID, c.Name)
	data := collectionPageData{V: v, C: c, Docs: p.Items, Status: st, Snippet: mcp.CollectionSnippet(c.Name),
		Statuses: []string{"all", string(domain.DocumentPending), string(domain.DocumentApproved)}}
	if p.Next != "" {
		data.Next = base + "?" + url.Values{"status": {st}, "cursor": {p.Next}}.Encode()
	}
	if q.Get("cursor") != "" {
		data.First = base + "?" + url.Values{"status": {st}}.Encode()
	}
	switch {
	case form != nil:
		data.Form = *form
	case q.Get("edit") != "":
		d, err := s.svc.GetDocument(r.Context(), rc.actor, v.Site.ID, c.Name, q.Get("edit"))
		if err != nil {
			s.failPage(w, r, rc, err)
			return
		}
		data.Form = documentForm{ID: d.ID, Position: strconv.FormatInt(d.Position, 10), Data: prettyJSON(d.Data), Editing: true}
	default:
		data.Form = documentForm{Data: "{\n  \n}"}
	}
	s.page(w, r, rc, status, "collection", "sites", "data.collection.title", data)
}

func (s *server) collectionPage(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	s.renderCollection(w, r, rc, http.StatusOK, nil)
}

func (s *server) saveDocument(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	ref, name := r.PathValue("ref"), r.PathValue("name")
	f := documentForm{ID: strings.TrimSpace(r.PostFormValue("id")), Position: strings.TrimSpace(r.PostFormValue("position")),
		Data: r.PostFormValue("data"), Editing: r.PostFormValue("editing") == "1"}
	in := service.DocumentInput{ID: f.ID, Data: json.RawMessage(f.Data)}
	if f.Position != "" {
		n, err := strconv.ParseInt(f.Position, 10, 64)
		if err != nil {
			f.Error = rc.loc.T("data.doc.position.invalid")
			s.renderCollection(w, r, rc, http.StatusBadRequest, &f)
			return
		}
		in.Position = &n
	}
	if _, err := s.svc.SaveDocuments(r.Context(), rc.actor, ref, name, []service.DocumentInput{in}, false); err != nil {
		if isNotFound(err) {
			s.notFound(w, r, rc)
			return
		}
		var status int
		f.Error, status = s.describeData(rc, err)
		s.renderCollection(w, r, rc, status, &f)
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("data.doc.saved"))
	redirect(w, r, collectionPagePath(ref, name))
}

func (s *server) deleteDocument(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	ref, name := r.PathValue("ref"), r.PathValue("name")
	if _, _, err := s.svc.DeleteDocuments(r.Context(), rc.actor, ref, name, []string{r.PostFormValue("id")}); err != nil {
		s.fail(w, r, rc, err, collectionPagePath(ref, name))
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("data.doc.deleted"))
	redirect(w, r, collectionPagePath(ref, name))
}

func (s *server) moderateDocuments(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	ref, name := r.PathValue("ref"), r.PathValue("name")
	back := collectionPagePath(ref, name) + "?status=pending"
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, rc, err, back)
		return
	}
	ids := r.PostForm["ids"]
	approve := r.PostFormValue("action") == "approve"
	n, _, err := s.svc.ModerateDocuments(r.Context(), rc.actor, ref, name, ids, approve)
	if err != nil {
		s.fail(w, r, rc, err, back)
		return
	}
	key := "data.moderated.rejected"
	if approve {
		key = "data.moderated.approved"
	}
	s.setFlash(w, r, flashOK, rc.loc.T(key, "n", n))
	redirect(w, r, back)
}
