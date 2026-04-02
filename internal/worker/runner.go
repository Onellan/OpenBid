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
}

func (r Runner) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.LoopEvery)
	defer ticker.Stop()
	lastSync := time.Time{}
	for {
		if lastSync.IsZero() || time.Since(lastSync) >= r.SyncEvery {
			r.syncAll(ctx)
			lastSync = time.Now()
		}
		r.processJobs(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
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
		_ = r.Store.AddSyncRun(ctx, models.SyncRun{SourceKey: ad.Key(), StartedAt: started, FinishedAt: time.Now().UTC(), Status: status, Message: msg})
		_ = r.Store.UpsertSourceHealth(ctx, models.SourceHealth{SourceKey: ad.Key(), LastSyncAt: time.Now().UTC(), LastStatus: status, LastMessage: msg, LastItemCount: len(items)})
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
		if !(job.State == models.ExtractionQueued || job.State == models.ExtractionRetry) || time.Now().Before(job.NextAttemptAt) {
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
			job.NextAttemptAt = time.Now().Add(time.Duration(job.Attempts*job.Attempts) * time.Minute)
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
