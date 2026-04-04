package app

import (
	"openbid/internal/store"
	"os"
	"path/filepath"
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

func TestProductionStartupRequiresBootstrapPasswordForEmptyDatabase(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("DATA_PATH", filepath.Join(t.TempDir(), "store.db"))
	t.Setenv("SECRET_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("SECURE_COOKIES", "true")
	t.Setenv("BOOTSTRAP_ADMIN_PASSWORD", "")

	if _, err := New(); err == nil {
		t.Fatal("expected production startup to require a bootstrap admin password")
	}
}

func TestStartupRejectsInvalidBooleanEnvValue(t *testing.T) {
	t.Setenv("DATA_PATH", filepath.Join(t.TempDir(), "store.db"))
	t.Setenv("SECURE_COOKIES", "maybe")

	if _, err := New(); err == nil {
		t.Fatal("expected startup to reject invalid boolean env values")
	}
}

func TestStartupRejectsInvalidIntegerEnvValue(t *testing.T) {
	t.Setenv("DATA_PATH", filepath.Join(t.TempDir(), "store.db"))
	t.Setenv("WORKER_SYNC_MINUTES", "abc")

	if _, err := New(); err == nil {
		t.Fatal("expected startup to reject invalid integer env values")
	}
}

func TestStartupLoadsSecretsFromFile(t *testing.T) {
	tempDir := t.TempDir()
	secretPath := filepath.Join(tempDir, "secret.txt")
	passwordPath := filepath.Join(tempDir, "bootstrap.txt")
	if err := os.WriteFile(secretPath, []byte("0123456789abcdef0123456789abcdef\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(passwordPath, []byte("StrongPass!2026\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("APP_ENV", "production")
	t.Setenv("DATA_PATH", filepath.Join(tempDir, "store.db"))
	t.Setenv("SECRET_KEY_FILE", secretPath)
	t.Setenv("SECURE_COOKIES", "true")
	t.Setenv("BOOTSTRAP_ADMIN_PASSWORD_FILE", passwordPath)

	a, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	if a.Config.SecretKey != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("unexpected secret key loaded from file: %q", a.Config.SecretKey)
	}
	if a.Config.BootstrapAdminPassword != "StrongPass!2026" {
		t.Fatalf("unexpected bootstrap password loaded from file: %q", a.Config.BootstrapAdminPassword)
	}
}

func TestStartupRejectsConflictingSecretSources(t *testing.T) {
	tempDir := t.TempDir()
	secretPath := filepath.Join(tempDir, "secret.txt")
	if err := os.WriteFile(secretPath, []byte("0123456789abcdef0123456789abcdef\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("DATA_PATH", filepath.Join(tempDir, "store.db"))
	t.Setenv("SECRET_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("SECRET_KEY_FILE", secretPath)

	if _, err := New(); err == nil {
		t.Fatal("expected startup to reject conflicting secret env and secret file settings")
	}
}
