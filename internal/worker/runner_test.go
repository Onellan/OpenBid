package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"openbid/internal/extract"
	"openbid/internal/models"
	"openbid/internal/source"
	"openbid/internal/store"
	"openbid/internal/tenderstate"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func allowPrivateURLs(t *testing.T) {
	t.Helper()
	t.Setenv("OPENBID_ALLOW_PRIVATE_URLS", "true")
}

func TestWriteHeartbeatCreatesFreshHeartbeatFile(t *testing.T) {
	heartbeatPath := filepath.Join(t.TempDir(), "worker", "heartbeat")
	now := time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC)
	r := Runner{
		HeartbeatPath: heartbeatPath,
		Now:           func() time.Time { return now },
	}

	r.writeHeartbeat()

	payload, err := os.ReadFile(heartbeatPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(payload)) != now.Format(time.RFC3339Nano) {
		t.Fatalf("unexpected heartbeat payload: %q", string(payload))
	}
}

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

func TestProcessJobsMarksFinalFailureAndClearsNextAttempt(t *testing.T) {
	s, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.UpsertTender(ctx, models.Tender{
		ID:             "final-failure",
		Title:          "Civil",
		DocumentURL:    "http://127.0.0.1:1/fail",
		DocumentStatus: models.ExtractionQueued,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.QueueJob(ctx, models.ExtractionJob{
		ID:            "j-final",
		TenderID:      "final-failure",
		DocumentURL:   "http://127.0.0.1:1/fail",
		State:         models.ExtractionRetry,
		Attempts:      2,
		NextAttemptAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	r := Runner{Store: s, Sources: source.NewRegistry(), Extractor: extract.New("http://127.0.0.1:1"), SyncEvery: time.Hour, LoopEvery: time.Millisecond}
	r.processJobs(ctx)

	jobs, err := s.ListJobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].State != models.ExtractionFailed {
		t.Fatalf("expected failed job, got %#v", jobs)
	}
	if !jobs[0].NextAttemptAt.IsZero() {
		t.Fatalf("expected terminal failure to clear retry schedule, got %#v", jobs[0])
	}
	tender, err := s.GetTender(ctx, "final-failure")
	if err != nil {
		t.Fatal(err)
	}
	if tender.DocumentStatus != models.ExtractionFailed {
		t.Fatalf("expected tender status to reflect failure, got %#v", tender)
	}
}

func TestProcessJobsRunsExpiredTenderCleanupMaintenanceJob(t *testing.T) {
	s, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	if err := s.UpsertTender(ctx, models.Tender{ID: "expired-cleanup", Title: "Expired", ClosingDate: now.Add(-48 * time.Hour).Format("2006-01-02 15:04")}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertTender(ctx, models.Tender{ID: "active-cleanup", Title: "Active", ClosingDate: now.Add(48 * time.Hour).Format("2006-01-02 15:04")}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertBookmark(ctx, models.Bookmark{TenantID: "tenant-1", UserID: "user-1", TenderID: "expired-cleanup", Note: "preserve"}); err != nil {
		t.Fatal(err)
	}
	if err := s.QueueJob(ctx, models.ExtractionJob{
		ID:       models.ExpiredTenderCleanupJobID,
		JobType:  models.JobTypeExpiredTenderCleanup,
		JobName:  models.ExpiredTenderCleanupJobName,
		TenantID: "tenant-1",
		UserID:   "user-1",
		State:    models.ExtractionQueued,
	}); err != nil {
		t.Fatal(err)
	}

	r := Runner{Store: s}
	r.processJobs(ctx)

	if _, err := s.GetTender(ctx, "expired-cleanup"); err != store.ErrNotFound {
		t.Fatalf("expected expired tender hidden after cleanup, got %v", err)
	}
	if _, err := s.GetTender(ctx, "active-cleanup"); err != nil {
		t.Fatalf("expected active tender preserved, got %v", err)
	}
	bookmarks, err := s.ListBookmarks(ctx, "tenant-1", "user-1")
	if err != nil || len(bookmarks) != 1 || bookmarks[0].TenderID != "expired-cleanup" {
		t.Fatalf("expected bookmark to be preserved, bookmarks=%#v err=%v", bookmarks, err)
	}
	jobs, err := s.ListJobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].State != models.ExtractionCompleted || !strings.Contains(jobs[0].ResultSummary, "Removed 1 expired tenders") {
		t.Fatalf("expected completed cleanup job with result summary, got %#v", jobs)
	}
	entries, err := s.ListAuditEntries(ctx, "tenant-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Action != "cleanup" || entries[0].Metadata["removed_count"] != "1" {
		t.Fatalf("expected cleanup audit entry, got %#v", entries)
	}
}

func TestProcessJobsPrunesOrphanJobs(t *testing.T) {
	s, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.QueueJob(context.Background(), models.ExtractionJob{ID: "orphan", TenderID: "missing", DocumentURL: "https://example.org/doc.pdf", State: models.ExtractionQueued})
	r := Runner{Store: s, Sources: source.NewRegistry(), Extractor: extract.New("http://127.0.0.1:1"), SyncEvery: time.Hour, LoopEvery: time.Millisecond}
	r.processJobs(context.Background())
	jobs, err := s.ListJobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("expected orphan job to be removed, got %#v", jobs)
	}
}

func TestProcessJobsRejectsUnsafeDocumentURL(t *testing.T) {
	s, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.UpsertTender(ctx, models.Tender{ID: "unsafe", Title: "Unsafe", DocumentURL: "http://127.0.0.1/doc.pdf", DocumentStatus: models.ExtractionQueued}); err != nil {
		t.Fatal(err)
	}
	if err := s.QueueJob(ctx, models.ExtractionJob{ID: "unsafe-job", TenderID: "unsafe", DocumentURL: "http://127.0.0.1/doc.pdf", State: models.ExtractionQueued}); err != nil {
		t.Fatal(err)
	}

	r := Runner{Store: s, Sources: source.NewRegistry(), Extractor: extract.New("http://127.0.0.1:1"), SyncEvery: time.Hour, LoopEvery: time.Millisecond}
	r.processJobs(ctx)

	jobs, err := s.ListJobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].State != models.ExtractionFailed {
		t.Fatalf("expected unsafe url job to fail immediately, got %#v", jobs)
	}
	if !jobs[0].NextAttemptAt.IsZero() {
		t.Fatalf("expected no retry window for rejected url, got %#v", jobs[0])
	}
	tender, err := s.GetTender(ctx, "unsafe")
	if err != nil {
		t.Fatal(err)
	}
	if tender.DocumentStatus != models.ExtractionFailed {
		t.Fatalf("expected tender marked failed, got %#v", tender)
	}
}

func TestProcessJobsSkipsExpiredPDFAndHTMLExtraction(t *testing.T) {
	allowPrivateURLs(t)
	for _, tc := range []struct {
		name string
		path string
	}{
		{name: "pdf", path: "/expired.pdf"},
		{name: "html", path: "/expired.html"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			ctx := context.Background()
			now := time.Date(2026, 4, 11, 12, 0, 0, 0, time.UTC)
			var requests int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&requests, 1)
				_ = json.NewEncoder(w).Encode(map[string]any{"excerpt": "should not run"})
			}))
			defer server.Close()
			tenderID := "expired-" + tc.name
			docURL := server.URL + tc.path
			if err := s.UpsertTender(ctx, models.Tender{
				ID:             tenderID,
				Title:          "Expired " + tc.name,
				ClosingDate:    "2026-04-10 11:00",
				DocumentURL:    docURL,
				DocumentStatus: models.ExtractionQueued,
			}); err != nil {
				t.Fatal(err)
			}
			if err := s.QueueJob(ctx, models.ExtractionJob{ID: "job-" + tc.name, TenderID: tenderID, DocumentURL: docURL, State: models.ExtractionQueued, NextAttemptAt: now.Add(-time.Minute)}); err != nil {
				t.Fatal(err)
			}

			r := Runner{Store: s, Extractor: extract.New(server.URL), SyncEvery: time.Hour, LoopEvery: time.Millisecond, Now: func() time.Time { return now }}
			r.processJobs(ctx)

			if got := atomic.LoadInt32(&requests); got != 0 {
				t.Fatalf("expected expired tender not to call extractor, got %d requests", got)
			}
			jobs, err := s.ListJobs(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(jobs) != 1 || jobs[0].State != models.ExtractionSkipped || jobs[0].SkipReason != tenderstate.ExpiredSkipReason || jobs[0].Attempts != 0 {
				t.Fatalf("expected skipped job without attempts, got %#v", jobs)
			}
			tender, err := s.GetTender(ctx, tenderID)
			if err != nil {
				t.Fatal(err)
			}
			if tender.DocumentStatus != models.ExtractionSkipped || tender.ExtractionSkippedReason != tenderstate.ExpiredSkipReason {
				t.Fatalf("expected tender skipped due to expiry, got %#v", tender)
			}
		})
	}
}

