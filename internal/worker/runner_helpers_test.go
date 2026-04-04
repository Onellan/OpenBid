package worker

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"tenderhub-za/internal/models"
	"tenderhub-za/internal/source"
	"tenderhub-za/internal/store"
)

func TestDueSourceTriggerPrefersManualAndScheduledDueStates(t *testing.T) {
	now := time.Date(2026, 4, 4, 10, 0, 0, 0, time.UTC)
	cfg := models.SourceConfig{Enabled: true, ManualChecksEnabled: true, AutoCheckEnabled: true}
	settings := models.SourceScheduleSettings{DefaultIntervalMinutes: 60}

	trigger, ok := dueSourceTrigger(now, cfg, settings, models.SourceHealth{PendingManualCheck: true})
	if !ok || trigger != "manual" {
		t.Fatalf("expected manual trigger, got trigger=%q ok=%v", trigger, ok)
	}

	trigger, ok = dueSourceTrigger(now, cfg, settings, models.SourceHealth{NextScheduledCheckAt: now.Add(-time.Minute)})
	if !ok || trigger != "scheduled" {
		t.Fatalf("expected scheduled trigger, got trigger=%q ok=%v", trigger, ok)
	}

	trigger, ok = dueSourceTrigger(now, cfg, settings, models.SourceHealth{Running: true, PendingManualCheck: true})
	if ok || trigger != "" {
		t.Fatalf("expected running source not to trigger, got trigger=%q ok=%v", trigger, ok)
	}
}

func TestRunnerNextWaitRespondsToImmediateManualChecksAndQueuedJobs(t *testing.T) {
	s, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	if err := s.UpsertSourceScheduleSettings(ctx, models.SourceScheduleSettings{ID: "global", DefaultIntervalMinutes: 60}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertSourceConfig(ctx, models.SourceConfig{Key: "manual", Name: "Manual", Type: source.TypeJSONFeed, Enabled: true, ManualChecksEnabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertSourceHealth(ctx, models.SourceHealth{SourceKey: "manual", PendingManualCheck: true}); err != nil {
		t.Fatal(err)
	}

	r := Runner{Store: s, LoopEvery: 30 * time.Second, SyncEvery: time.Hour, Now: func() time.Time { return now }}
	if wait := r.nextWait(ctx); wait != time.Second {
		t.Fatalf("expected immediate wakeup for pending manual check, got %v", wait)
	}

	if err := s.UpsertSourceHealth(ctx, models.SourceHealth{SourceKey: "manual", PendingManualCheck: false}); err != nil {
		t.Fatal(err)
	}
	if err := s.QueueJob(ctx, models.ExtractionJob{ID: "job-1", TenderID: "tender-1", DocumentURL: "https://example.org/doc.pdf", State: models.ExtractionQueued, NextAttemptAt: now}); err != nil {
		t.Fatal(err)
	}
	if wait := r.nextWait(ctx); wait != time.Second {
		t.Fatalf("expected immediate wakeup for due queued job, got %v", wait)
	}
}

func TestRunnerResetRunningSourcesClearsRunningFlag(t *testing.T) {
	s, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	if err := s.UpsertSourceScheduleSettings(ctx, models.SourceScheduleSettings{ID: "global", DefaultIntervalMinutes: 60}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertSourceConfig(ctx, models.SourceConfig{Key: "metro", Name: "Metro", Type: source.TypeJSONFeed, Enabled: true, ManualChecksEnabled: true, AutoCheckEnabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertSourceHealth(ctx, models.SourceHealth{SourceKey: "metro", Running: true, LastStatus: "running", LastCheckedAt: time.Now().Add(-time.Minute), LastSuccessfulCheckAt: time.Now().Add(-2 * time.Minute)}); err != nil {
		t.Fatal(err)
	}

	r := Runner{Store: s, SyncEvery: time.Hour}
	if err := r.resetRunningSources(ctx); err != nil {
		t.Fatal(err)
	}
	health, err := s.GetSourceHealth(ctx, "metro")
	if err != nil {
		t.Fatal(err)
	}
	if health.Running {
		t.Fatalf("expected running flag cleared, got %#v", health)
	}
	if health.HealthStatus == "" {
		t.Fatalf("expected recomputed health status, got %#v", health)
	}
}

func TestMergeFactMapsAndApplyDocumentPromotionsHelpers(t *testing.T) {
	facts := mergeFactMaps(
		map[string]string{"document_title": "Electrical upgrade", "contact_name": "Jane Doe", "contact_email": "jane@example.org"},
		map[string]string{"location_city": "Pretoria", "briefing_required": "yes", "briefing_date": "2026-04-10", "briefing_time": "10:00"},
	)
	tender := models.Tender{
		Title:   "DPWI tender PT25/020",
		Summary: "DPWI tender PT25/020",
	}
	applyDocumentPromotions(&tender, facts)

	if tender.Title != "Electrical upgrade" || tender.Summary != "Electrical upgrade" {
		t.Fatalf("expected placeholder title and summary to be promoted, got %#v", tender)
	}
	if tender.Location.Town != "Pretoria" {
		t.Fatalf("expected location promotion, got %#v", tender.Location)
	}
	if len(tender.Contacts) != 1 || tender.Contacts[0].Name != "Jane Doe" {
		t.Fatalf("expected contact promotion, got %#v", tender.Contacts)
	}
	if len(tender.Briefings) != 1 || !tender.Briefings[0].Required || tender.Briefings[0].DateTime != "2026-04-10 10:00" {
		t.Fatalf("expected briefing promotion, got %#v", tender.Briefings)
	}
}
