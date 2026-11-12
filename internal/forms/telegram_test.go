package forms

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const testToken = "123456:ABCdefGHIjklMNOpqrSTUvwxYZ012345"

// fakeBot is a minimal Bot API.
type fakeBot struct {
	mu      sync.Mutex
	sent    []map[string]any
	fail    map[string]int // method → error code
	updates []map[string]any
}

func (b *fakeBot) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prefix := "/bot" + testToken + "/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":404,"description":"Not Found"}`))
			return
		}
		method := strings.TrimPrefix(r.URL.Path, prefix)
		var params map[string]any
		_ = json.NewDecoder(r.Body).Decode(&params)
		b.mu.Lock()
		defer b.mu.Unlock()
		if code := b.fail[method]; code != 0 {
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": code, "description": "Bad Request: chat not found " + testToken})
			return
		}
		var result any = true
		switch method {
		case "getMe":
			result = map[string]any{"id": 1, "is_bot": true, "username": "dropsite_test_bot"}
		case "sendMessage":
			b.sent = append(b.sent, params)
			result = map[string]any{"message_id": len(b.sent)}
		case "getUpdates":
			result = b.updates
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	})
}

func TestTelegramSendAndGetMe(t *testing.T) {
	bot := &fakeBot{}
	srv := httptest.NewServer(bot.handler(t))
	defer srv.Close()
	tg := NewTelegram(srv.URL, testToken)
	name, err := tg.GetMe(context.Background())
	if err != nil || name != "dropsite_test_bot" {
		t.Fatalf("getMe %q %v", name, err)
	}
	long := strings.Repeat("я", 5000)
	if err := tg.Send(context.Background(), "-100123", "<b>x</b>\n"+long); err != nil {
		t.Fatal(err)
	}
	msg := bot.sent[0]
	if msg["chat_id"] != "-100123" || msg["parse_mode"] != nil {
		t.Fatalf("message %v", msg)
	}
	if text := msg["text"].(string); !strings.HasPrefix(text, "<b>x</b>") || len([]rune(text)) > 4096 {
		t.Fatalf("text length %d", len([]rune(text)))
	}
}

func TestTelegramErrorsHideToken(t *testing.T) {
	bot := &fakeBot{fail: map[string]int{"sendMessage": 400, "getMe": 502}}
	srv := httptest.NewServer(bot.handler(t))
	tg := NewTelegram(srv.URL, testToken)
	err := tg.Send(context.Background(), "1", "x")
	if err == nil || !IsPermanent(err) || strings.Contains(err.Error(), testToken) || !strings.Contains(err.Error(), "chat not found") {
		t.Fatalf("400: %v", err)
	}
	if _, err := tg.GetMe(context.Background()); err == nil || IsPermanent(err) {
		t.Fatalf("502 must be temporary: %v", err)
	}
	srv.Close()
	err = tg.Send(context.Background(), "1", "x")
	if err == nil || IsPermanent(err) || strings.Contains(err.Error(), testToken) {
		t.Fatalf("transport error: %v", err)
	}
	wrong := NewTelegram(srv.URL+"/", "1:wrongtokenwrongtokenwrong")
	if _, err := wrong.GetMe(context.Background()); err == nil || strings.Contains(err.Error(), "wrongtoken") {
		t.Fatalf("wrong token: %v", err)
	}
}

func TestTelegramUpdates(t *testing.T) {
	bot := &fakeBot{updates: []map[string]any{
		{"update_id": 10, "message": map[string]any{"text": "/start CODE", "chat": map[string]any{"id": 42}}},
		{"update_id": 11},
	}}
	srv := httptest.NewServer(bot.handler(t))
	defer srv.Close()
	ups, err := NewTelegram(srv.URL, testToken).Updates(context.Background(), 0)
	if err != nil || len(ups) != 2 || ups[0].ChatID != "42" || ups[0].Text != "/start CODE" || ups[1].ID != 11 {
		t.Fatalf("%+v %v", ups, err)
	}
}

func TestTokenAndChatValidation(t *testing.T) {
	if !ValidBotToken(testToken) || ValidBotToken("abc") || ValidBotToken(testToken+"/x") {
		t.Fatal("token")
	}
	if !ValidChatID("-1001234") || !ValidChatID("42") || ValidChatID("@name") || ValidChatID("") {
		t.Fatal("chat id")
	}
}
