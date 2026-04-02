package worker

import (
	"context"
	"tenderhub-za/internal/extract"
	"tenderhub-za/internal/models"
	"tenderhub-za/internal/source"
	"tenderhub-za/internal/store"
	"time"
)

type Runner struct {
	Store                store.Store
	Sources              source.Registry
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
	for _, ad := range r.Sources.Adapters {
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
			t.ExtractedFacts = res.Facts
			_ = r.Store.UpsertTender(ctx, t)
		}
		job.State = models.ExtractionCompleted
		job.NextAttemptAt = time.Time{}
		_ = r.Store.UpdateJob(ctx, job)
	}
}
