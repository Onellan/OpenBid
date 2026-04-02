package source

import (
	"strings"
	"tenderhub-za/internal/models"
	"time"
)

const (
	DefaultCheckIntervalMinutes = 360
	maxRetryBackoffMinutes      = 120
)

func NormalizeScheduleSettings(settings models.SourceScheduleSettings, fallbackMinutes int) models.SourceScheduleSettings {
	if settings.ID == "" {
		settings.ID = "global"
	}
	if settings.DefaultIntervalMinutes <= 0 {
		if fallbackMinutes > 0 {
			settings.DefaultIntervalMinutes = fallbackMinutes
		} else {
			settings.DefaultIntervalMinutes = DefaultCheckIntervalMinutes
		}
	}
	return settings
}

func EffectiveIntervalMinutes(cfg models.SourceConfig, settings models.SourceScheduleSettings) int {
	settings = NormalizeScheduleSettings(settings, DefaultCheckIntervalMinutes)
	if cfg.IntervalMinutes > 0 {
		return cfg.IntervalMinutes
	}
	return settings.DefaultIntervalMinutes
}

func ShouldAutoCheck(cfg models.SourceConfig, settings models.SourceScheduleSettings) bool {
	settings = NormalizeScheduleSettings(settings, DefaultCheckIntervalMinutes)
	return cfg.Enabled && cfg.AutoCheckEnabled && !settings.Paused
}

func InitialNextCheckAt(now time.Time, cfg models.SourceConfig, settings models.SourceScheduleSettings) time.Time {
	if !ShouldAutoCheck(cfg, settings) {
		return time.Time{}
	}
	return now.UTC().Add(time.Duration(EffectiveIntervalMinutes(cfg, settings)) * time.Minute)
}

func NextScheduledCheckAt(now time.Time, cfg models.SourceConfig, settings models.SourceScheduleSettings, consecutiveFailures int, success bool) time.Time {
	if !ShouldAutoCheck(cfg, settings) {
		return time.Time{}
	}
	now = now.UTC()
	interval := time.Duration(EffectiveIntervalMinutes(cfg, settings)) * time.Minute
	if success || consecutiveFailures <= 0 {
		return now.Add(interval)
	}
	backoffMinutes := consecutiveFailures * consecutiveFailures * 5
	if backoffMinutes > maxRetryBackoffMinutes {
		backoffMinutes = maxRetryBackoffMinutes
	}
	backoff := time.Duration(backoffMinutes) * time.Minute
	if backoff > interval {
		backoff = interval
	}
	return now.Add(backoff)
}

func ComputeHealthStatus(cfg models.SourceConfig, settings models.SourceScheduleSettings, health models.SourceHealth) string {
	switch {
	case !cfg.Enabled:
		return "disabled"
	case health.Running:
		return "running"
	case health.PendingManualCheck:
		return "queued"
	case !cfg.AutoCheckEnabled:
		return "manual-only"
	case settings.Paused:
		return "paused"
	case strings.EqualFold(health.LastStatus, "failed") && health.ConsecutiveFailures >= 3:
		return "degraded"
	case strings.EqualFold(health.LastStatus, "failed"):
		return "failing"
	case strings.EqualFold(health.LastStatus, "success"):
		return "healthy"
	default:
		return "configured"
	}
}