func TestSyncAllUsesDynamicSourceLoader(t *testing.T) {
	s, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := Runner{
		Store: s,
		SourceLoad: func(context.Context) (source.Registry, error) {
			return source.RegistryFromConfigs([]models.SourceConfig{{
				Key:     "municipal-feed",
				Name:    "Municipal Feed",
				Type:    source.TypeJSONFeed,
				Enabled: true,
			}}), nil
		},
		Extractor: extract.New("http://127.0.0.1:1"),
	}
	r.syncAll(context.Background())
	health, err := s.ListSourceHealth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range health {
		if item.SourceKey == "municipal-feed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected dynamic source health entry, got %#v", health)
	}
}

func TestProcessJobsMergesPageAndDocumentFacts(t *testing.T) {
	s, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"excerpt": "Document summary",
			"facts": map[string]string{
				"document_title":              "Supply, installation and maintenance of network cabling",
				"closing_date":                "2026-05-05",
				"issued_date":                 "2026-03-31",
				"validity_days":               "90",
				"cidb_hints":                  "CIDB 3GB",
				"cidb_grade":                  "3GB",
				"submission_details":          "sealed envelope at Komani office",
				"submission_address":          "1267 Gordon Hood Road, Centurion, Pretoria, South Africa",
				"contact_name":                "Ulizwi Mngoma",
				"contact_email":               "ulizwim@cidb.org.za",
				"contact_phone":               "012 482 7252",
				"briefing_required":           "yes",
				"briefing_date":               "2026-04-08",
				"briefing_time":               "10:00",
				"briefing_venue":              "191 Madiba Street, Pretoria",
				"location_city":               "Centurion",
				"location_province":           "Gauteng",
				"evaluation_method":           "80/20",
				"price_points":                "80",
				"preference_points":           "20",
				"minimum_functionality_score": "60",
			},
			"type": "pdf",
		})
	}))
	defer server.Close()

	tender := models.Tender{
		ID:             "1",
		Title:          "DPWI tender PT25/020",
		DocumentURL:    "https://example.org/doc.pdf",
		DocumentStatus: models.ExtractionQueued,
		PageFacts: map[string]string{
			"contact_details":  "person=Jane Doe",
			"closing_details":  "2026-04-17 11:00",
			"submission_notes": "listing note",
		},
		ExtractedFacts: map[string]string{
			"legacy_fact": "keep me",
		},
	}
	_ = s.UpsertTender(context.Background(), tender)
	_ = s.QueueJob(context.Background(), models.ExtractionJob{ID: "j1", TenderID: "1", DocumentURL: "https://example.org/doc.pdf", State: models.ExtractionQueued})

	r := Runner{Store: s, Sources: source.NewRegistry(), Extractor: extract.New(server.URL), SyncEvery: time.Hour, LoopEvery: time.Millisecond}
	r.processJobs(context.Background())

	updated, err := s.GetTender(context.Background(), "1")
	if err != nil {
		t.Fatal(err)
	}
	if updated.DocumentStatus != models.ExtractionCompleted {
		t.Fatalf("expected completed document status, got %s", updated.DocumentStatus)
	}
	if updated.PageFacts["contact_details"] == "" || updated.DocumentFacts["cidb_hints"] == "" {
		t.Fatalf("expected separated fact maps, got page=%#v doc=%#v", updated.PageFacts, updated.DocumentFacts)
	}
	if updated.ExtractedFacts["contact_details"] == "" || updated.ExtractedFacts["cidb_hints"] == "" || updated.ExtractedFacts["legacy_fact"] == "" {
		t.Fatalf("expected merged extracted facts, got %#v", updated.ExtractedFacts)
	}
	if updated.Title != "Supply, installation and maintenance of network cabling" {
		t.Fatalf("expected promoted title, got %#v", updated.Title)
	}
	if updated.PublishedDate != "2026-03-31" {
		t.Fatalf("expected issued date promotion, got %#v", updated.PublishedDate)
	}
	if updated.ClosingDate != "2026-05-05" {
		t.Fatalf("expected closing date promotion, got %#v", updated.ClosingDate)
	}
	if updated.ValidityDays != 90 || updated.CIDBGrading != "3GB" {
		t.Fatalf("expected validity/CIDB promotions, got days=%d cidb=%q", updated.ValidityDays, updated.CIDBGrading)
	}
	if updated.Submission.Address != "1267 Gordon Hood Road, Centurion, Pretoria, South Africa" || !updated.Submission.PhysicalAllowed {
		t.Fatalf("expected submission promotion, got %#v", updated.Submission)
	}
	if updated.Location.Town != "Centurion" || updated.Location.Province != "Gauteng" || updated.Province != "Gauteng" {
		t.Fatalf("expected location promotion, got location=%#v province=%q", updated.Location, updated.Province)
	}
	if updated.Evaluation.Method != "80/20" || updated.Evaluation.PricePoints != 80 || updated.Evaluation.PreferencePoints != 20 || updated.Evaluation.MinimumFunctionalityScore != 60 {
		t.Fatalf("expected evaluation promotion, got %#v", updated.Evaluation)
	}
	if len(updated.Contacts) != 1 || updated.Contacts[0].Email != "ulizwim@cidb.org.za" {
		t.Fatalf("expected promoted contact, got %#v", updated.Contacts)
	}
	if len(updated.Briefings) != 1 || updated.Briefings[0].DateTime != "2026-04-08 10:00" || updated.Briefings[0].Venue != "191 Madiba Street, Pretoria" || !updated.Briefings[0].Required {
		t.Fatalf("expected promoted briefing, got %#v", updated.Briefings)
	}
}

