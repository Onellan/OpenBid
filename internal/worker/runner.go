package worker

import (
	"context"
	"fmt"
	"log"
	"openbid/internal/extract"
	"openbid/internal/models"
	"openbid/internal/netguard"
	"openbid/internal/source"
	"openbid/internal/store"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Runner struct {
	Store                store.Store
	Sources              source.Registry
	SourceLoad           func(context.Context) (source.Registry, error)
	Extractor            *extract.Client
	HeartbeatPath        string
	SyncEvery, LoopEvery time.Duration
	Now                  func() time.Time
}

func (r Runner) logKV(event string, fields ...any) {
	parts := []string{"event=" + event}
	for i := 0; i+1 < len(fields); i += 2 {
		key := "field_" + strconv.Itoa(i/2)
		if rawKey, ok := fields[i].(string); ok && strings.TrimSpace(rawKey) != "" {
			key = strings.TrimSpace(strings.ToLower(strings.ReplaceAll(rawKey, " ", "_")))
		}
		value := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(strings.Trim(strings.TrimSpace(toString(fields[i+1])), "\"")), "\n", " "), "\r", " "))
		if value == "" {
			value = "-"
		}
		parts = append(parts, key+"="+value)
	}
	log.Printf(strings.Join(parts, " "))
}

func toString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case error:
		return v.Error()
	default:
		return fmt.Sprint(v)
	}
}

func (r Runner) Run(ctx context.Context) error {
	if err := r.resetRunningSources(ctx); err != nil {
		r.logKV("worker_reset_running_sources_failed", "error", err)
	}
	r.writeHeartbeat()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		r.processSourceChecks(ctx)
		r.processJobs(ctx)
		r.writeHeartbeat()
		wait := r.nextWait(ctx)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (r Runner) writeHeartbeat() {
	if strings.TrimSpace(r.HeartbeatPath) == "" {
		return
	}
	path := filepath.Clean(r.HeartbeatPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		r.logKV("worker_heartbeat_dir_failed", "path", path, "error", err)
		return
	}
	payload := []byte(r.now().Format(time.RFC3339Nano))
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		r.logKV("worker_heartbeat_write_failed", "path", path, "error", err)
	}
}

func (r Runner) persistSyncRun(ctx context.Context, run models.SyncRun) {
	if err := r.Store.AddSyncRun(ctx, run); err != nil {
		r.logKV("worker_sync_run_write_failed", "source", run.SourceKey, "trigger", run.Trigger, "error", err)
	}
}

func (r Runner) persistSourceHealth(ctx context.Context, health models.SourceHealth) {
	if err := r.Store.UpsertSourceHealth(ctx, health); err != nil {
		r.logKV("worker_source_health_write_failed", "source", health.SourceKey, "status", health.LastStatus, "error", err)
	}
}

func (r Runner) persistTender(ctx context.Context, tender models.Tender) {
	if err := r.Store.UpsertTender(ctx, tender); err != nil {
		r.logKV("worker_tender_write_failed", "tender", tender.ID, "source", tender.SourceKey, "error", err)
	}
}

func (r Runner) persistJobUpdate(ctx context.Context, job models.ExtractionJob) bool {
	if err := r.Store.UpdateJob(ctx, job); err != nil {
		r.logKV("worker_job_update_failed", "job", job.ID, "tender", job.TenderID, "state", job.State, "error", err)
		return false
	}
	return true
}

func (r Runner) persistQueuedJob(ctx context.Context, job models.ExtractionJob) {
	if err := r.Store.QueueJob(ctx, job); err != nil {
		r.logKV("worker_job_queue_failed", "tender", job.TenderID, "url", job.DocumentURL, "error", err)
	}
}

func (r Runner) deleteJob(ctx context.Context, id string) {
	if err := r.Store.DeleteJob(ctx, id); err != nil {
		r.logKV("worker_job_delete_failed", "job", id, "error", err)
	}
}

