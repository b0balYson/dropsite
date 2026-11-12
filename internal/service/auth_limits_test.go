package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/store"
)

func (e *env) auditMeta(action string) []map[string]any {
	e.t.Helper()
	rows, _, err := e.repo.ListAudit(context.Background(), store.AuditFilter{Action: action, Page: store.Page{Limit: 1000}})
	if err != nil {
		e.t.Fatal(err)
	}
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Meta)
	}
	return out
}

func isRateLimited(err error) bool {
	de := domain.AsError(err)
	return de != nil && de.Kind == domain.KindRateLimited && de.RetryAfter > 0
}

// Parallel requests must not slip past the per-IP failure limit: the attempt
// is reserved before the slow password check.
func TestLoginParallelAttemptsAreBounded(t *testing.T) {
	e := newEnv(t)
	const n = 200
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.svc.Login(context.Background(), "admin@pp.test", "wrong-password-x", "203.0.113.9", "ua")
			if err != nil && !errors.Is(err, domain.ErrBadCredentials) && !isRateLimited(err) {
				t.Errorf("unexpected error %v", err)
			}
		}()
	}
	wg.Wait()
	if got := len(e.auditMeta("auth.login_failed")); got > FailedLoginsPerIP {
		t.Fatalf("%d password checks for %d parallel attempts, want at most %d", got, n, FailedLoginsPerIP)
	}
}

func TestLoginConcurrentAttemptFromSameIPIsRefused(t *testing.T) {
	e := newEnv(t)
	release, ok := e.svc.loginBusy.tryAcquire(ipKey("198.51.100.1"))
	if !ok {
		t.Fatal("first acquire must succeed")
	}
	_, err := e.svc.Login(context.Background(), "admin@pp.test", "admin-password", "198.51.100.1", "")
	if !isRateLimited(err) {
		t.Fatalf("a concurrent attempt from the same IP must be refused, got %v", err)
	}
	if _, err := e.svc.Login(context.Background(), "admin@pp.test", "admin-password", "198.51.100.2", ""); err != nil {
		t.Fatalf("other IPs are not blocked: %v", err)
	}
	release()
	if _, err := e.svc.Login(context.Background(), "admin@pp.test", "admin-password", "198.51.100.1", ""); err != nil {
		t.Fatalf("released: %v", err)
	}
}

func TestSuccessfulLoginDoesNotSpendLimit(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for range FailedLoginsPerIP + 5 {
		if _, err := e.svc.Login(ctx, "admin@pp.test", "admin-password", "192.0.2.7", ""); err != nil {
			t.Fatalf("successful logins must not be limited: %v", err)
		}
	}
}

func TestLoginAccountLimitAppliesToAnyEmail(t *testing.T) {
	for _, email := range []string{"admin@pp.test", "nobody@pp.test"} {
		t.Run(email, func(t *testing.T) {
			e := newEnv(t)
			ctx := context.Background()
			for i := range FailedLoginsPerAccount {
				ip := fmt.Sprintf("10.9.%d.%d", i/200, i%200)
				variant := email
				if i%2 == 1 {
					variant = "  " + strings.ToUpper(email) + " "
				}
				if _, err := e.svc.Login(ctx, variant, "wrong-password-x", ip, ""); !errors.Is(err, domain.ErrBadCredentials) {
					t.Fatalf("attempt %d: want bad credentials, got %v", i+1, err)
				}
			}
			_, err := e.svc.Login(ctx, email, "admin-password", "10.99.0.1", "")
			if !isRateLimited(err) {
				t.Fatalf("account must be limited after %d failures from many IPs, got %v", FailedLoginsPerAccount, err)
			}
			if _, err := e.svc.Login(ctx, "other@pp.test", "wrong-password-x", "10.99.0.2", ""); !errors.Is(err, domain.ErrBadCredentials) {
				t.Fatalf("other accounts unaffected, got %v", err)
			}
			e.clock.Advance(FailedLoginsWindow)
			_, err = e.svc.Login(ctx, email, "admin-password", "10.99.0.1", "")
			if email == "admin@pp.test" && err != nil {
				t.Fatalf("window expired: %v", err)
			}
		})
	}
}

func TestLoginFailedAuditSanitisesEmail(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	huge := strings.Repeat("p", 1<<20)
	inputs := []string{" Admin@pp.test ", huge, "my secret password", "a@b.c" + strings.Repeat("x", 300)}
	for i, in := range inputs {
		_, _ = e.svc.Login(ctx, in, "wrong-password-x", fmt.Sprintf("10.8.0.%d", i), "")
	}
	metas := e.auditMeta("auth.login_failed")
	if len(metas) != len(inputs) {
		t.Fatalf("got %d audit entries", len(metas))
	}
	valid, invalid := 0, 0
	for _, m := range metas {
		if v, ok := m["email"]; ok {
			s, _ := v.(string)
			if s != "Admin@pp.test" || len(m) != 1 {
				t.Fatalf("unexpected email meta %v", m)
			}
			valid++
			continue
		}
		if m["email_invalid"] != true || len(m) != 1 {
			t.Fatalf("invalid input must be flagged without its value: %v", m)
		}
		invalid++
	}
	if valid != 1 || invalid != 3 {
		t.Fatalf("valid=%d invalid=%d", valid, invalid)
	}
}

func TestTokenParallelAttemptsAreBounded(t *testing.T) {
	e := newEnv(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	unauthorized := 0
	for range 300 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.svc.AuthenticateToken(context.Background(), "nope", "203.0.113.10")
			if errors.Is(err, domain.ErrUnauthorized) {
				mu.Lock()
				unauthorized++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if unauthorized != FailedTokensPerIP {
		t.Fatalf("%d token checks got through, want exactly %d", unauthorized, FailedTokensPerIP)
	}
}

func TestValidTokenDoesNotSpendLimit(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	tok, err := e.svc.CreateToken(ctx, e.admin, "ci", 0)
	if err != nil {
		t.Fatal(err)
	}
	for range FailedTokensPerIP + 5 {
		if _, err := e.svc.AuthenticateToken(ctx, tok.Value, "203.0.113.11"); err != nil {
			t.Fatalf("valid token must not be limited: %v", err)
		}
	}
}