func TestProcessSourceChecksManualSingleTrigger(t *testing.T) {
	allowPrivateURLs(t)
	s, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	now := time.Date(2026, 4, 3, 9, 0, 0, 0, time.UTC)
	if err := s.UpsertSourceScheduleSettings(ctx, models.SourceScheduleSettings{ID: "global", DefaultIntervalMinutes: 60}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"releases": []map[string]any{{
				"ocid":           "ocid-1",
				"title":          "Manual check tender",
				"issuer":         "Metro",
				"status":         "open",
				"original_url":   "https://example.org/t/1",
				"document_url":   "https://example.org/d/1.pdf",
				"published_date": "2026-04-03",
			}},
		})
	}))
	defer server.Close()
	cfg := models.SourceConfig{
		Key:                 "manual-feed",
		Name:                "Manual Feed",
		Type:                source.TypeJSONFeed,
		FeedURL:             server.URL,
		Enabled:             true,
		ManualChecksEnabled: true,
		AutoCheckEnabled:    false,
	}
	if err := s.UpsertSourceConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertSourceHealth(ctx, models.SourceHealth{SourceKey: cfg.Key, PendingManualCheck: true, LastStatus: "queued"}); err != nil {
		t.Fatal(err)
	}
	r := Runner{Store: s, Extractor: extract.New("http://127.0.0.1:1"), SyncEvery: time.Hour, LoopEvery: time.Second, Now: func() time.Time { return now }}
	r.processSourceChecks(ctx)

	health, err := s.GetSourceHealth(ctx, cfg.Key)
	if err != nil {
		t.Fatal(err)
	}
	if health.PendingManualCheck || health.LastStatus != "success" || health.LastTrigger != "manual" {
		t.Fatalf("expected successful manual run, got %#v", health)
	}
	if !health.NextScheduledCheckAt.IsZero() {
		t.Fatalf("manual-only source should not have next schedule, got %v", health.NextScheduledCheckAt)
	}
	runs, err := s.ListSyncRuns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Trigger != "manual" {
		t.Fatalf("expected one manual sync run, got %#v", runs)
	}
	items, total, err := s.ListTenders(ctx, store.ListFilter{Page: 1, PageSize: 20})
	if err != nil || total != 1 || len(items) != 1 {
		t.Fatalf("expected imported tender, err=%v total=%d len=%d", err, total, len(items))
	}
}

