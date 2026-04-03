package app

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	"sort"
	"time"

	"tenderhub-za/internal/models"
	"tenderhub-za/internal/source"
	"tenderhub-za/internal/store"
)

type HealthDetail struct {
	Label string
	Value string
}

type HealthCard struct {
	Name    string
	Tone    string
	Summary string
	Details []HealthDetail
}

type QueueMetric struct {
	Label string
	Count int
	Tone  string
}

func (a *App) HealthPage(w http.ResponseWriter, r *http.Request) {
	u, t, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !canViewPlatformHealth(m.Role) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	now := time.Now().UTC()
	runtimeCards := a.runtimeHealthCards(r.Context(), now)
	queueMetrics := a.healthQueueMetrics(r.Context())
	sourceSummary := a.healthSourceSummary(r.Context(), now)
	a.render(w, r, "health.html", map[string]any{
		"Title":         "Health",
		"User":          u,
		"Tenant":        t,
		"HealthCards":   runtimeCards,
		"QueueMetrics":  queueMetrics,
		"SourceSummary": sourceSummary,
		"GeneratedAt":   now,
		"Uptime":        now.Sub(a.StartedAt),
	})
}

func (a *App) runtimeHealthCards(ctx context.Context, now time.Time) []HealthCard {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	appCard := HealthCard{
		Name:    "Application",
		Tone:    "success",
		Summary: "Server process is running and exposing the current workspace UI.",
		Details: []HealthDetail{
			{Label: "Environment", Value: a.Config.AppEnv},
			{Label: "Started", Value: a.StartedAt.Local().Format("2006-01-02 15:04:05")},
			{Label: "Uptime", Value: now.Sub(a.StartedAt).Truncate(time.Second).String()},
			{Label: "Address", Value: a.Config.AppAddr},
			{Label: "Goroutines", Value: fmt.Sprintf("%d", runtime.NumGoroutine())},
			{Label: "CPUs", Value: fmt.Sprintf("%d", runtime.NumCPU())},
			{Label: "GOMAXPROCS", Value: fmt.Sprintf("%d", runtime.GOMAXPROCS(0))},
			{Label: "Heap alloc", Value: formatBytes(mem.HeapAlloc)},
			{Label: "Heap sys", Value: formatBytes(mem.HeapSys)},
			{Label: "Total alloc", Value: formatBytes(mem.TotalAlloc)},
		},
	}

	dbCard := HealthCard{
		Name:    "Database",
		Tone:    "warning",
		Summary: "Database runtime details are unavailable.",
	}
	if validator, ok := a.Store.(interface{ ValidateRuntime(context.Context) error }); ok {
		if err := validator.ValidateRuntime(ctx); err == nil {
			dbCard.Tone = "success"
			dbCard.Summary = "SQLite runtime validation passed."
		} else {
			dbCard.Tone = "danger"
			dbCard.Summary = "SQLite runtime validation failed."
			dbCard.Details = append(dbCard.Details, HealthDetail{Label: "Validation", Value: err.Error()})
		}
	}
	if inspector, ok := a.Store.(interface {
		RuntimeStats(context.Context) (store.RuntimeStats, error)
	}); ok {
		if stats, err := inspector.RuntimeStats(ctx); err == nil {
			dbCard.Details = append(dbCard.Details,
				HealthDetail{Label: "DB path", Value: stats.Path},
				HealthDetail{Label: "DB size", Value: formatBytes(uint64(stats.SizeBytes))},
				HealthDetail{Label: "WAL size", Value: formatBytes(uint64(stats.WALSizeBytes))},
				HealthDetail{Label: "SHM size", Value: formatBytes(uint64(stats.SHMSizeBytes))},
				HealthDetail{Label: "Schema version", Value: fmt.Sprintf("%d / %d", stats.SchemaVersion, stats.ExpectedSchemaVersion)},
				HealthDetail{Label: "Journal mode", Value: stats.JournalMode},
				HealthDetail{Label: "Quick check", Value: stats.QuickCheck},
				HealthDetail{Label: "Tenders", Value: fmt.Sprintf("%d", stats.TenderCount)},
				HealthDetail{Label: "Jobs", Value: fmt.Sprintf("%d", stats.JobCount)},
				HealthDetail{Label: "Sources", Value: fmt.Sprintf("%d configs / %d health", stats.SourceConfigCount, stats.SourceHealthCount)},
				HealthDetail{Label: "Audit entries", Value: fmt.Sprintf("%d", stats.AuditCount)},
			)
		} else {
			dbCard.Tone = "danger"
			dbCard.Summary = "SQLite runtime stats could not be collected."
			dbCard.Details = append(dbCard.Details, HealthDetail{Label: "Stats", Value: err.Error()})
		}
	}

	extractorCard := HealthCard{
		Name:    "Extractor service",
		Tone:    "warning",
		Summary: "Extractor connectivity has not been checked.",
		Details: []HealthDetail{{Label: "Base URL", Value: a.Config.ExtractorURL}},
	}
	if a.Extractor == nil || a.Config.ExtractorURL == "" {
		extractorCard.Tone = "warning"
		extractorCard.Summary = "Extractor client is not configured."
		extractorCard.Details = append(extractorCard.Details, HealthDetail{Label: "Status", Value: "disabled"})
	} else {
		checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := a.Extractor.Healthz(checkCtx); err != nil {
			extractorCard.Tone = "danger"
			extractorCard.Summary = "Extractor health check failed."
			extractorCard.Details = append(extractorCard.Details, HealthDetail{Label: "Status", Value: err.Error()})
		} else {
			extractorCard.Tone = "success"
			extractorCard.Summary = "Extractor is reachable and responding to health checks."
			extractorCard.Details = append(extractorCard.Details, HealthDetail{Label: "Status", Value: "healthy"})
		}
	}

	workerCard := a.workerHealthCard(ctx, now)
	return []HealthCard{appCard, dbCard, extractorCard, workerCard}
}

