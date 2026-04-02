package worker

import (
	"context"
	"path/filepath"
	"tenderhub-za/internal/extract"
	"tenderhub-za/internal/models"
	"tenderhub-za/internal/source"
	"tenderhub-za/internal/store"
	"testing"
	"time"
)

func TestProcessJobsRetry(t *testing.T) {
	s, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.UpsertTender(context.Background(), models.Tender{ID: "1", Title: "Civil", DocumentURL: "http://127.0.0.1:1/fail", DocumentStatus: models.ExtractionQueued})
	_ = s.QueueJob(context.Background(), models.ExtractionJob{ID: "j1", TenderID: "1", DocumentURL: "http://127.0.0.1:1/fail", State: models.ExtractionQueued})
	r := Runner{Store: s, Sources: source.NewRegistry(), Extractor: extract.New("http://127.0.0.1:1"), SyncEvery: time.Hour, LoopEvery: time.Millisecond}
	r.processJobs(context.Background())
	jobs, _ := s.ListJobs(context.Background())
	if len(jobs) == 0 || (jobs[0].State != models.ExtractionRetry && jobs[0].State != models.ExtractionFailed) {
		t.Fatal("expected retry or failed state")
	}
}
