package service

import (
	"context"
	"testing"

	"github.com/b0balYson/dropsite/internal/domain"
)

// storedRejected reads the counter straight from the database, without the
// flush that owner reads do.
func (r *proxyRig) storedRejected(name string) int64 {
	r.t.Helper()
	site, err := r.repo.GetSiteByHost(context.Background(), r.host)
	if err != nil {
		r.t.Fatal(err)
	}
	e, err := r.repo.GetProxyEndpoint(context.Background(), site.ID, name)
	if err != nil {
		r.t.Fatal(err)
	}
	return e.Usage.Rejected
}

func TestProxyRejectedCallsAreBatched(t *testing.T) {
	r := newProxyRig(t)
	r.endpoint("ai", domain.ProxyConfig{Class: domain.ProxyClassTransparent, UpstreamURL: "https://api.example.com", Auth: domain.ProxyAuthBearer,
		Methods: []string{"POST"}, Enabled: true}, "sk")
	evil := ProxyCall{Name: "ai", Origin: "https://evil.example"}

	// Setting the endpoint up read it as the owner, which flushed: the
	// burst within the interval stays in memory.
	const burst = 500
	for range burst {
		if _, _, err := r.call(evil); domain.AsError(err).Code != domain.CodeProxyOrigin {
			t.Fatalf("want origin error, got %v", err)
		}
	}
	if got := r.storedRejected("ai"); got != 0 {
		t.Fatalf("stored after a burst: %d, want 0 (no write per request)", got)
	}
	// After the flush interval the next rejection writes the whole batch.
	r.clock.Advance(ProxyRejectedFlushInterval)
	if _, _, err := r.call(evil); err == nil {
		t.Fatal("want error")
	}
	if got := r.storedRejected("ai"); got != burst+1 {
		t.Fatalf("stored after the interval: %d, want %d", got, burst+1)
	}
	// Owner reads flush what is pending.
	for range 3 {
		_, _, _ = r.call(ProxyCall{Name: "ai", Method: "DELETE"})
	}
	v, err := r.svc.GetProxyEndpoint(context.Background(), r.owner, "bot", "ai")
	if err != nil {
		t.Fatal(err)
	}
	if u := v.Endpoint.Usage; u.Rejected != burst+4 || u.LastStatus != 405 || u.Requests != 0 {
		t.Fatalf("usage %+v", u)
	}
}
