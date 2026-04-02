package app

import (
	"os"
	"path/filepath"
	"tenderhub-za/internal/store"
	"testing"
)

func TestSeededStartupSQLite(t *testing.T) {
	old := os.Getenv("DATA_PATH")
	t.Cleanup(func() { _ = os.Setenv("DATA_PATH", old) })
	if err := os.Setenv("DATA_PATH", filepath.Join(t.TempDir(), "store.db")); err != nil {
		t.Fatal(err)
	}
	a, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if closer, ok := a.Store.(interface{ Close() error }); ok {
		defer closer.Close()
	}
	users, err := a.Store.ListUsers(t.Context())
	if err != nil || len(users) == 0 {
		t.Fatalf("expected seeded user, err=%v len=%d", err, len(users))
	}
	tenants, err := a.Store.ListTenants(t.Context())
	if err != nil || len(tenants) == 0 {
		t.Fatalf("expected seeded tenant, err=%v len=%d", err, len(tenants))
	}
	_, total, err := a.Store.ListTenders(t.Context(), store.ListFilter{Page: 1, PageSize: 100})
	if err != nil || total == 0 {
		t.Fatalf("expected seeded tenders, err=%v total=%d", err, total)
	}
}
