package worker

import (
	"context"
	"strconv"
	"strings"
	"tenderhub-za/internal/extract"
	"tenderhub-za/internal/models"
	"tenderhub-za/internal/source"
	"tenderhub-za/internal/store"
	"time"
)

type Runner struct {
	Store                store.Store
	Sources              source.Registry
	SourceLoad           func(context.Context) (source.Registry, error)
	Extractor            *extract.Client
	SyncEvery, LoopEvery time.Duration
	Now                  func() time.Time
}

func (r Runner) Run(ctx context.Context) error {
	_ = r.resetRunningSources(ctx)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		r.processSourceChecks(ctx)
		r.processJobs(ctx)
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

func (r Runner) syncAll(ctx context.Context) {
	registry := r.Sources
	if r.SourceLoad != nil {
		if loaded, err := r.SourceLoad(ctx); err == nil {
			registry = loaded
		}
	}
	for _, ad := range registry.Adapters {
		started := time.Now().UTC()
		items, msg, err := ad.Fetch(ctx)
		status := "success"
		if err != nil {
			status = "failed"
			msg = err.Error()
		}
		_ = r.Store.AddSyncRun(ctx, models.SyncRun{SourceKey: ad.Key(), StartedAt: started, FinishedAt: r.now(), Status: status, Message: msg, Trigger: "manual_all", ItemCount: len(items)})
		_ = r.Store.UpsertSourceHealth(ctx, models.SourceHealth{SourceKey: ad.Key(), LastSyncAt: r.now(), LastCheckedAt: r.now(), LastStatus: status, LastMessage: msg, LastItemCount: len(items), HealthStatus: status})
		for _, t := range items {
			if t.DocumentStatus == "" {
				t.DocumentStatus = models.ExtractionQueued
			}
			_ = r.Store.UpsertTender(ctx, t)
			if t.DocumentURL != "" {
				_ = r.Store.QueueJob(ctx, models.ExtractionJob{TenderID: t.ID, DocumentURL: t.DocumentURL, State: models.ExtractionQueued})
			}
		}
	}
}
func (r Runner) processJobs(ctx context.Context) {
	jobs, err := r.Store.ListJobs(ctx)
	if err != nil {
		return
	}
	for _, job := range jobs {
		if !(job.State == models.ExtractionQueued || job.State == models.ExtractionRetry) || r.now().Before(job.NextAttemptAt) {
			continue
		}
		job.State = models.ExtractionProcessing
		job.Attempts++
		_ = r.Store.UpdateJob(ctx, job)
		res, err := r.Extractor.Extract(ctx, job.DocumentURL)
		if err != nil {
			job.State = models.ExtractionRetry
			if job.Attempts >= 3 {
				job.State = models.ExtractionFailed
			}
			job.LastError = err.Error()
			job.NextAttemptAt = r.now().Add(time.Duration(job.Attempts*job.Attempts) * time.Minute)
			_ = r.Store.UpdateJob(ctx, job)
			continue
		}
		if t, err := r.Store.GetTender(ctx, job.TenderID); err == nil {
			t.DocumentStatus = models.ExtractionCompleted
			t.Excerpt = res.Excerpt
			t.DocumentFacts = cloneFactMap(res.Facts)
			applyDocumentPromotions(&t, t.DocumentFacts)
			t.ExtractedFacts = mergeFactMaps(t.ExtractedFacts, t.PageFacts, t.DocumentFacts)
			_ = r.Store.UpsertTender(ctx, t)
		}
		job.State = models.ExtractionCompleted
		job.NextAttemptAt = time.Time{}
		_ = r.Store.UpdateJob(ctx, job)
	}
}

func (r Runner) processSourceChecks(ctx context.Context) {
	configs, err := r.Store.ListSourceConfigs(ctx)
	if err != nil {
		return
	}
	settings := r.loadScheduleSettings(ctx)
	healths, _ := r.Store.ListSourceHealth(ctx)
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
			_ = r.Store.UpsertSourceHealth(ctx, health)
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
	_ = r.Store.UpsertSourceHealth(ctx, health)

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
	_ = r.Store.AddSyncRun(ctx, models.SyncRun{
		SourceKey:  cfg.Key,
		StartedAt:  started,
		FinishedAt: finished,
		Status:     status,
		Message:    msg,
		Trigger:    trigger,
		ItemCount:  len(items),
	})
	_ = r.Store.UpsertSourceHealth(ctx, health)
	if runErr != nil {
		return
	}
	for _, t := range items {
		if t.DocumentStatus == "" {
			t.DocumentStatus = models.ExtractionQueued
		}
		_ = r.Store.UpsertTender(ctx, t)
		if t.DocumentURL != "" {
			_ = r.Store.QueueJob(ctx, models.ExtractionJob{TenderID: t.ID, DocumentURL: t.DocumentURL, State: models.ExtractionQueued})
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
