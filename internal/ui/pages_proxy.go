package ui

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

// The "API keys" screen of a site: secret proxy endpoints and the only
// place where their keys are entered.

// proxyMethods are the method checkboxes of the endpoint form.
var proxyMethods = []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"}

// proxyForm is the endpoint form as submitted (re-shown after an error);
// it never carries a key.
type proxyForm struct {
	Name         string
	UpstreamURL  string
	AllowedHosts string
	Methods      map[string]bool
	Paths        string
	Auth         string
	AuthName     string
	Enabled      bool
	Editing      bool
	Class        string
	// Template is the request template as JSON text (classes ai and send).
	Template     string
	PerMinute    string
	DailyBudget  string
	SessionQuota string
}

type proxyPageData struct {
	V         service.SiteView
	Endpoints []service.ProxyEndpointView
	Methods   []string
	Classes   []domain.ProxyClass
	Form      proxyForm
	Error     string
	Limit     int
	Example   string
}

var errTemplateJSON = domain.E(domain.KindInvalid, domain.CodeProxyInvalid,
	"the request template is not valid JSON")

func formInt(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func limitText(n int) string {
	if n <= 0 {
		return ""
	}
	return strconv.Itoa(n)
}

func proxyPath(siteID string) string { return sitePath(siteID) + "/proxy" }

func lines(s string) []string {
	var out []string
	for _, l := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' || r == ',' }) {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func newProxyForm() proxyForm {
	return proxyForm{Methods: map[string]bool{"GET": true, "POST": true}, Auth: string(domain.ProxyAuthBearer), Enabled: true,
		Class: string(domain.ProxyClassAI)}
}

func readProxyForm(r *http.Request) proxyForm {
	f := proxyForm{
		Name: strings.TrimSpace(r.PostFormValue("name")), UpstreamURL: strings.TrimSpace(r.PostFormValue("upstream_url")),
		AllowedHosts: r.PostFormValue("allowed_hosts"), Paths: r.PostFormValue("paths"), Auth: r.PostFormValue("auth"),
		AuthName: strings.TrimSpace(r.PostFormValue("auth_name")), Enabled: r.PostFormValue("enabled") == "1",
		Editing: r.PostFormValue("editing") == "1", Methods: map[string]bool{},
		Class: r.PostFormValue("class"), Template: r.PostFormValue("template"),
		PerMinute: r.PostFormValue("per_minute"), DailyBudget: r.PostFormValue("daily_budget"),
		SessionQuota: r.PostFormValue("session_quota"),
	}
	for _, m := range r.PostForm["methods"] {
		f.Methods[m] = true
	}
	return f
}

// config turns the form into a configuration. The template field is used
// only by the templated classes; methods and paths only by the others.
func (f proxyForm) config() (domain.ProxyConfig, error) {
	var methods []string
	for _, m := range proxyMethods {
		if f.Methods[m] {
			methods = append(methods, m)
		}
	}
	c := domain.ProxyConfig{
		UpstreamURL: f.UpstreamURL, AllowedHosts: lines(f.AllowedHosts), Methods: methods, Paths: lines(f.Paths),
		Auth: domain.ProxyAuth(f.Auth), AuthName: f.AuthName, Enabled: f.Enabled,
		Class: domain.ProxyClass(f.Class),
		Limits: domain.ProxyLimits{PerMinute: formInt(f.PerMinute), DailyBudget: formInt(f.DailyBudget),
			SessionQuota: formInt(f.SessionQuota)},
	}
	if c.Class.Templated() && strings.TrimSpace(f.Template) != "" {
		var t domain.ProxyTemplate
		if err := json.Unmarshal([]byte(f.Template), &t); err != nil {
			return c, errTemplateJSON
		}
		c.Template = &t
	}
	return c, nil
}

func (s *server) proxyPage(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	s.renderProxy(w, r, rc, http.StatusOK, newProxyForm(), "")
}

func (s *server) renderProxy(w http.ResponseWriter, r *http.Request, rc *reqCtx, status int, form proxyForm, errMsg string) {
	v, err := s.svc.GetSite(r.Context(), rc.actor, r.PathValue("ref"))
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	eps, err := s.svc.ListProxyEndpoints(r.Context(), rc.actor, v.Site.ID)
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	s.sitePageOf(w, r, rc, status, "proxy", secProxy, "proxy.title", v, proxyPageData{
		V: v, Endpoints: eps, Methods: proxyMethods, Classes: domain.ProxyClasses, Form: form, Error: errMsg,
		Limit: service.ProxyEndpointsPerSite, Example: service.ProxyTemplateExample,
	})
}

func (s *server) saveProxy(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	form := readProxyForm(r)
	cfg, err := form.config()
	var view service.ProxyEndpointView
	var created bool
	if err == nil {
		view, created, err = s.svc.PutProxyEndpoint(r.Context(), rc.actor, r.PathValue("ref"), form.Name, cfg)
	}
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r, rc)
			return
		}
		msg, status := s.describe(rc, err)
		if de := domain.AsError(err); de.Code == domain.CodeProxyInvalid {
			msg += " " + de.Message
		}
		s.renderProxy(w, r, rc, status, form, msg)
		return
	}
	key := "proxy.saved"
	switch {
	case created:
		key = "proxy.created"
	case !view.SecretSet:
		key = "proxy.saved.nokey"
	}
	// The result shows on the endpoint's card, where the page comes back to.
	s.setFlashIn(w, r, flashOK, rc.loc.T(key, "name", view.Endpoint.Name), "proxy-"+view.Endpoint.Name)
	redirect(w, r, proxyPath(view.Endpoint.SiteID)+"#proxy-"+view.Endpoint.Name)
}

