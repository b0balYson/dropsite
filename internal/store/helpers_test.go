package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/b0balYson/dropsite/internal/domain"
)

const base = "example.test"

var t0 = time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

func newDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func mkUser(t *testing.T, db *DB, email string, status domain.UserStatus) int64 {
	t.Helper()
	id, err := db.CreateUser(context.Background(), domain.User{
		Email: email, Role: domain.RoleUser, Status: status, CreatedAt: t0, UpdatedAt: t0,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func host(name, siteID string, kind domain.HostKind) domain.Hostname {
	return domain.Hostname{Hostname: name, SiteID: siteID, Kind: kind, Verified: true, CreatedAt: t0}
}

// mkSite creates a site with its default hostname and, when slug != "", a slug hostname.
func mkSite(t *testing.T, db *DB, id string, owner int64, slug string, at time.Time) {
	t.Helper()
	if err := createSite(db, id, owner, slug, at); err != nil {
		t.Fatal(err)
	}
}

func createSite(db *DB, id string, owner int64, slug string, at time.Time) error {
	hosts := []domain.Hostname{host(id+"."+base, id, domain.HostDefault)}
	if slug != "" {
		hosts = append(hosts, host(slug+"."+base, id, domain.HostSlug))
	}
	return db.CreateSite(context.Background(), domain.Site{
		ID: id, OwnerID: owner, Title: "Site " + id, CreatedAt: at, UpdatedAt: at,
	}, hosts)
}

func mkDeploy(t *testing.T, db *DB, siteID, id string, by int64, size int64, at time.Time) {
	t.Helper()
	err := db.InsertDeployAndActivate(context.Background(), domain.Deploy{
		ID: id, SiteID: siteID, CreatedBy: by, Source: domain.SourceAPI, SizeBytes: size, FileCount: 1,
		Status: domain.DeployReady, CreatedAt: at,
	}, at)
	if err != nil {
		t.Fatal(err)
	}
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var de *domain.Error
	if !errors.As(err, &de) || de.Code != code {
		t.Fatalf("want error code %q, got %v", code, err)
	}
}

func wantNotFound(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func ptr[T any](v T) *T { return &v }
