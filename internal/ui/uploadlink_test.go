package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/b0balYson/dropsite/internal/oauth"
	"github.com/b0balYson/dropsite/internal/testenv"
)

func TestUploadPage(t *testing.T) {
	env := testenv.New(t)
	h := NewAPIHostPages(env.Svc, oauth.New(env.Svc, oauth.Options{}), func(*http.Request) string { return "203.0.113.9" }, nil)
	a := env.User("page@pp.test")
	info, err := env.Svc.BeginUpload(context.Background(), a, "", "Page", "", "")
	if err != nil {
		t.Fatal(err)
	}
	path := info.LinkURL[strings.Index(info.LinkURL, "/u/"):]
	get := func(target, accept string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		if accept != "" {
			req.Header.Set("Accept-Language", accept)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	for _, c := range []struct{ accept, query, want, lang string }{
		{"ru-RU,ru;q=0.9", "", "Перетащите сюда папку или файлы", `lang="ru"`},
		{"en-US", "", "Drop a folder or files here", `lang="en"`},
		{"zh-CN", "", "将文件夹或文件拖到这里", `lang="zh-CN"`},
		{"en-US", "?ui_lang=ru", "Готово. Вернитесь в чат и напишите «готово».", `lang="ru"`},
	} {
		rec := get(path+c.query, c.accept)
		body := rec.Body.String()
		if rec.Code != http.StatusOK || !strings.Contains(body, c.want) || !strings.Contains(body, c.lang) {
			t.Fatalf("%s %s: %d %s", c.accept, c.query, rec.Code, body)
		}
		if !strings.Contains(body, "upload.js") || strings.Contains(body, "<script>") {
			t.Fatal("page script missing or inline")
		}
		hd := rec.Header()
		if hd.Get("Referrer-Policy") != "no-referrer" || !strings.Contains(hd.Get("X-Robots-Tag"), "noindex") ||
			!strings.Contains(hd.Get("Content-Security-Policy"), "script-src 'self'") || hd.Get("Set-Cookie") != "" {
			t.Fatalf("headers %v", hd)
		}
	}
	// An unknown or used link shows a friendly page.
	rec := get("/u/"+strings.Repeat("x", 43), "ru")
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "Ссылка устарела, попросите ассистента новую.") ||
		strings.Contains(rec.Body.String(), "upload.js") {
		t.Fatalf("expired page: %d %s", rec.Code, rec.Body.String())
	}
}