func (a *App) workerHealthCard(ctx context.Context, now time.Time) HealthCard {
	card := HealthCard{
		Name:    "Worker pipeline",
		Tone:    "warning",
		Summary: "Worker activity is being inferred from jobs, schedules, and source runs.",
	}
	configs, configErr := a.Store.ListSourceConfigs(ctx)
	healthItems, healthErr := a.Store.ListSourceHealth(ctx)
	jobs, jobErr := a.Store.ListJobs(ctx)
	runs, runErr := a.Store.ListSyncRuns(ctx)
	if configErr != nil || healthErr != nil || jobErr != nil || runErr != nil {
		card.Tone = "danger"
		card.Summary = "Worker state could not be fully evaluated."
		if configErr != nil {
			card.Details = append(card.Details, HealthDetail{Label: "Sources", Value: configErr.Error()})
		}
		if healthErr != nil {
			card.Details = append(card.Details, HealthDetail{Label: "Source health", Value: healthErr.Error()})
		}
		if jobErr != nil {
			card.Details = append(card.Details, HealthDetail{Label: "Jobs", Value: jobErr.Error()})
		}
		if runErr != nil {
			card.Details = append(card.Details, HealthDetail{Label: "Runs", Value: runErr.Error()})
		}
		return card
	}

	settings := a.loadSourceScheduleSettings(ctx)
	healthByKey := map[string]models.SourceHealth{}
	for _, item := range healthItems {
		healthByKey[item.SourceKey] = item
	}
	overdue := 0
	pendingManual := 0
	running := 0
	enabled := 0
	for _, cfg := range configs {
		if !cfg.Enabled {
			continue
		}
		enabled++
		health := healthByKey[cfg.Key]
		if health.Running {
			running++
		}
		if health.PendingManualCheck {
			pendingManual++
		}
		if source.ShouldAutoCheck(cfg, settings) {
			dueAt := health.NextScheduledCheckAt
			if dueAt.IsZero() {
				dueAt = source.InitialNextCheckAt(now, cfg, settings)
			}
			if !health.Running && !dueAt.After(now) {
				overdue++
			}
		}
	}

	latestRun := "none"
	if len(runs) > 0 {
		latestRun = runs[0].StartedAt.Local().Format("2006-01-02 15:04:05")
	}
	if overdue == 0 {
		card.Tone = "success"
		card.Summary = "Worker schedules look current for enabled sources."
	} else {
		card.Tone = "warning"
		card.Summary = "Some enabled sources are overdue for checks or waiting on worker throughput."
	}
	card.Details = []HealthDetail{
		{Label: "Enabled sources", Value: fmt.Sprintf("%d", enabled)},
		{Label: "Running checks", Value: fmt.Sprintf("%d", running)},
		{Label: "Pending manual", Value: fmt.Sprintf("%d", pendingManual)},
		{Label: "Overdue sources", Value: fmt.Sprintf("%d", overdue)},
		{Label: "Queued/retry jobs", Value: fmt.Sprintf("%d", healthQueuedJobs(jobs))},
		{Label: "Latest source run", Value: latestRun},
		{Label: "Default interval", Value: fmt.Sprintf("%d minutes", settings.DefaultIntervalMinutes)},
	}
	return card
}

