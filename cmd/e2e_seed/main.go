package main

import (
	"context"
	"log"
	"os"
	"strings"
	"time"

	"openbid/internal/auth"
	"openbid/internal/models"
	"openbid/internal/store"
)

const (
	e2eUsername     = "e2e-admin"
	e2eDisplayName  = "OpenBid E2E Admin"
	e2eEmail        = "e2e-admin@example.test"
	e2ePassword     = "OpenBidE2E!2026"
	e2ePasswordEnv  = "E2E_ADMIN_PASSWORD"
	e2eTenantName   = "OpenBid E2E Tenant"
	e2eTenantSlug   = "openbid-e2e-tenant"
	e2eFailedTender = "e2e-queue-failed"
	e2eFailedJob    = "e2e-queue-failed-job"
)

func main() {
	ctx := context.Background()
	dataPath := strings.TrimSpace(os.Getenv("DATA_PATH"))
	if dataPath == "" {
		dataPath = "./data/store.db"
	}
	s, err := store.NewSQLiteStore(dataPath)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()

	primaryTenantID, err := firstTenantID(ctx, s)
	if err != nil {
		log.Fatal(err)
	}

	password := strings.TrimSpace(os.Getenv(e2ePasswordEnv))
	if password == "" {
		password = e2ePassword
	}
	e2eUser := ensureE2EAdminUser(ctx, s, password)

	secondaryTenant := ensureTenant(ctx, s, e2eTenantName, e2eTenantSlug)
	ensureMembership(ctx, s, e2eUser.ID, primaryTenantID, models.TenantRoleOwner)
	ensureMembership(ctx, s, e2eUser.ID, secondaryTenant.ID, models.TenantRoleOwner)
	ensureE2ESchedulesPaused(ctx, s)
	ensureFailedQueueFixture(ctx, s, primaryTenantID)

	log.Printf("seeded e2e user=%s tenant=%s tender=%s", e2eUser.Username, secondaryTenant.ID, e2eFailedTender)
}

func firstTenantID(ctx context.Context, s store.Store) (string, error) {
	tenants, err := s.ListTenants(ctx)
	if err != nil {
		return "", err
	}
	if len(tenants) == 0 {
		return "", store.ErrNotFound
	}
	return tenants[0].ID, nil
}

func ensureTenant(ctx context.Context, s store.Store, name, slug string) models.Tenant {
	tenants, err := s.ListTenants(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, tenant := range tenants {
		if tenant.Slug == slug {
			return tenant
		}
	}
	tenant := models.Tenant{Name: name, Slug: slug}
	if err := s.UpsertTenant(ctx, tenant); err != nil {
		log.Fatal(err)
	}
	tenants, err = s.ListTenants(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, item := range tenants {
		if item.Slug == slug {
			return item
		}
	}
	log.Fatalf("tenant %s was not created", slug)
	return models.Tenant{}
}

func ensureMembership(ctx context.Context, s store.Store, userID, tenantID string, role models.TenantRole) {
	membership, err := s.GetMembership(ctx, userID, tenantID)
	if err == nil {
		membership.Role = role
		if err := s.UpsertMembership(ctx, membership); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err != store.ErrNotFound {
		log.Fatal(err)
	}
	if err := s.UpsertMembership(ctx, models.Membership{
		UserID:           userID,
		TenantID:         tenantID,
		Role:             role,
		Responsibilities: "E2E coverage tenant access",
	}); err != nil {
		log.Fatal(err)
	}
}

func ensureE2EAdminUser(ctx context.Context, s store.Store, password string) models.User {
	salt, hash, err := auth.HashPassword(password)
	if err != nil {
		log.Fatal(err)
	}

	user, err := s.GetUserByUsername(ctx, e2eUsername)
	switch err {
	case nil:
		user.DisplayName = e2eDisplayName
		user.Email = e2eEmail
	case store.ErrNotFound:
		user = models.User{
			Username:     e2eUsername,
			DisplayName:  e2eDisplayName,
			Email:        e2eEmail,
			PlatformRole: models.PlatformRoleSuperAdmin,
			IsActive:     true,
		}
	default:
		log.Fatal(err)
	}

	user.PasswordSalt = salt
	user.PasswordHash = hash
	user.IsActive = true
	user.PlatformRole = models.PlatformRoleSuperAdmin
	user.MFAEnabled = false
	user.MFASecret = ""
	user.RecoveryCodes = nil
	user.FailedLogins = 0
	user.LockedUntil = time.Time{}
	user.SessionVersion++

	if err := s.UpsertUser(ctx, user); err != nil {
		log.Fatal(err)
	}

	updatedUser, err := s.GetUserByUsername(ctx, e2eUsername)
	if err != nil {
		log.Fatal(err)
	}
	if err := s.DeleteSessionsForUser(ctx, updatedUser.ID); err != nil {
		log.Fatal(err)
	}
	return updatedUser
}

func ensureE2ESchedulesPaused(ctx context.Context, s store.Store) {
	settings, err := s.GetSourceScheduleSettings(ctx)
	if err != nil && err != store.ErrNotFound {
		log.Fatal(err)
	}
	if settings.ID == "" {
		settings.ID = "global"
	}
	if settings.DefaultIntervalMinutes <= 0 {
		settings.DefaultIntervalMinutes = 360
	}
	settings.Paused = true
	if err := s.UpsertSourceScheduleSettings(ctx, settings); err != nil {
		log.Fatal(err)
	}
}

func ensureFailedQueueFixture(ctx context.Context, s store.Store, tenantID string) {
	now := time.Now().UTC()
	tender := models.Tender{
		ID:             e2eFailedTender,
		Title:          "E2E Failed Queue Tender",
		Issuer:         "OpenBid Test Harness",
		SourceKey:      "treasury",
		Status:         "open",
		Province:       "Gauteng",
		Category:       "Engineering",
		DocumentURL:    "https://example.org/e2e-queue-failed.pdf",
		DocumentStatus: models.ExtractionFailed,
		OriginalURL:    "https://example.org/e2e-queue-failed",
		ClosingDate:    "2026-12-31",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if existing, err := s.GetTender(ctx, tender.ID); err == nil {
		tender.CreatedAt = existing.CreatedAt
	}
	if err := s.UpsertTender(ctx, tender); err != nil {
		log.Fatal(err)
	}
	job := models.ExtractionJob{
		ID:            e2eFailedJob,
		JobType:       models.JobTypeExtraction,
		TenderID:      tender.ID,
		DocumentURL:   tender.DocumentURL,
		State:         models.ExtractionFailed,
		LastError:     "seeded E2E failure",
		Attempts:      2,
		CreatedAt:     now,
		UpdatedAt:     now,
		NextAttemptAt: now,
	}
	if err := s.UpdateJob(ctx, job); err != nil {
		if err := s.QueueJob(ctx, job); err != nil {
			log.Fatal(err)
		}
	}
	if err := s.UpsertWorkflow(ctx, models.Workflow{
		TenantID: tenantID,
		TenderID: tender.ID,
		Status:   "reviewing",
		Priority: "high",
	}); err != nil {
		log.Fatal(err)
	}
}
