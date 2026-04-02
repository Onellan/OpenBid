package source

import (
	"tenderhub-za/internal/models"
	"testing"
	"time"
)

func TestEffectiveIntervalMinutesUsesOverride(t *testing.T) {
	settings := models.SourceScheduleSettings{DefaultIntervalMinutes: 60}
	cfg := models.SourceConfig{Enabled: true, AutoCheckEnabled: true, IntervalMinutes: 15}
	if got := EffectiveIntervalMinutes(cfg, settings); got != 15 {
		t.Fatalf("expected override interval, got %d", got)
	}
}

func TestShouldAutoCheckSkipsDisabledAndPausedSources(t *testing.T) {
	settings := models.SourceScheduleSettings{DefaultIntervalMinutes: 60}
	if ShouldAutoCheck(models.SourceConfig{Enabled: false, AutoCheckEnabled: true}, settings) {
		t.Fatal("disabled source should not auto-check")
	}
	if ShouldAutoCheck(models.SourceConfig{Enabled: true, AutoCheckEnabled: false}, settings) {
		t.Fatal("manual-only source should not auto-check")
	}
	settings.Paused = true
	if ShouldAutoCheck(models.SourceConfig{Enabled: true, AutoCheckEnabled: true}, settings) {
		t.Fatal("paused global schedule should skip auto-check")
	}
}

func TestNextScheduledCheckAtUsesFailureBackoff(t *testing.T) {
	now := time.Date(2026, 4, 3, 9, 0, 0, 0, time.UTC)
	cfg := models.SourceConfig{Enabled: true, AutoCheckEnabled: true}
	settings := models.SourceScheduleSettings{DefaultIntervalMinutes: 180}
	if got := NextScheduledCheckAt(now, cfg, settings, 0, true); got.Sub(now) != 180*time.Minute {
		t.Fatalf("expected normal interval, got %v", got.Sub(now))
	}
	if got := NextScheduledCheckAt(now, cfg, settings, 2, false); got.Sub(now) != 20*time.Minute {
		t.Fatalf("expected quadratic retry backoff, got %v", got.Sub(now))
	}
}