func TestProcessSourceChecksSkipsExpiredTenderExtractionQueue(t *testing.T) {
	allowPrivateURLs(t)
	s, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	now := time.Date(2026, 4, 3, 9, 0, 0, 0, time.UTC)
	if err := s.UpsertSourceScheduleSettings(ctx, models.SourceScheduleSettings{ID: "global", DefaultIntervalMinutes: 60}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"releases": []map[string]any{
				{
					"ocid":         "expired-source",
					"title":        "Expired source tender",
					"issuer":       "Metro",
					"status":       "open",
					"closing_date": "2026-04-02 11:00",
					"document_url": "https://example.org/expired.pdf",
				},
				{
					"ocid":         "active-source",
					"title":        "Active source tender",
					"issuer":       "Metro",
					"status":       "open",
					"closing_date": "2026-04-04 11:00",
					"document_url": "https://example.org/active.pdf",
				},
			},
		})
	}))
	defer server.Close()
	cfg := models.SourceConfig{
		Key:                 "expiry-feed",
		Name:                "Expiry Feed",
		Type:                source.TypeJSONFeed,
		FeedURL:             server.URL,
		Enabled:             true,
		ManualChecksEnabled: true,
		AutoCheckEnabled:    false,
	}
	if err := s.UpsertSourceConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertSourceHealth(ctx, models.SourceHealth{SourceKey: cfg.Key, PendingManualCheck: true, LastStatus: "queued"}); err != nil {
		t.Fatal(err)
	}
	r := Runner{Store: s, Extractor: extract.New("http://127.0.0.1:1"), SyncEvery: time.Hour, LoopEvery: time.Second, Now: func() time.Time { return now }}
	r.processSourceChecks(ctx)

	items, total, err := s.ListTenders(ctx, store.ListFilter{Page: 1, PageSize: 20, Sort: "title"})
	if err != nil || total != 2 || len(items) != 2 {
		t.Fatalf("expected both source tenders persisted for visibility, err=%v total=%d items=%#v", err, total, items)
	}
	byTitle := map[string]models.Tender{}
	for _, item := range items {
		byTitle[item.Title] = item
	}
	if byTitle["Expired source tender"].DocumentStatus != models.ExtractionSkipped || byTitle["Expired source tender"].ExtractionSkippedReason != tenderstate.ExpiredSkipReason {
		t.Fatalf("expected expired source tender marked skipped, got %#v", byTitle["Expired source tender"])
	}
	if byTitle["Active source tender"].DocumentStatus != models.ExtractionQueued {
		t.Fatalf("expected active source tender queued, got %#v", byTitle["Active source tender"])
	}
	jobs, err := s.ListJobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].TenderID != byTitle["Active source tender"].ID || jobs[0].State != models.ExtractionQueued {
		t.Fatalf("expected only active tender queued, got %#v", jobs)
	}
}