func (r Runner) updateTenderDocumentState(ctx context.Context, tenderID string, state models.ExtractionState) {
	tender, err := r.Store.GetTender(ctx, tenderID)
	if err != nil {
		r.logKV("worker_tender_lookup_failed", "tender", tenderID, "error", err)
		return
	}
	tender.DocumentStatus = state
	r.persistTender(ctx, tender)
}

func (r Runner) syncAll(ctx context.Context) {
	registry := r.Sources
	if r.SourceLoad != nil {
		if loaded, err := r.SourceLoad(ctx); err == nil {
			registry = loaded
		}
	}
	for _, ad := range registry.Adapters {
		started := time.Now().UTC()
		r.logKV("worker_sync_all_start", "source", ad.Key(), "trigger", "manual_all")
		items, msg, err := ad.Fetch(ctx)
		status := "success"
		if err != nil {
			status = "failed"
			msg = err.Error()
			r.logKV("worker_sync_all_failed", "source", ad.Key(), "error", err)
		}
		r.persistSyncRun(ctx, models.SyncRun{SourceKey: ad.Key(), StartedAt: started, FinishedAt: r.now(), Status: status, Message: msg, Trigger: "manual_all", ItemCount: len(items)})
		r.persistSourceHealth(ctx, models.SourceHealth{SourceKey: ad.Key(), LastSyncAt: r.now(), LastCheckedAt: r.now(), LastStatus: status, LastMessage: msg, LastItemCount: len(items), HealthStatus: status})
		for _, t := range items {
			if t.DocumentStatus == "" {
				t.DocumentStatus = models.ExtractionQueued
			}
			r.persistTender(ctx, t)
			if t.DocumentURL != "" {
				r.persistQueuedJob(ctx, models.ExtractionJob{TenderID: t.ID, DocumentURL: t.DocumentURL, State: models.ExtractionQueued})
			}
		}
	}
}
func (r Runner) processJobs(ctx context.Context) {
	jobs, err := r.Store.ListJobs(ctx)
	if err != nil {
		r.logKV("worker_jobs_list_failed", "error", err)
		return
	}
	for _, job := range jobs {
		if strings.TrimSpace(job.TenderID) == "" {
			r.logKV("worker_job_prune_missing_tender_id", "job", job.ID)
			r.deleteJob(ctx, job.ID)
			continue
		}
		if _, err := r.Store.GetTender(ctx, job.TenderID); err != nil {
			r.logKV("worker_job_prune_missing_tender", "job", job.ID, "tender", job.TenderID)
			r.deleteJob(ctx, job.ID)
			continue
		}
		if !(job.State == models.ExtractionQueued || job.State == models.ExtractionRetry) || r.now().Before(job.NextAttemptAt) {
			continue
		}
		job.State = models.ExtractionProcessing
		job.Attempts++
		if !r.persistJobUpdate(ctx, job) {
			continue
		}
		if r.Extractor == nil || strings.TrimSpace(r.Extractor.BaseURL) == "" {
			job.State = models.ExtractionFailed
			job.LastError = "extractor client is not configured"
			job.NextAttemptAt = time.Time{}
			r.persistJobUpdate(ctx, job)
			r.updateTenderDocumentState(ctx, job.TenderID, models.ExtractionFailed)
			r.logKV("worker_job_extractor_unconfigured", "job", job.ID, "tender", job.TenderID)
			continue
		}
		if _, err := netguard.NormalizePublicHTTPURL(job.DocumentURL); err != nil {
			job.State = models.ExtractionFailed
			job.LastError = err.Error()
			job.NextAttemptAt = time.Time{}
			r.persistJobUpdate(ctx, job)
			r.updateTenderDocumentState(ctx, job.TenderID, models.ExtractionFailed)
			r.logKV("worker_job_extract_rejected_url", "job", job.ID, "tender", job.TenderID, "error", err)
			continue
		}
		res, err := r.Extractor.Extract(ctx, job.DocumentURL)
		if err != nil {
			job.State = models.ExtractionRetry
			if job.Attempts >= 3 {
				job.State = models.ExtractionFailed
			}
			job.LastError = err.Error()
			if job.State == models.ExtractionFailed {
				job.NextAttemptAt = time.Time{}
			} else {
				job.NextAttemptAt = r.now().Add(time.Duration(job.Attempts*job.Attempts) * time.Minute)
			}
			r.persistJobUpdate(ctx, job)
			if job.State == models.ExtractionFailed {
				r.updateTenderDocumentState(ctx, job.TenderID, models.ExtractionFailed)
			} else {
				r.updateTenderDocumentState(ctx, job.TenderID, models.ExtractionRetry)
			}
			r.logKV("worker_job_extract_failed", "job", job.ID, "tender", job.TenderID, "attempts", job.Attempts, "state", job.State, "error", err)
			continue
		}
		if t, err := r.Store.GetTender(ctx, job.TenderID); err == nil {
			t.DocumentStatus = models.ExtractionCompleted
			t.Excerpt = res.Excerpt
			t.DocumentFacts = cloneFactMap(res.Facts)
			applyDocumentPromotions(&t, t.DocumentFacts)
			t.ExtractedFacts = mergeFactMaps(t.ExtractedFacts, t.PageFacts, t.DocumentFacts)
			r.persistTender(ctx, t)
		} else {
			r.logKV("worker_tender_lookup_failed", "tender", job.TenderID, "error", err)
		}
		job.State = models.ExtractionCompleted
		job.NextAttemptAt = time.Time{}
		r.persistJobUpdate(ctx, job)
		r.logKV("worker_job_extract_completed", "job", job.ID, "tender", job.TenderID, "attempts", job.Attempts)
	}
}