func (s *server) proxyAction(w http.ResponseWriter, r *http.Request, rc *reqCtx, act func(ref, name string) error, okKey string) {
	ref, name := r.PathValue("ref"), r.PathValue("name")
	back := proxyPath(ref)
	if err := act(ref, name); err != nil {
		s.fail(w, r, rc, err, back+"#proxy-"+name)
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T(okKey, "name", name))
	redirect(w, r, back+"#proxy-"+name)
}

func (s *server) setProxySecret(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	s.proxyAction(w, r, rc, func(ref, name string) error {
		_, err := s.svc.SetProxySecret(r.Context(), rc.actor, ref, name, r.PostFormValue("secret"))
		return err
	}, "proxy.secret.saved")
}

func (s *server) clearProxySecret(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	s.proxyAction(w, r, rc, func(ref, name string) error {
		return s.svc.ClearProxySecret(r.Context(), rc.actor, ref, name)
	}, "proxy.secret.cleared")
}

func (s *server) deleteProxy(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	ref := r.PathValue("ref")
	if err := s.svc.DeleteProxyEndpoint(r.Context(), rc.actor, ref, r.PathValue("name")); err != nil {
		s.fail(w, r, rc, err, proxyPath(ref))
		return
	}
	s.setFlash(w, r, flashOK, rc.loc.T("proxy.deleted", "name", r.PathValue("name")))
	redirect(w, r, proxyPath(ref))
}

// editProxy shows the form filled with an endpoint's configuration.
func (s *server) editProxy(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	view, err := s.svc.GetProxyEndpoint(r.Context(), rc.actor, r.PathValue("ref"), r.PathValue("name"))
	if err != nil {
		s.failPage(w, r, rc, err)
		return
	}
	e := view.Endpoint
	f := proxyForm{
		Name: e.Name, UpstreamURL: e.UpstreamURL, AllowedHosts: strings.Join(extraHosts(e.AllowedHosts), "\n"),
		Paths: strings.Join(e.Paths, "\n"), Auth: string(e.Auth), AuthName: e.AuthName, Enabled: e.Enabled,
		Editing: true, Methods: map[string]bool{}, Class: string(e.Class),
		PerMinute: limitText(e.Limits.PerMinute), DailyBudget: limitText(e.Limits.DailyBudget),
		SessionQuota: limitText(e.Limits.SessionQuota),
	}
	if e.Template != nil {
		if b, err := json.MarshalIndent(e.Template, "", "  "); err == nil {
			f.Template = string(b)
		}
	}
	for _, m := range e.Methods {
		f.Methods[m] = true
	}
	s.renderProxy(w, r, rc, http.StatusOK, f, "")
}

// extraHosts drops the upstream host, which the form does not repeat.
func extraHosts(hosts []string) []string {
	if len(hosts) < 2 {
		return nil
	}
	return hosts[1:]
}