func TestProcessSourceChecksRefreshesKeywordMatches(t *testing.T) {
	allowPrivateURLs(t)
	s, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	now := time.Date(2026, 4, 3, 9, 0, 0, 0, time.UTC)
	if _, err := s.UpsertKeyword(ctx, models.Keyword{TenantID: "tenant-1", UserID: "user-1", Value: "solar inverter", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertSourceScheduleSettings(ctx, models.SourceScheduleSettings{ID: "global", DefaultIntervalMinutes: 60}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"releases": []map[string]any{{
				"ocid":         "ocid-keyword",
				"title":        "Solar inverter maintenance",
				"issuer":       "Metro",
				"status":       "open",
				"original_url": "https://example.org/t/keyword",
			}},
		})
	}))
	defer server.Close()
	cfg := models.SourceConfig{
		Key:                 "keyword-feed",
		Name:                "Keyword Feed",
		Type:                source.TypeJSONFeed,
		FeedURL:             server.URL,
		Enabled:             true,
		ManualChecksEnabled: true,
		AutoCheckEnabled:    false,
	}
	if err := s.UpsertSourceConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertSourceHealth(ctx, models.SourceHealth{SourceKey: cfg.Key, PendingManualCheck: true, LastStatus: "queued"}); err != nil {
		t.Fatal(err)
	}
	r := Runner{Store: s, Extractor: extract.New("http://127.0.0.1:1"), SyncEvery: time.Hour, LoopEvery: time.Second, Now: func() time.Time { return now }}
	r.processSourceChecks(ctx)

	items, total, err := s.ListKeywordTenderMatches(ctx, "tenant-1", "user-1", store.KeywordMatchFilter{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 || !containsKeyword(items[0].Match.MatchedKeywords, "solar inverter") {
		t.Fatalf("expected source update to refresh keyword matches, total=%d items=%#v", total, items)
	}
}

func TestProcessSourceChecksScheduledUsesOverrideAndSkipsDisabledOrManualOnly(t *testing.T) {
	allowPrivateURLs(t)
	s, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	now := time.Date(2026, 4, 3, 9, 0, 0, 0, time.UTC)
	if err := s.UpsertSourceScheduleSettings(ctx, models.SourceScheduleSettings{ID: "global", DefaultIntervalMinutes: 120}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"releases": []map[string]any{}})
	}))
	defer server.Close()
	configs := []models.SourceConfig{
		{Key: "override", Name: "Override", Type: source.TypeJSONFeed, FeedURL: server.URL, Enabled: true, ManualChecksEnabled: true, AutoCheckEnabled: true, IntervalMinutes: 15},
		{Key: "manual-only", Name: "Manual Only", Type: source.TypeJSONFeed, FeedURL: server.URL, Enabled: true, ManualChecksEnabled: true, AutoCheckEnabled: false},
		{Key: "disabled", Name: "Disabled", Type: source.TypeJSONFeed, FeedURL: server.URL, Enabled: false, ManualChecksEnabled: true, AutoCheckEnabled: true},
	}
	for _, cfg := range configs {
		if err := s.UpsertSourceConfig(ctx, cfg); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.UpsertSourceHealth(ctx, models.SourceHealth{SourceKey: "override", NextScheduledCheckAt: now.Add(-time.Minute)})
	_ = s.UpsertSourceHealth(ctx, models.SourceHealth{SourceKey: "manual-only", NextScheduledCheckAt: now.Add(-time.Minute)})
	_ = s.UpsertSourceHealth(ctx, models.SourceHealth{SourceKey: "disabled", NextScheduledCheckAt: now.Add(-time.Minute)})

	r := Runner{Store: s, Extractor: extract.New("http://127.0.0.1:1"), SyncEvery: 2 * time.Hour, LoopEvery: time.Second, Now: func() time.Time { return now }}
	r.processSourceChecks(ctx)

	overrideHealth, _ := s.GetSourceHealth(ctx, "override")
	manualHealth, _ := s.GetSourceHealth(ctx, "manual-only")
	disabledHealth, _ := s.GetSourceHealth(ctx, "disabled")
	if overrideHealth.LastStatus != "success" {
		t.Fatalf("expected scheduled source to run, got %#v", overrideHealth)
	}
	if got := overrideHealth.NextScheduledCheckAt.Sub(now); got != 15*time.Minute {
		t.Fatalf("expected override next run in 15m, got %v", got)
	}
	if manualHealth.LastCheckedAt != (time.Time{}) {
		t.Fatalf("manual-only source should not auto-run, got %#v", manualHealth)
	}
	if disabledHealth.LastCheckedAt != (time.Time{}) {
		t.Fatalf("disabled source should not run, got %#v", disabledHealth)
	}
}