func (r Runner) processSourceChecks(ctx context.Context) {
	configs, err := r.Store.ListSourceConfigs(ctx)
	if err != nil {
		r.logKV("worker_source_configs_list_failed", "error", err)
		return
	}
	settings := r.loadScheduleSettings(ctx)
	healths, err := r.Store.ListSourceHealth(ctx)
	if err != nil {
		r.logKV("worker_source_health_list_failed", "error", err)
		return
	}
	healthByKey := map[string]models.SourceHealth{}
	for _, health := range healths {
		healthByKey[health.SourceKey] = health
	}
	now := r.now()
	for _, cfg := range configs {
		health := healthByKey[cfg.Key]
		health.SourceKey = cfg.Key
		if !cfg.Enabled && health.PendingManualCheck {
			health.PendingManualCheck = false
			health.LastStatus = "skipped"
			health.LastMessage = "Manual check skipped because the source is disabled."
			health.HealthStatus = source.ComputeHealthStatus(cfg, settings, health)
			r.persistSourceHealth(ctx, health)
			continue
		}
		trigger, due := dueSourceTrigger(now, cfg, settings, health)
		if !due {
			continue
		}
		r.runSourceCheck(ctx, cfg, settings, health, trigger)
	}
}

func (r Runner) runSourceCheck(ctx context.Context, cfg models.SourceConfig, settings models.SourceScheduleSettings, health models.SourceHealth, trigger string) {
	started := r.now()
	health.SourceKey = cfg.Key
	health.Running = true
	if trigger == "manual" {
		health.PendingManualCheck = false
	}
	health.LastTrigger = trigger
	health.LastStatus = "running"
	health.LastMessage = "Checking source now."
	health.HealthStatus = source.ComputeHealthStatus(cfg, settings, health)
	r.persistSourceHealth(ctx, health)
	r.logKV("worker_source_check_started", "source", cfg.Key, "trigger", trigger)

	adapter, err := source.AdapterFromConfig(cfg)
	if err != nil {
		r.finalizeSourceCheck(ctx, cfg, settings, health, trigger, started, nil, err.Error(), err)
		return
	}
	items, msg, fetchErr := adapter.Fetch(ctx)
	r.finalizeSourceCheck(ctx, cfg, settings, health, trigger, started, items, msg, fetchErr)
}