func healthQueuedJobs(jobs []models.ExtractionJob) int {
	count := 0
	for _, job := range jobs {
		if job.State == models.ExtractionQueued || job.State == models.ExtractionRetry || job.State == models.ExtractionProcessing {
			count++
		}
	}
	return count
}

func formatBytes(value uint64) string {
	size := float64(value)
	units := []string{"B", "KB", "MB", "GB", "TB"}
	unit := 0
	for size >= 1024 && unit < len(units)-1 {
		size /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%.0f %s", size, units[unit])
	}
	return fmt.Sprintf("%.1f %s", size, units[unit])
}

func (a *App) healthQueueMetrics(ctx context.Context) []QueueMetric {
	jobs, err := a.Store.ListJobs(ctx)
	if err != nil {
		return nil
	}
	summary := queueSummary(jobs)
	metrics := []QueueMetric{
		{Label: "Queued", Count: summary.Queued, Tone: "info"},
		{Label: "Processing", Count: summary.Processing, Tone: "warning"},
		{Label: "Failed", Count: summary.Failed, Tone: "danger"},
		{Label: "Completed", Count: summary.Completed, Tone: "success"},
	}
	return metrics
}

func (a *App) healthSourceSummary(ctx context.Context, now time.Time) []HealthDetail {
	configs, err := a.Store.ListSourceConfigs(ctx)
	if err != nil {
		return []HealthDetail{{Label: "Sources", Value: err.Error()}}
	}
	healthItems, err := a.Store.ListSourceHealth(ctx)
	if err != nil {
		return []HealthDetail{{Label: "Source health", Value: err.Error()}}
	}
	settings := a.loadSourceScheduleSettings(ctx)
	healthByKey := map[string]models.SourceHealth{}
	for _, item := range healthItems {
		healthByKey[item.SourceKey] = item
	}
	counts := map[string]int{
		"healthy":     0,
		"degraded":    0,
		"manual-only": 0,
		"paused":      0,
		"disabled":    0,
		"running":     0,
	}
	for _, cfg := range configs {
		health := healthByKey[cfg.Key]
		status := source.ComputeHealthStatus(cfg, settings, health)
		counts[status]++
		if !health.NextScheduledCheckAt.IsZero() && !health.NextScheduledCheckAt.After(now) && cfg.Enabled {
			counts["degraded"]++
		}
	}
	details := []HealthDetail{
		{Label: "Healthy", Value: fmt.Sprintf("%d", counts["healthy"])},
		{Label: "Degraded", Value: fmt.Sprintf("%d", counts["degraded"])},
		{Label: "Manual only", Value: fmt.Sprintf("%d", counts["manual-only"])},
		{Label: "Paused", Value: fmt.Sprintf("%d", counts["paused"])},
		{Label: "Disabled", Value: fmt.Sprintf("%d", counts["disabled"])},
		{Label: "Running", Value: fmt.Sprintf("%d", counts["running"])},
	}
	sort.Slice(details, func(i, j int) bool { return details[i].Label < details[j].Label })
	return details
}
