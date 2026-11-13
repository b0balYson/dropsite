package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/forms"
	"github.com/b0balYson/dropsite/internal/service"
)

// Paths served on site hosts (Caddy proxies only these to control).
const (
	formPathPrefix   = "/_form/"
	challengePath    = "/_form/challenge"
	altchaScriptPath = "/_dropsite/altcha.js"
)

// registerSitePaths mounts the visitor-facing form endpoints. They are not
// part of API v1 and are not in openapi.json.
func (a *api) registerSitePaths(mux *http.ServeMux) {
	mux.Handle("POST "+formPathPrefix+"{name}", a.wrap("POST /_form/{name}", a.submitForm))
	mux.Handle("GET "+challengePath, a.wrap("GET /_form/challenge", a.altchaChallenge))
	mux.Handle("GET "+altchaScriptPath, a.wrap("GET /_dropsite/altcha.js", a.altchaScript))
}

var altchaETag = func() string {
	sum := sha256.Sum256(forms.AltchaWidget)
	return `"` + hex.EncodeToString(sum[:8]) + `"`
}()

func (a *api) altchaScript(w http.ResponseWriter, r *http.Request) error {
	h := w.Header()
	h.Set("Content-Type", "text/javascript; charset=utf-8")
	h.Set("Cache-Control", "public, max-age=3600")
	h.Set("ETag", altchaETag)
	http.ServeContent(w, r, "altcha.js", time.Time{}, bytes.NewReader(forms.AltchaWidget))
	return nil
}

func (a *api) altchaChallenge(w http.ResponseWriter, r *http.Request) error {
	ch, err := a.svc.AltchaChallenge(r.Context(), r.Host, infoFrom(r.Context()).ip)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, ch)
	return nil
}

type formReply struct {
	OK    bool          `json:"ok"`
	Error *errorPayload `json:"error,omitempty"`
}

func (a *api) submitForm(w http.ResponseWriter, r *http.Request) error {
	html := forms.WantsHTML(r)
	data, err := forms.Parse(r.Header.Get("Content-Type"), http.MaxBytesReader(w, r.Body, forms.MaxBodyBytes))
	if err != nil {
		a.formError(w, r, html, err)
		return nil
	}
	_, err = a.svc.SubmitForm(r.Context(), service.FormPost{
		Host: r.Host, Form: r.PathValue("name"), Origin: r.Header.Get("Origin"),
		FetchSite: r.Header.Get("Sec-Fetch-Site"), IP: infoFrom(r.Context()).ip, Data: data,
	})
	if err != nil {
		a.formError(w, r, html, err)
		return nil
	}
	if !html {
		writeJSON(w, http.StatusOK, formReply{OK: true})
		return nil
	}
	if next := forms.SafeNext(data.Next); next != "" {
		http.Redirect(w, r, next, http.StatusSeeOther) //nolint:gosec // SafeNext allows only same-site paths
		return nil
	}
	writeFormPage(w, http.StatusOK, visitorLang(r), true, "")
	return nil
}

// formStatus maps intake errors to HTTP statuses.
func formStatus(e *domain.Error) int {
	switch e.Code {
	case domain.CodeFormOrigin:
		return http.StatusForbidden
	case domain.CodeFormUnsupported:
		return http.StatusUnsupportedMediaType
	}
	return statusFor(e)
}

func (a *api) formError(w http.ResponseWriter, r *http.Request, html bool, err error) {
	e := domain.AsError(err)
	if e.Kind == domain.KindInternal {
		a.logInternal(r, err)
		e = internalError()
	}
	status := formStatus(e)
	if status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", retryAfterSeconds(e))
	}
	if html {
		writeFormPage(w, status, visitorLang(r), false, formErrorText(visitorLang(r), e.Code))
		return
	}
	writeJSON(w, status, formReply{Error: &errorPayload{Code: e.Code, Message: e.Message}})
}

// visitorLang returns the first language of Accept-Language that the
// visitor page speaks (browsers list preferences in order), or English.
func visitorLang(r *http.Request) string {
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		tag, _, _ := strings.Cut(strings.TrimSpace(part), ";")
		primary, _, _ := strings.Cut(strings.ToLower(tag), "-")
		if _, ok := formPageTexts[primary]; ok {
			return primary
		}
	}
	return "en"
}

// visitorLangTags maps a page language to its html lang attribute.
var visitorLangTags = map[string]string{"ru": "ru", "en": "en", "zh": "zh-CN"}