func TestProcessSourceChecksPreventsDuplicateOverlap(t *testing.T) {
	s, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	now := time.Date(2026, 4, 3, 9, 0, 0, 0, time.UTC)
	_ = s.UpsertSourceScheduleSettings(ctx, models.SourceScheduleSettings{ID: "global", DefaultIntervalMinutes: 60})
	cfg := models.SourceConfig{Key: "busy", Name: "Busy", Type: source.TypeJSONFeed, Enabled: true, ManualChecksEnabled: true, AutoCheckEnabled: true}
	_ = s.UpsertSourceConfig(ctx, cfg)
	_ = s.UpsertSourceHealth(ctx, models.SourceHealth{SourceKey: "busy", Running: true, PendingManualCheck: true, NextScheduledCheckAt: now.Add(-time.Minute)})

	r := Runner{Store: s, Extractor: extract.New("http://127.0.0.1:1"), SyncEvery: time.Hour, LoopEvery: time.Second, Now: func() time.Time { return now }}
	r.processSourceChecks(ctx)

	health, _ := s.GetSourceHealth(ctx, "busy")
	if !health.Running || !health.PendingManualCheck {
		t.Fatalf("expected running source to remain pending without overlap, got %#v", health)
	}
	runs, _ := s.ListSyncRuns(ctx)
	if len(runs) != 0 {
		t.Fatalf("expected no sync run while source marked running, got %#v", runs)
	}
}