func (r Runner) finalizeSourceCheck(ctx context.Context, cfg models.SourceConfig, settings models.SourceScheduleSettings, health models.SourceHealth, trigger string, started time.Time, items []models.Tender, msg string, runErr error) {
	finished := r.now()
	status := "success"
	if runErr != nil {
		status = "failed"
		if strings.TrimSpace(msg) == "" {
			msg = runErr.Error()
		}
	}
	if strings.TrimSpace(msg) == "" {
		msg = "Source check completed."
	}

	latest, err := r.Store.GetSourceHealth(ctx, cfg.Key)
	if err == nil && latest.PendingManualCheck {
		health.PendingManualCheck = true
	}
	health.Running = false
	health.LastSyncAt = finished
	health.LastCheckedAt = finished
	health.LastStatus = status
	health.LastMessage = msg
	health.LastItemCount = len(items)
	health.LastTrigger = trigger
	if runErr != nil {
		health.ConsecutiveFailures++
	} else {
		health.ConsecutiveFailures = 0
		health.LastSuccessfulCheckAt = finished
	}
	if trigger == "scheduled" || health.NextScheduledCheckAt.IsZero() || !health.NextScheduledCheckAt.After(started) {
		health.NextScheduledCheckAt = source.NextScheduledCheckAt(finished, cfg, settings, health.ConsecutiveFailures, runErr == nil)
	}
	health.HealthStatus = source.ComputeHealthStatus(cfg, settings, health)
	r.persistSyncRun(ctx, models.SyncRun{
		SourceKey:  cfg.Key,
		StartedAt:  started,
		FinishedAt: finished,
		Status:     status,
		Message:    msg,
		Trigger:    trigger,
		ItemCount:  len(items),
	})
	r.persistSourceHealth(ctx, health)
	r.logKV("worker_source_check_finished", "source", cfg.Key, "trigger", trigger, "status", status, "items", len(items), "failures", health.ConsecutiveFailures)
	if runErr != nil {
		return
	}
	for _, t := range items {
		if t.DocumentStatus == "" {
			t.DocumentStatus = models.ExtractionQueued
		}
		r.persistTender(ctx, t)
		if t.DocumentURL != "" {
			r.persistQueuedJob(ctx, models.ExtractionJob{TenderID: t.ID, DocumentURL: t.DocumentURL, State: models.ExtractionQueued})
		}
	}
}

func dueSourceTrigger(now time.Time, cfg models.SourceConfig, settings models.SourceScheduleSettings, health models.SourceHealth) (string, bool) {
	if !cfg.Enabled || health.Running {
		return "", false
	}
	if cfg.ManualChecksEnabled && health.PendingManualCheck {
		return "manual", true
	}
	if !source.ShouldAutoCheck(cfg, settings) {
		return "", false
	}
	if health.NextScheduledCheckAt.IsZero() || !health.NextScheduledCheckAt.After(now) {
		return "scheduled", true
	}
	return "", false
}

func (r Runner) nextWait(ctx context.Context) time.Duration {
	maxIdle := r.LoopEvery
	if maxIdle <= 0 {
		maxIdle = 30 * time.Second
	}
	now := r.now()
	next := now.Add(maxIdle)
	settings := r.loadScheduleSettings(ctx)
	configs, err := r.Store.ListSourceConfigs(ctx)
	if err == nil {
		healths, _ := r.Store.ListSourceHealth(ctx)
		healthByKey := map[string]models.SourceHealth{}
		for _, health := range healths {
			healthByKey[health.SourceKey] = health
		}
		for _, cfg := range configs {
			health := healthByKey[cfg.Key]
			if cfg.Enabled && cfg.ManualChecksEnabled && health.PendingManualCheck {
				return time.Second
			}
			if !source.ShouldAutoCheck(cfg, settings) {
				continue
			}
			dueAt := health.NextScheduledCheckAt
			if dueAt.IsZero() {
				dueAt = source.InitialNextCheckAt(now, cfg, settings)
			}
			if dueAt.Before(next) {
				next = dueAt
			}
		}
	}
	jobs, err := r.Store.ListJobs(ctx)
	if err == nil {
		for _, job := range jobs {
			if !(job.State == models.ExtractionQueued || job.State == models.ExtractionRetry) {
				continue
			}
			if !job.NextAttemptAt.After(now) {
				return time.Second
			}
			if job.NextAttemptAt.Before(next) {
				next = job.NextAttemptAt
			}
		}
	}
	wait := time.Until(next)
	if wait < time.Second {
		return time.Second
	}
	if wait > maxIdle {
		return maxIdle
	}
	return wait
}