var formErrorTexts = map[string]map[string]string{
	"ru": {
		domain.CodeNotFound:        "Форма не найдена.",
		domain.CodeFormTooLarge:    "Слишком много данных в форме.",
		domain.CodeFormEmpty:       "Форма пуста: заполните хотя бы одно поле.",
		domain.CodeFormCaptcha:     "Проверка «я не робот» не пройдена или устарела. Обновите страницу и отправьте форму ещё раз.",
		domain.CodeRateLimited:     "Слишком много отправок. Попробуйте позже.",
		domain.CodeFormOrigin:      "Форма отправлена с другого сайта.",
		domain.CodeInternal:        "Не удалось принять заявку. Попробуйте позже.",
		"":                         "Не удалось принять заявку: проверьте поля формы.",
		domain.CodeFormUnsupported: "Форма отправлена в неподдерживаемом формате.",
	},
	"en": {
		domain.CodeNotFound:        "Form not found.",
		domain.CodeFormTooLarge:    "The form contains too much data.",
		domain.CodeFormEmpty:       "The form is empty: fill in at least one field.",
		domain.CodeFormCaptcha:     "The anti-robot check failed or expired. Reload the page and submit the form again.",
		domain.CodeRateLimited:     "Too many submissions. Please try again later.",
		domain.CodeFormOrigin:      "The form was submitted from another site.",
		domain.CodeInternal:        "The submission could not be accepted. Please try again later.",
		"":                         "The submission could not be accepted: check the form fields.",
		domain.CodeFormUnsupported: "The form was sent in an unsupported format.",
	},
	"zh": {
		domain.CodeNotFound:        "未找到该表单。",
		domain.CodeFormTooLarge:    "表单中的数据过多。",
		domain.CodeFormEmpty:       "表单为空：请至少填写一个字段。",
		domain.CodeFormCaptcha:     "人机验证未通过或已过期。请刷新页面后重新提交表单。",
		domain.CodeRateLimited:     "提交次数过多，请稍后再试。",
		domain.CodeFormOrigin:      "表单是从其他网站提交的。",
		domain.CodeInternal:        "暂时无法接收您的提交，请稍后再试。",
		"":                         "无法接收您的提交：请检查表单中的各个字段。",
		domain.CodeFormUnsupported: "表单以不支持的格式提交。",
	},
}

func formErrorText(lang, code string) string {
	if t, ok := formErrorTexts[lang][code]; ok {
		return t
	}
	return formErrorTexts[lang][""]
}

var formPageTexts = map[string]map[string]string{
	"ru": {"ok_title": "Спасибо!", "ok_text": "Заявка отправлена.", "err_title": "Заявка не отправлена", "back": "Вернуться на сайт"},
	"en": {"ok_title": "Thank you!", "ok_text": "Your submission has been sent.", "err_title": "Submission not sent", "back": "Back to the site"},
	"zh": {"ok_title": "谢谢！", "ok_text": "您的信息已提交成功。", "err_title": "提交未成功", "back": "返回网站"},
}

// formPage is the neutral built-in page: no platform branding, inline
// styles only, nothing loaded from elsewhere.
var formPage = template.Must(template.New("form").Parse(`<!doctype html>
<html lang="{{.Lang}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="robots" content="noindex"><title>{{.Title}}</title>
<style>body{margin:0;min-height:100vh;display:grid;place-items:center;font:16px/1.5 system-ui,-apple-system,"Segoe UI","PingFang SC","Hiragino Sans GB","Microsoft YaHei","Noto Sans CJK SC",sans-serif;background:#f6f6f4;color:#1b1b1b;overflow-wrap:break-word}
main{max-width:28rem;margin:1rem;padding:2rem;background:#fff;border:1px solid #e2e2dc;border-radius:12px}
h1{margin:0 0 .5rem;font-size:1.5rem}p{margin:0 0 1.25rem}a{color:inherit}</style></head>
<body><main><h1>{{.Title}}</h1><p>{{.Text}}</p><p><a href="/">{{.Back}}</a></p></main></body></html>
`))

func writeFormPage(w http.ResponseWriter, status int, lang string, ok bool, msg string) {
	t := formPageTexts[lang]
	data := struct{ Lang, Title, Text, Back string }{Lang: visitorLangTags[lang], Back: t["back"]}
	if ok {
		data.Title, data.Text = t["ok_title"], t["ok_text"]
	} else {
		data.Title, data.Text = t["err_title"], msg
	}
	var buf bytes.Buffer
	if err := formPage.Execute(&buf, data); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}