func TestProcessSourceChecksFailureBackoff(t *testing.T) {
	allowPrivateURLs(t)
	s, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	now := time.Date(2026, 4, 3, 9, 0, 0, 0, time.UTC)
	_ = s.UpsertSourceScheduleSettings(ctx, models.SourceScheduleSettings{ID: "global", DefaultIntervalMinutes: 180})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer server.Close()
	cfg := models.SourceConfig{Key: "failing", Name: "Failing", Type: source.TypeJSONFeed, FeedURL: server.URL, Enabled: true, ManualChecksEnabled: true, AutoCheckEnabled: true}
	_ = s.UpsertSourceConfig(ctx, cfg)
	_ = s.UpsertSourceHealth(ctx, models.SourceHealth{SourceKey: "failing", NextScheduledCheckAt: now.Add(-time.Minute)})

	r := Runner{Store: s, Extractor: extract.New("http://127.0.0.1:1"), SyncEvery: 3 * time.Hour, LoopEvery: time.Second, Now: func() time.Time { return now }}
	r.processSourceChecks(ctx)

	health, _ := s.GetSourceHealth(ctx, "failing")
	if health.LastStatus != "failed" || health.ConsecutiveFailures != 1 {
		t.Fatalf("expected failed health state, got %#v", health)
	}
	if got := health.NextScheduledCheckAt.Sub(now); got != 5*time.Minute {
		t.Fatalf("expected 5m retry backoff, got %v", got)
	}
}

func containsKeyword(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