func (r Runner) resetRunningSources(ctx context.Context) error {
	healths, err := r.Store.ListSourceHealth(ctx)
	if err != nil {
		return err
	}
	configs, _ := r.Store.ListSourceConfigs(ctx)
	configByKey := map[string]models.SourceConfig{}
	for _, cfg := range configs {
		configByKey[cfg.Key] = cfg
	}
	settings := r.loadScheduleSettings(ctx)
	for _, health := range healths {
		if !health.Running {
			continue
		}
		health.Running = false
		cfg := configByKey[health.SourceKey]
		health.HealthStatus = source.ComputeHealthStatus(cfg, settings, health)
		if err := r.Store.UpsertSourceHealth(ctx, health); err != nil {
			return err
		}
	}
	return nil
}

func (r Runner) loadScheduleSettings(ctx context.Context) models.SourceScheduleSettings {
	settings, err := r.Store.GetSourceScheduleSettings(ctx)
	if err != nil {
		fallback := int(r.SyncEvery / time.Minute)
		if fallback <= 0 {
			fallback = source.DefaultCheckIntervalMinutes
		}
		return source.NormalizeScheduleSettings(models.SourceScheduleSettings{ID: "global", DefaultIntervalMinutes: fallback}, fallback)
	}
	return source.NormalizeScheduleSettings(settings, int(r.SyncEvery/time.Minute))
}

func (r Runner) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

func cloneFactMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func mergeFactMaps(parts ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, part := range parts {
		for k, v := range part {
			if v != "" {
				out[k] = v
			}
		}
	}
	return out
}

func applyDocumentPromotions(t *models.Tender, facts map[string]string) {
	if len(facts) == 0 {
		return
	}
	if t.Title == "" || placeholderTenderTitle(t.Title) {
		setString(&t.Title, facts["document_title"])
	}
	setStringIfEmpty(&t.PublishedDate, facts["issued_date"])
	setStringIfEmpty(&t.ClosingDate, firstNonEmpty(facts["closing_datetime"], facts["closing_date"]))
	setStringIfEmpty(&t.CIDBGrading, facts["cidb_grade"])
	if t.ValidityDays == 0 {
		t.ValidityDays = parseInt(facts["validity_days"])
	}
	if t.Scope == "" {
		setStringIfEmpty(&t.Scope, facts["document_title"])
	}
	if t.Summary == "" || placeholderTenderTitle(t.Summary) {
		setString(&t.Summary, facts["document_title"])
	}

	if t.Submission.Method == "" && facts["submission_address"] != "" {
		t.Submission.Method = "physical"
	}
	setStringIfEmpty(&t.Submission.Address, facts["submission_address"])
	if facts["submission_address"] != "" {
		t.Submission.PhysicalAllowed = true
	}

	setStringIfEmpty(&t.Location.Town, facts["location_city"])
	setStringIfEmpty(&t.Location.Province, facts["location_province"])
	if t.Province == "" {
		setStringIfEmpty(&t.Province, facts["location_province"])
	}

	setStringIfEmpty(&t.Evaluation.Method, facts["evaluation_method"])
	if t.Evaluation.PricePoints == 0 {
		t.Evaluation.PricePoints = parseInt(facts["price_points"])
	}
	if t.Evaluation.PreferencePoints == 0 {
		t.Evaluation.PreferencePoints = parseInt(facts["preference_points"])
	}
	if t.Evaluation.MinimumFunctionalityScore == 0 {
		t.Evaluation.MinimumFunctionalityScore = parseFloat(facts["minimum_functionality_score"])
	}

	contact := models.TenderContact{
		Role:      "document_contact",
		Name:      strings.TrimSpace(facts["contact_name"]),
		Email:     strings.TrimSpace(facts["contact_email"]),
		Telephone: strings.TrimSpace(facts["contact_phone"]),
	}
	if hasContactData(contact) && !containsContact(t.Contacts, contact) {
		t.Contacts = append(t.Contacts, contact)
	}

	briefing := models.TenderBriefing{
		Label:    "document_briefing",
		DateTime: firstNonEmpty(facts["briefing_datetime"], joinDateTime(facts["briefing_date"], facts["briefing_time"])),
		Venue:    strings.TrimSpace(facts["briefing_venue"]),
		Address:  strings.TrimSpace(facts["briefing_venue"]),
		Required: parseBoolYesNo(facts["briefing_required"]),
	}
	if briefing.DateTime != "" || briefing.Venue != "" || briefing.Address != "" || briefing.Required {
		briefing.Notes = "Promoted from document extraction"
	}
	if hasBriefingData(briefing) && !containsBriefing(t.Briefings, briefing) {
		t.Briefings = append(t.Briefings, briefing)
	}
}

