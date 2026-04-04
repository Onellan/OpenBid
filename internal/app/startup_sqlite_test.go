package app

import (
	"openbid/internal/models"
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
	t.Setenv("BOOTSTRAP_SYNC_ON_STARTUP", "false")
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
	var defaultTenant models.Tenant
	for _, tenant := range tenants {
		if tenant.Slug == "kolabosolutions" {
			defaultTenant = tenant
			break
		}
	}
	if defaultTenant.ID == "" || defaultTenant.Name != "KolaboSolutions" {
		t.Fatalf("expected KolaboSolutions bootstrap tenant, got %#v", tenants)
	}
	configs, err := a.Store.ListSourceConfigs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var sawTreasury, sawEskom, sawOnlineTenders bool
	for _, cfg := range configs {
		if cfg.Key == "treasury" {
			sawTreasury = true
		}
		if cfg.Key == "eskom" && cfg.Type == "eskom_portal" && cfg.FeedURL == "https://tenderbulletin.eskom.co.za/?pageSize=5&pageNumber=1" {
			sawEskom = true
		}
		if cfg.Key == "onlinetenders" && cfg.Type == "onlinetenders_portal" && cfg.FeedURL == "https://www.onlinetenders.co.za/tenders/south-africa?tcs=civil%23engineering%20consultants" {
			sawOnlineTenders = true
		}
	}
	if !sawTreasury || !sawEskom || !sawOnlineTenders {
		t.Fatalf("expected built-in treasury, eskom, and onlinetenders sources, got %#v", configs)
	}
	assignments, err := a.Store.ListSourceAssignments(t.Context(), defaultTenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assignments) != len(configs) {
		t.Fatalf("expected default tenant to be linked to all sources, got assignments=%#v configs=%#v", assignments, configs)
	}
}

