package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"openbid/internal/models"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSQLiteStoreBasicFlow(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	if err := s.UpsertTenant(ctx, models.Tenant{ID: "t1", Name: "Tenant One"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertUser(ctx, models.User{ID: "u1", Username: "alice", IsActive: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertMembership(ctx, models.Membership{UserID: "u1", TenantID: "t1", Role: models.TenantRoleOwner}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertTender(ctx, models.Tender{ID: "x1", Title: "Civil works", Issuer: "Metro", SourceKey: "treasury", Status: "open", DocumentURL: "https://example.org/a.pdf"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertWorkflow(ctx, models.Workflow{TenantID: "t1", TenderID: "x1", Status: "reviewing", Priority: "high"}); err != nil {
		t.Fatal(err)
	}
	if err := s.QueueJob(ctx, models.ExtractionJob{TenderID: "x1", DocumentURL: "https://example.org/a.pdf", State: models.ExtractionQueued}); err != nil {
		t.Fatal(err)
	}

	items, total, err := s.ListTenders(ctx, ListFilter{Query: "civil", WorkflowStatus: "reviewing", TenantID: "t1", UserID: "u1", Page: 1, PageSize: 20})
	if err != nil || total != 1 || len(items) != 1 {
		t.Fatalf("unexpected tender list result: err=%v total=%d len=%d", err, total, len(items))
	}

	if err := s.UpsertTender(ctx, models.Tender{ID: "x2", Title: "Bookmarked works", Issuer: "Metro", SourceKey: "treasury", Status: "open"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertBookmark(ctx, models.Bookmark{TenantID: "t1", UserID: "u1", TenderID: "x2"}); err != nil {
		t.Fatal(err)
	}
	items, total, err = s.ListTenders(ctx, ListFilter{BookmarkedOnly: true, TenantID: "t1", UserID: "u1", Page: 1, PageSize: 20})
	if err != nil || total != 1 || len(items) != 1 || items[0].ID != "x2" {
		t.Fatalf("unexpected bookmarked tender list result: err=%v total=%d items=%#v", err, total, items)
	}
}

func TestSQLiteMigrationAndRuntimeValidation(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ValidateRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteMigrationAddsKeywordTablesWithoutDroppingExistingRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		create table schema_meta (key text primary key, value text not null);
		insert into schema_meta(key, value) values('schema_version', '6');
		create table saved_search_records (
			id text primary key,
			tenant_id text not null,
			user_id text not null,
			name text not null,
			query text not null,
			filters text not null,
			created_at text not null,
			updated_at text not null
		);
		insert into saved_search_records(id, tenant_id, user_id, name, query, filters, created_at, updated_at)
		values('saved-1', 'tenant-1', 'user-1', 'Legacy saved search', 'q=roads', '{}', '2026-04-01T00:00:00Z', '2026-04-01T00:00:00Z');
		pragma user_version = 6;
	`)
	if closeErr := db.Close(); err == nil && closeErr != nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}

	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ValidateRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	searches, err := s.ListSavedSearches(context.Background(), "tenant-1", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(searches) != 1 || searches[0].Name != "Legacy saved search" {
		t.Fatalf("expected existing saved search to survive migration, got %#v", searches)
	}
	stats, err := s.RuntimeStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.KeywordProfileCount != 0 || stats.KeywordCount != 0 || stats.KeywordMatchCount != 0 {
		t.Fatalf("expected migration to add empty keyword tables without seeding data, got %#v", stats)
	}
}

func TestSQLiteKeywordReadPathsDoNotCreateProfile(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.KeywordSearchSummary(ctx, "tenant-1", "user-1"); err != nil {
		t.Fatal(err)
	}
	if keywords, err := s.ListKeywords(ctx, "tenant-1", "user-1"); err != nil || len(keywords) != 0 {
		t.Fatalf("expected empty keyword list without profile, keywords=%#v err=%v", keywords, err)
	}
	if matches, total, err := s.ListKeywordTenderMatches(ctx, "tenant-1", "user-1", KeywordMatchFilter{Page: 1, PageSize: 20}); err != nil || total != 0 || len(matches) != 0 {
		t.Fatalf("expected empty matches without profile, total=%d matches=%#v err=%v", total, matches, err)
	}
	if _, err := s.GetKeywordProfile(ctx, "tenant-1", "user-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected read paths not to create keyword profile, got %v", err)
	}
	stats, err := s.RuntimeStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.KeywordProfileCount != 0 || stats.KeywordCount != 0 || stats.KeywordMatchCount != 0 {
		t.Fatalf("expected no keyword rows after read paths, got %#v", stats)
	}
}

func TestSQLiteRuntimeStatsReportsSchemaAndCounts(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	if err := s.UpsertTenant(ctx, models.Tenant{ID: "tenant-1", Name: "Tenant One", Slug: "tenant-one"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertUser(ctx, models.User{ID: "user-1", Username: "admin", IsActive: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertMembership(ctx, models.Membership{UserID: "user-1", TenantID: "tenant-1", Role: models.TenantRoleOwner}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertTender(ctx, models.Tender{ID: "tender-1", Title: "Civil works", Issuer: "Metro", SourceKey: "treasury", Status: "open"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddAuditEntry(ctx, models.AuditEntry{TenantID: "tenant-1", UserID: "user-1", Action: "create", Entity: "tender", EntityID: "tender-1", Summary: "Created tender"}); err != nil {
		t.Fatal(err)
	}

	stats, err := s.RuntimeStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.SchemaVersion != currentSchemaVersion || stats.ExpectedSchemaVersion != currentSchemaVersion {
		t.Fatalf("unexpected schema stats: %#v", stats)
	}
	if stats.QuickCheck == "" || stats.JournalMode == "" {
		t.Fatalf("expected runtime health details, got %#v", stats)
	}
	if stats.TenantCount != 1 || stats.UserCount != 1 || stats.MembershipCount != 1 || stats.TenderCount != 1 || stats.AuditCount != 1 {
		t.Fatalf("expected persisted counts, got %#v", stats)
	}
}

func TestSQLiteQueueWritesDeduplicate(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	job := models.ExtractionJob{TenderID: "t1", DocumentURL: "https://example.org/doc.pdf", State: models.ExtractionQueued}
	if err := s.QueueJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := s.QueueJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.ListJobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected 1 queued job, got %d", len(jobs))
	}
}

func TestSQLiteUpsertBookmarkUpdatesExistingNote(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.UpsertBookmark(ctx, models.Bookmark{TenantID: "t1", UserID: "u1", TenderID: "x1", Note: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertBookmark(ctx, models.Bookmark{TenantID: "t1", UserID: "u1", TenderID: "x1", Note: "updated"}); err != nil {
		t.Fatal(err)
	}
	bookmarks, err := s.ListBookmarks(ctx, "t1", "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(bookmarks) != 1 || bookmarks[0].Note != "updated" {
		t.Fatalf("expected bookmark note update, got %#v", bookmarks)
	}
}

func TestSQLiteConcurrentWrites(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 15; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := fmt.Sprintf("tender-%d", n)
			_ = s.UpsertTender(ctx, models.Tender{ID: id, Title: "Tender", Issuer: "Issuer", SourceKey: "treasury", Status: "open"})
		}(i)
	}
	wg.Wait()
	items, total, err := s.ListTenders(ctx, ListFilter{Page: 1, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	if total != len(items) || total < 15 {
		t.Fatalf("expected >=15 tenders, got total=%d len=%d", total, len(items))
	}
}

func TestDashboardCountsAllTendersBeyondPageCap(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	for i := 0; i < 120; i++ {
		if err := s.UpsertTender(ctx, models.Tender{
			ID:             fmt.Sprintf("dash-%d", i),
			Title:          "Tender",
			Issuer:         "Issuer",
			SourceKey:      "treasury",
			Status:         "open",
			DocumentURL:    "https://example.org/doc.pdf",
			DocumentStatus: models.ExtractionCompleted,
		}); err != nil {
			t.Fatal(err)
		}
	}
	dashboard, err := s.Dashboard(ctx, "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if dashboard.TotalTenders != 120 || dashboard.WithDocuments != 120 || dashboard.ExtractedDocuments != 120 {
		t.Fatalf("expected full dashboard counts, got %#v", dashboard)
	}
}

func TestSQLiteListTendersPaginatesAndFiltersWithoutWorkflowJoin(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	inputs := []models.Tender{
		{ID: "sql-1", Title: "Alpha", Issuer: "Metro", SourceKey: "treasury", Status: "open", PublishedDate: "2026-04-01", DocumentURL: "https://example.org/a.pdf", DocumentStatus: models.ExtractionCompleted},
		{ID: "sql-2", Title: "Bravo", Issuer: "Metro", SourceKey: "treasury", Status: "open", PublishedDate: "2026-04-02", DocumentURL: "https://example.org/b.pdf", DocumentStatus: models.ExtractionCompleted},
		{ID: "sql-3", Title: "Charlie", Issuer: "Metro", SourceKey: "treasury", Status: "closed", PublishedDate: "2026-04-03", DocumentStatus: models.ExtractionQueued},
	}
	for _, tender := range inputs {
		if err := s.UpsertTender(ctx, tender); err != nil {
			t.Fatal(err)
		}
	}

	items, total, err := s.ListTenders(ctx, ListFilter{
		Source:         "treasury",
		Status:         "open",
		DocumentStatus: string(models.ExtractionCompleted),
		HasDocuments:   true,
		Sort:           "published_date",
		Page:           2,
		PageSize:       1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(items) != 1 {
		t.Fatalf("expected total=2 len=1, got total=%d len=%d", total, len(items))
	}
	if items[0].ID != "sql-1" {
		t.Fatalf("expected second page to contain sql-1, got %#v", items[0])
	}
}

func TestSQLiteTenderRoundTripsStructuredFields(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	original := models.Tender{
		ID:           "rich-1",
		Title:        "Structured Tender",
		Issuer:       "Example Works",
		SourceKey:    "etenders",
		Status:       "open",
		TenderType:   "Request for Bid(Open-Tender)",
		ValidityDays: 90,
		PageFacts: map[string]string{
			"closing_details": "2026-05-11 11:00",
		},
		DocumentFacts: map[string]string{
			"cidb_hints": "CIDB 3GB",
		},
		Location: models.TenderLocation{
			Town:     "Komani",
			Province: "Eastern Cape",
		},
		Submission: models.TenderSubmission{
			Method:            "physical",
			ElectronicAllowed: false,
			PhysicalAllowed:   true,
		},
		Evaluation: models.TenderEvaluation{
			Method:                    "80/20",
			PricePoints:               80,
			PreferencePoints:          20,
			MinimumFunctionalityScore: 60,
		},
		Contacts: []models.TenderContact{{
			Role:      "listing_contact",
			Name:      "Ms K Mbuqwa",
			Email:     "khutala.mbuqwa@ecagriculture.gov.za",
			Telephone: "083-262-2633",
		}},
		Briefings: []models.TenderBriefing{{
			Label:    "site_inspection",
			DateTime: "2026-04-16 10:00",
			Required: true,
		}},
		Documents: []models.TenderDocument{{
			URL:      "https://example.org/doc.pdf",
			FileName: "doc.pdf",
			Role:     "notice",
			Source:   "listing",
		}},
		Requirements: []models.TenderRequirement{{
			Category:    "registration",
			Description: "Active CSD registration",
			Required:    true,
		}},
	}
	if err := s.UpsertTender(ctx, original); err != nil {
		t.Fatal(err)
	}
	stored, err := s.GetTender(ctx, "rich-1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Evaluation.Method != "80/20" || stored.Location.Town != "Komani" {
		t.Fatalf("expected structured fields to round-trip, got %#v", stored)
	}
	if len(stored.Documents) != 1 || stored.Documents[0].FileName != "doc.pdf" {
		t.Fatalf("expected documents to round-trip, got %#v", stored.Documents)
	}
	if stored.DocumentFacts["cidb_hints"] != "CIDB 3GB" || stored.PageFacts["closing_details"] == "" {
		t.Fatalf("expected fact maps to round-trip, got page=%#v doc=%#v", stored.PageFacts, stored.DocumentFacts)
	}
}

func TestSQLiteKeywordSearchLifecycleMatchingAndScoping(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.UpsertTender(ctx, models.Tender{ID: "kw-solar", Title: "Solar panel installation", Summary: "Rooftop PV work", SourceKey: "treasury", Status: "open", ClosingDate: "2026-05-01"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertTender(ctx, models.Tender{ID: "kw-water", Title: "Pump station", Summary: "Water treatment upgrades", SourceKey: "cidb", Status: "open", ClosingDate: "2026-05-02"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertTender(ctx, models.Tender{ID: "kw-generator", Title: "Facilities maintenance", SourceKey: "eskom", Status: "open", DocumentFacts: map[string]string{"scope": "backup generator maintenance"}, ClosingDate: "2026-05-03"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertTender(ctx, models.Tender{ID: "kw-none", Title: "Road repairs", Summary: "Asphalt surfacing", SourceKey: "treasury", Status: "open"}); err != nil {
		t.Fatal(err)
	}

	solar, err := s.UpsertKeyword(ctx, models.Keyword{TenantID: "tenant-1", UserID: "user-1", Value: "SOLAR", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertKeyword(ctx, models.Keyword{TenantID: "tenant-1", UserID: "user-1", Value: "water treatment", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	generator, err := s.UpsertKeyword(ctx, models.Keyword{TenantID: "tenant-1", UserID: "user-1", Value: "backup generator", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	otherKeyword, err := s.UpsertKeyword(ctx, models.Keyword{TenantID: "tenant-1", UserID: "user-2", Value: "asphalt", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertKeyword(ctx, models.Keyword{ID: otherKeyword.ID, TenantID: "tenant-1", UserID: "user-1", Value: "stolen edit", Enabled: true}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected cross-user keyword edit to be rejected, got %v", err)
	}

	items, total, err := s.ListKeywordTenderMatches(ctx, "tenant-1", "user-1", KeywordMatchFilter{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(items) != 3 {
		t.Fatalf("expected three matches for user-1, got total=%d items=%#v", total, items)
	}
	matchByTender := map[string][]string{}
	for _, item := range items {
		matchByTender[item.Tender.ID] = item.Match.MatchedKeywords
	}
	if !containsString(matchByTender["kw-solar"], "SOLAR") || !containsString(matchByTender["kw-water"], "water treatment") || !containsString(matchByTender["kw-generator"], "backup generator") {
		t.Fatalf("expected title, phrase, and fact matches, got %#v", matchByTender)
	}

	otherItems, otherTotal, err := s.ListKeywordTenderMatches(ctx, "tenant-1", "user-2", KeywordMatchFilter{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if otherTotal != 1 || len(otherItems) != 1 || otherItems[0].Tender.ID != "kw-none" {
		t.Fatalf("expected user scoping to isolate matches, got total=%d items=%#v", otherTotal, otherItems)
	}

	solar.Value = "asphalt"
	if _, err := s.UpsertKeyword(ctx, solar); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteKeyword(ctx, "tenant-1", "user-1", generator.ID); err != nil {
		t.Fatal(err)
	}
	items, total, err = s.ListKeywordTenderMatches(ctx, "tenant-1", "user-1", KeywordMatchFilter{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	matchByTender = map[string][]string{}
	for _, item := range items {
		matchByTender[item.Tender.ID] = item.Match.MatchedKeywords
	}
	if total != 2 || matchByTender["kw-solar"] != nil || matchByTender["kw-generator"] != nil || !containsString(matchByTender["kw-none"], "asphalt") {
		t.Fatalf("expected edits/deletes to recalculate results, total=%d matches=%#v", total, matchByTender)
	}

	summary, err := s.RefreshKeywordMatches(ctx, "tenant-1", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if summary.MatchedTenderCount != 2 || summary.ActiveKeywordCount != 2 || summary.LastRefreshedAt.IsZero() {
		t.Fatalf("unexpected manual refresh summary: %#v", summary)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	keywords, err := reopened.ListKeywords(ctx, "tenant-1", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(keywords) != 2 {
		t.Fatalf("expected keyword persistence after reopen, got %#v", keywords)
	}
}

func TestSQLiteKeywordMatchesRefreshWhenTenderChanges(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.UpsertKeyword(ctx, models.Keyword{TenantID: "tenant-1", UserID: "user-1", Value: "substation", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertTender(ctx, models.Tender{ID: "source-update", Title: "Substation repair", SourceKey: "eskom", Status: "open"}); err != nil {
		t.Fatal(err)
	}
	_, total, err := s.ListKeywordTenderMatches(ctx, "tenant-1", "user-1", KeywordMatchFilter{Page: 1, PageSize: 20})
	if err != nil || total != 1 {
		t.Fatalf("expected tender write to create keyword match, total=%d err=%v", total, err)
	}
	if err := s.UpsertTender(ctx, models.Tender{ID: "source-update", Title: "Road repair", SourceKey: "eskom", Status: "open"}); err != nil {
		t.Fatal(err)
	}
	_, total, err = s.ListKeywordTenderMatches(ctx, "tenant-1", "user-1", KeywordMatchFilter{Page: 1, PageSize: 20})
	if err != nil || total != 0 {
		t.Fatalf("expected tender write to remove stale keyword match, total=%d err=%v", total, err)
	}
}

func TestSQLiteSourceSchedulingRoundTrips(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	settings := models.SourceScheduleSettings{ID: "global", DefaultIntervalMinutes: 45, Paused: true}
	if err := s.UpsertSourceScheduleSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	cfg := models.SourceConfig{
		Key:                 "metro",
		Name:                "Metro",
		Type:                "json_feed",
		FeedURL:             "https://example.org/feed.json",
		Enabled:             true,
		ManualChecksEnabled: true,
		AutoCheckEnabled:    true,
		IntervalMinutes:     15,
	}
	if err := s.UpsertSourceConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	health := models.SourceHealth{
		SourceKey:             "metro",
		LastStatus:            "success",
		HealthStatus:          "healthy",
		LastItemCount:         3,
		ConsecutiveFailures:   0,
		LastCheckedAt:         time.Now().UTC(),
		LastSuccessfulCheckAt: time.Now().UTC(),
		NextScheduledCheckAt:  time.Now().UTC().Add(15 * time.Minute),
		PendingManualCheck:    true,
	}
	if err := s.UpsertSourceHealth(ctx, health); err != nil {
		t.Fatal(err)
	}

	storedSettings, err := s.GetSourceScheduleSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if storedSettings.DefaultIntervalMinutes != 45 || !storedSettings.Paused {
		t.Fatalf("unexpected stored settings: %#v", storedSettings)
	}
	storedConfig, err := s.GetSourceConfig(ctx, "metro")
	if err != nil {
		t.Fatal(err)
	}
	if !storedConfig.AutoCheckEnabled || storedConfig.IntervalMinutes != 15 {
		t.Fatalf("unexpected stored config: %#v", storedConfig)
	}
	storedHealth, err := s.GetSourceHealth(ctx, "metro")
	if err != nil {
		t.Fatal(err)
	}
	if !storedHealth.PendingManualCheck || storedHealth.HealthStatus != "healthy" {
		t.Fatalf("unexpected stored health: %#v", storedHealth)
	}
}

func TestSQLiteTenderFilterOptionsAndSorts(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	if err := s.UpsertSourceConfig(ctx, models.SourceConfig{Key: "treasury", Name: "National Treasury", Type: "json_feed", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertSourceConfig(ctx, models.SourceConfig{Key: "eskom", Name: "Eskom Tender Bulletin", Type: "eskom_portal", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertTenant(ctx, models.Tenant{ID: "tenant-1", Name: "Tenant One", Slug: "tenant-one"}); err != nil {
		t.Fatal(err)
	}
	tenders := []models.Tender{
		{ID: "sort-1", Title: "Alpha", Issuer: "Metro Water", SourceKey: "treasury", Province: "Gauteng", Status: "open", Category: "Civil Engineering", CIDBGrading: "7CE", DocumentStatus: models.ExtractionCompleted},
		{ID: "sort-2", Title: "Bravo", Issuer: "Eskom Holdings", SourceKey: "eskom", Province: "Western Cape", Status: "closed", Category: "Electrical Engineering", CIDBGrading: "6EP", DocumentStatus: models.ExtractionQueued},
	}
	for _, tender := range tenders {
		if err := s.UpsertTender(ctx, tender); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UpsertWorkflow(ctx, models.Workflow{TenantID: "tenant-1", TenderID: "sort-1", Status: "reviewing"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertWorkflow(ctx, models.Workflow{TenantID: "tenant-1", TenderID: "sort-2", Status: "submitted"}); err != nil {
		t.Fatal(err)
	}

	options, err := s.TenderFilterOptions(ctx, "tenant-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(options.Sources) != 2 || options.Sources[0].Label != "Eskom Tender Bulletin" || options.Sources[1].Label != "National Treasury" {
		t.Fatalf("unexpected source options: %#v", options.Sources)
	}
	for _, want := range []string{"Gauteng", "Western Cape"} {
		if !containsString(options.Provinces, want) {
			t.Fatalf("expected province option %q in %#v", want, options.Provinces)
		}
	}
	for _, want := range []string{"reviewing", "submitted"} {
		if !containsString(options.WorkflowStatus, want) {
			t.Fatalf("expected workflow option %q in %#v", want, options.WorkflowStatus)
		}
	}
	for _, want := range []string{"completed", "queued"} {
		if !containsString(options.DocumentStatus, want) {
			t.Fatalf("expected document status option %q in %#v", want, options.DocumentStatus)
		}
	}

	items, _, err := s.ListTenders(ctx, ListFilter{Sort: "source", Page: 1, PageSize: 10, TenantID: "tenant-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].SourceKey != "eskom" || items[1].SourceKey != "treasury" {
		t.Fatalf("unexpected source sort order: %#v", items)
	}

	items, _, err = s.ListTenders(ctx, ListFilter{Sort: "workflow_status", Page: 1, PageSize: 10, TenantID: "tenant-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ID != "sort-1" || items[1].ID != "sort-2" {
		t.Fatalf("unexpected workflow sort order: %#v", items)
	}

	items, _, err = s.ListTenders(ctx, ListFilter{Sort: "document_status", Page: 1, PageSize: 10, TenantID: "tenant-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ID != "sort-1" || items[1].ID != "sort-2" {
		t.Fatalf("unexpected document status sort order: %#v", items)
	}
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func TestSQLiteDeduplicateTendersCollapsesDuplicatesAndRepairsReferences(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	canonical := models.Tender{
		ID:          "etenders-a",
		SourceKey:   "etenders",
		ExternalID:  "152485",
		Title:       "Plant and equipment panel",
		Issuer:      "Madibeng Local Municipality",
		ClosingDate: "2026-04-30 10:00",
		DocumentURL: "https://example.org/doc.pdf",
		PageFacts:   map[string]string{"closing_details": "2026-04-30 10:00"},
		Documents:   []models.TenderDocument{{URL: "https://example.org/doc.pdf", FileName: "doc.pdf"}},
		UpdatedAt:   time.Now().Add(-time.Hour).UTC(),
	}
	duplicate := models.Tender{
		ID:            "etenders-b",
		SourceKey:     "etenders",
		ExternalID:    "152485",
		Title:         "Plant and equipment panel",
		Issuer:        "Madibeng Local Municipality",
		ClosingDate:   "2026-04-30 10:00",
		DocumentURL:   "https://example.org/doc.pdf",
		Summary:       "Extra summary",
		DocumentFacts: map[string]string{"cidb_hints": "CIDB 3GB"},
		UpdatedAt:     time.Now().UTC(),
	}
	if err := s.UpsertTender(ctx, canonical); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertTender(ctx, duplicate); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertWorkflow(ctx, models.Workflow{TenantID: "tenant-1", TenderID: duplicate.ID, Status: "reviewing", Priority: "high"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertBookmark(ctx, models.Bookmark{TenantID: "tenant-1", UserID: "user-1", TenderID: duplicate.ID, Note: "watch this"}); err != nil {
		t.Fatal(err)
	}
	if err := s.QueueJob(ctx, models.ExtractionJob{TenderID: "", DocumentURL: canonical.DocumentURL, State: models.ExtractionQueued}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddWorkflowEvent(ctx, models.WorkflowEvent{TenantID: "tenant-1", TenderID: duplicate.ID, Status: "reviewing", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddAuditEntry(ctx, models.AuditEntry{TenantID: "tenant-1", Entity: "tender", EntityID: duplicate.ID, Summary: "duplicate touched", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}

	removed, err := s.DeduplicateTenders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("expected one duplicate to be removed, got %d", removed)
	}

	items, total, err := s.ListTenders(ctx, ListFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("expected one tender after dedupe, got total=%d len=%d", total, len(items))
	}
	if items[0].Summary != "Extra summary" || items[0].DocumentFacts["cidb_hints"] != "CIDB 3GB" {
		t.Fatalf("expected merged tender data to be preserved, got %#v", items[0])
	}

	workflow, err := s.GetWorkflow(ctx, "tenant-1", canonical.ID)
	if err != nil {
		t.Fatal(err)
	}
	if workflow.Priority != "high" {
		t.Fatalf("expected workflow to be remapped, got %#v", workflow)
	}
	bookmarks, err := s.ListBookmarks(ctx, "tenant-1", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(bookmarks) != 1 || bookmarks[0].TenderID != canonical.ID {
		t.Fatalf("expected bookmark to be remapped, got %#v", bookmarks)
	}
	jobs, err := s.ListJobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].TenderID != canonical.ID {
		t.Fatalf("expected queued job to be repaired, got %#v", jobs)
	}
	events, err := s.ListWorkflowEvents(ctx, "tenant-1", canonical.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected workflow event to be remapped, got %#v", events)
	}
	audits, err := s.ListAuditEntries(ctx, "tenant-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 || audits[0].EntityID != canonical.ID {
		t.Fatalf("expected audit entry to be remapped, got %#v", audits)
	}
}

func TestSQLiteListAuditEntriesPageAndJobStateCountsUseFastPaths(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	if err := s.UpsertTender(ctx, models.Tender{ID: "audit-fast", Title: "Audit Fast", Issuer: "Metro", SourceKey: "treasury", Status: "open"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 25; i++ {
		if err := s.AddAuditEntry(ctx, models.AuditEntry{
			TenantID:  "tenant-1",
			Entity:    "workflow",
			EntityID:  fmt.Sprintf("entity-%02d", i),
			Summary:   fmt.Sprintf("entry-%02d", i),
			CreatedAt: time.Now().UTC().Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddAuditEntry(ctx, models.AuditEntry{
		TenantID:  "tenant-2",
		Entity:    "workflow",
		EntityID:  "other",
		Summary:   "other-tenant",
		CreatedAt: time.Now().UTC().Add(30 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.QueueJob(ctx, models.ExtractionJob{ID: "queued-job", TenderID: "audit-fast", DocumentURL: "https://example.org/doc.pdf", State: models.ExtractionQueued}); err != nil {
		t.Fatal(err)
	}
	if err := s.QueueJob(ctx, models.ExtractionJob{ID: "failed-job", TenderID: "audit-fast", DocumentURL: "https://example.org/doc2.pdf", State: models.ExtractionFailed}); err != nil {
		t.Fatal(err)
	}
	if err := s.QueueJob(ctx, models.ExtractionJob{ID: "orphan-job", TenderID: "missing", DocumentURL: "https://example.org/doc3.pdf", State: models.ExtractionQueued}); err != nil {
		t.Fatal(err)
	}

	items, total, err := s.ListAuditEntriesPage(ctx, "tenant-1", 2, 20)
	if err != nil {
		t.Fatal(err)
	}
	if total != 25 || len(items) != 5 {
		t.Fatalf("expected paged tenant audit results, got total=%d len=%d", total, len(items))
	}
	for _, item := range items {
		if item.TenantID != "tenant-1" {
			t.Fatalf("expected tenant filter to be applied, got %#v", item)
		}
	}

	counts, err := s.JobStateCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts.Queued != 1 || counts.Failed != 1 {
		t.Fatalf("expected valid job counts only, got %#v", counts)
	}
	if counts.Processing != 0 || counts.Completed != 0 || counts.Retry != 0 {
		t.Fatalf("unexpected extra job counts: %#v", counts)
	}
}