func placeholderTenderTitle(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(normalized, "dpwi tender ") || strings.HasPrefix(normalized, "public works tender ")
}

func setStringIfEmpty(target *string, value string) {
	if strings.TrimSpace(*target) != "" {
		return
	}
	setString(target, value)
}

func setString(target *string, value string) {
	*target = strings.TrimSpace(value)
}

func parseInt(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return parsed
}

func parseFloat(value string) float64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func joinDateTime(date, clock string) string {
	date = strings.TrimSpace(date)
	clock = strings.TrimSpace(clock)
	if date == "" {
		return ""
	}
	if clock == "" {
		return date
	}
	return date + " " + clock
}

func parseBoolYesNo(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "yes" || value == "true" || value == "required"
}

func hasContactData(contact models.TenderContact) bool {
	return contact.Name != "" || contact.Email != "" || contact.Telephone != "" || contact.Fax != "" || contact.Mobile != ""
}

func containsContact(existing []models.TenderContact, candidate models.TenderContact) bool {
	key := strings.ToLower(strings.Join([]string{
		strings.TrimSpace(candidate.Name),
		strings.TrimSpace(candidate.Email),
		strings.TrimSpace(candidate.Telephone),
	}, "|"))
	if key == "||" {
		return false
	}
	for _, item := range existing {
		existingKey := strings.ToLower(strings.Join([]string{
			strings.TrimSpace(item.Name),
			strings.TrimSpace(item.Email),
			strings.TrimSpace(item.Telephone),
		}, "|"))
		if existingKey == key {
			return true
		}
	}
	return false
}

func hasBriefingData(briefing models.TenderBriefing) bool {
	return briefing.DateTime != "" || briefing.Venue != "" || briefing.Address != "" || briefing.Notes != "" || briefing.Required
}

func containsBriefing(existing []models.TenderBriefing, candidate models.TenderBriefing) bool {
	key := strings.ToLower(strings.Join([]string{
		strings.TrimSpace(candidate.DateTime),
		strings.TrimSpace(candidate.Venue),
		strings.TrimSpace(candidate.Address),
	}, "|"))
	for _, item := range existing {
		existingKey := strings.ToLower(strings.Join([]string{
			strings.TrimSpace(item.DateTime),
			strings.TrimSpace(item.Venue),
			strings.TrimSpace(item.Address),
		}, "|"))
		if existingKey == key && key != "||" {
			return true
		}
	}
	return false
}