func TestStartupEnsuresBuiltInSourcesForExistingDatabase(t *testing.T) {
	dataPath := filepath.Join(t.TempDir(), "store.db")
	s, err := store.NewSQLiteStore(dataPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if err := s.UpsertUser(ctx, models.User{ID: "u1", Username: "admin", DisplayName: "Admin", Email: "admin@example.org", IsActive: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertTenant(ctx, models.Tenant{ID: "t1", Name: "Tenant One", Slug: "tenant-one"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertMembership(ctx, models.Membership{ID: "m1", UserID: "u1", TenantID: "t1", Role: models.TenantRoleOwner}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertSourceConfig(ctx, models.SourceConfig{
		Key:                 "municipal-feed",
		Name:                "Municipal Feed",
		Type:                "json_feed",
		FeedURL:             "https://example.org/feed.json",
		Enabled:             true,
		ManualChecksEnabled: true,
		AutoCheckEnabled:    true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	t.Setenv("DATA_PATH", dataPath)
	t.Setenv("BOOTSTRAP_SYNC_ON_STARTUP", "false")
	a, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	configs, err := a.Store.ListSourceConfigs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var sawCustom, sawTreasury, sawEskom, sawOnlineTenders bool
	for _, cfg := range configs {
		switch cfg.Key {
		case "municipal-feed":
			sawCustom = true
		case "treasury":
			sawTreasury = true
		case "eskom":
			sawEskom = cfg.Type == "eskom_portal"
		case "onlinetenders":
			sawOnlineTenders = cfg.Type == "onlinetenders_portal"
		}
	}
	if !sawCustom || !sawTreasury || !sawEskom || !sawOnlineTenders {
		t.Fatalf("expected existing and built-in sources, got %#v", configs)
	}
	tenants, err := a.Store.ListTenants(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var defaultTenantID string
	for _, tenant := range tenants {
		if tenant.Slug == "kolabosolutions" && tenant.Name == "KolaboSolutions" {
			defaultTenantID = tenant.ID
			break
		}
	}
	if defaultTenantID == "" {
		t.Fatalf("expected KolaboSolutions tenant to be created, got %#v", tenants)
	}
	assignments, err := a.Store.ListSourceAssignments(t.Context(), defaultTenantID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assignments) != len(configs) {
		t.Fatalf("expected KolaboSolutions to be linked to all current sources, got assignments=%#v configs=%#v", assignments, configs)
	}
}

func TestStartupDefaultTenantSourceAssignmentsRemainIdempotent(t *testing.T) {
	dataPath := filepath.Join(t.TempDir(), "store.db")
	t.Setenv("DATA_PATH", dataPath)
	t.Setenv("BOOTSTRAP_SYNC_ON_STARTUP", "false")

	a, err := New()
	if err != nil {
		t.Fatal(err)
	}

	tenants, err := a.Store.ListTenants(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var defaultTenantID string
	for _, tenant := range tenants {
		if tenant.Slug == "kolabosolutions" {
			defaultTenantID = tenant.ID
			break
		}
	}
	if defaultTenantID == "" {
		t.Fatal("expected KolaboSolutions tenant")
	}
	initialAssignments, err := a.Store.ListSourceAssignments(t.Context(), defaultTenantID)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}

	a, err = New()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	assignments, err := a.Store.ListSourceAssignments(t.Context(), defaultTenantID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assignments) != len(initialAssignments) {
		t.Fatalf("expected idempotent tenant source assignments, got before=%d after=%d", len(initialAssignments), len(assignments))
	}
}

func TestStartupSupportsBootstrapTenantOverride(t *testing.T) {
	t.Setenv("DATA_PATH", filepath.Join(t.TempDir(), "store.db"))
	t.Setenv("BOOTSTRAP_SYNC_ON_STARTUP", "false")
	t.Setenv("BOOTSTRAP_TENANT_NAME", "Custom Tenant")
	t.Setenv("BOOTSTRAP_TENANT_SLUG", "custom-tenant")

	a, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	tenants, err := a.Store.ListTenants(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tenant := range tenants {
		if tenant.Name == "Custom Tenant" && tenant.Slug == "custom-tenant" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected custom bootstrap tenant override, got %#v", tenants)
	}
}

func TestStartupMigratesLegacyRoleModel(t *testing.T) {
	dataPath := filepath.Join(t.TempDir(), "store.db")
	s, err := store.NewSQLiteStore(dataPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if err := s.UpsertTenant(ctx, models.Tenant{ID: "t1", Name: "Legacy Tenant", Slug: "legacy-tenant"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertUser(ctx, models.User{ID: "u1", Username: "legacy-admin", DisplayName: "Legacy Admin", Email: "legacy-admin@example.org", IsActive: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertUser(ctx, models.User{ID: "u2", Username: "legacy-reviewer", DisplayName: "Legacy Reviewer", Email: "legacy-reviewer@example.org", IsActive: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertMembership(ctx, models.Membership{ID: "m1", UserID: "u1", TenantID: "t1", Role: models.TenantRole("portfolio_manager")}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertMembership(ctx, models.Membership{ID: "m2", UserID: "u2", TenantID: "t1", Role: models.TenantRole("reviewer")}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	t.Setenv("DATA_PATH", dataPath)
	t.Setenv("BOOTSTRAP_SYNC_ON_STARTUP", "false")
	a, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	adminUser, err := a.Store.GetUser(t.Context(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if adminUser.PlatformRole != models.PlatformRoleAdmin {
		t.Fatalf("expected legacy portfolio manager to migrate to platform admin, got %#v", adminUser)
	}
	adminMembership, err := a.Store.GetMembership(t.Context(), "u1", "t1")
	if err != nil {
		t.Fatal(err)
	}
	if adminMembership.Role != models.TenantRoleAdmin {
		t.Fatalf("expected legacy portfolio manager membership to migrate to tenant admin, got %#v", adminMembership)
	}
	reviewerMembership, err := a.Store.GetMembership(t.Context(), "u2", "t1")
	if err != nil {
		t.Fatal(err)
	}
	if reviewerMembership.Role != models.TenantRoleSuperUser {
		t.Fatalf("expected legacy reviewer membership to migrate to super user, got %#v", reviewerMembership)
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
