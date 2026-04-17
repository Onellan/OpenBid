package store

import (
	"context"
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openbid/internal/models"
)

type fakeSmartEmailSender struct {
	calls int
	last  models.EmailMessage
	err   error
}

func (f *fakeSmartEmailSender) Send(_ context.Context, message models.EmailMessage) (models.EmailSendResult, error) {
	f.calls++
	f.last = message
	if f.err != nil {
		return models.EmailSendResult{}, f.err
	}
	return models.EmailSendResult{AcceptedRecipients: len(message.To), Message: "sent by fake sender"}, nil
}

func newSmartTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "smart.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestSmartKeywordEvaluationModesTagsAndExcludes(t *testing.T) {
	s := newSmartTestStore(t)
	ctx := t.Context()
	group, err := s.UpsertSmartKeywordGroup(ctx, models.SmartKeywordGroup{
		TenantID:      "tenant-1",
		Name:          "Water",
		TagName:       "Water",
		Enabled:       true,
		MatchMode:     models.SmartMatchModeAny,
		MinMatchCount: 1,
		ExcludeTerms:  []string{"training"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertSmartKeyword(ctx, models.SmartKeyword{TenantID: "tenant-1", GroupID: group.ID, Value: "wastewater", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertSmartKeyword(ctx, models.SmartKeyword{TenantID: "tenant-1", Value: "pump station", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertSmartExtractionSettings(ctx, models.SmartExtractionSettings{TenantID: "tenant-1", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	tender, evaluation, accepted, err := s.EvaluateSmartTenderForExtraction(ctx, models.Tender{ID: "t1", Title: "Wastewater pump station upgrade", Issuer: "Metro"})
	if err != nil {
		t.Fatal(err)
	}
	if !accepted || !evaluation.Accepted || len(tender.GroupTags) != 1 || tender.GroupTags[0] != "Water" {
		t.Fatalf("expected accepted Water tagged tender, got accepted=%v eval=%#v tender=%#v", accepted, evaluation, tender)
	}

	_, evaluation, accepted, err = s.EvaluateSmartTenderForExtraction(ctx, models.Tender{ID: "t2", Title: "Wastewater training workshop"})
	if err != nil {
		t.Fatal(err)
	}
	if accepted || evaluation.Accepted || len(evaluation.GroupTags) != 0 {
		t.Fatalf("expected exclude term to suppress group match, got accepted=%v eval=%#v", accepted, evaluation)
	}

	group.MatchMode = models.SmartMatchModeAll
	group.ExcludeTerms = nil
	if _, err := s.UpsertSmartKeywordGroup(ctx, group); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertSmartKeyword(ctx, models.SmartKeyword{TenantID: "tenant-1", GroupID: group.ID, Value: "treatment", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, _, accepted, err = s.EvaluateSmartTenderForExtraction(ctx, models.Tender{ID: "t3", Title: "Wastewater works only"}); err != nil {
		t.Fatal(err)
	}
	if accepted {
		t.Fatal("expected ALL mode to require every active group keyword")
	}
	if _, _, accepted, err = s.EvaluateSmartTenderForExtraction(ctx, models.Tender{ID: "t4", Title: "Wastewater treatment package"}); err != nil {
		t.Fatal(err)
	}
	if !accepted {
		t.Fatal("expected ALL mode to accept when every active group keyword matches")
	}
}

func TestSmartReprocessSearchAndAlertDedup(t *testing.T) {
	s := newSmartTestStore(t)
	ctx := t.Context()
	group, err := s.UpsertSmartKeywordGroup(ctx, models.SmartKeywordGroup{TenantID: "tenant-1", Name: "Wastewater", TagName: "Wastewater", Enabled: true, MatchMode: models.SmartMatchModeAny, MinMatchCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertSmartKeyword(ctx, models.SmartKeyword{TenantID: "tenant-1", GroupID: group.ID, Value: "wastewater", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	sender := &fakeSmartEmailSender{}
	s.SetEmailSender(sender)
	if err := s.UpsertSmartExtractionSettings(ctx, models.SmartExtractionSettings{TenantID: "tenant-1", Enabled: true, AlertsEnabled: true, EmailAlertsEnabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertTender(ctx, models.Tender{ID: "match", Title: "Wastewater treatment upgrade", Status: "open"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertTender(ctx, models.Tender{ID: "skip", Title: "Road works", Status: "open"}); err != nil {
		t.Fatal(err)
	}
	result, err := s.ReprocessSmartKeywords(ctx, "tenant-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Accepted != 1 || result.Excluded != 1 {
		t.Fatalf("unexpected reprocess result: %#v", result)
	}
	items, total, err := s.ListTenders(ctx, ListFilter{TenantID: "tenant-1", GroupTag: "Wastewater", Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || items[0].ID != "match" {
		t.Fatalf("expected group tag search to return only match, total=%d items=%#v", total, items)
	}
	filterJSON := `{"GroupTags":["Wastewater"]}`
	view, err := s.UpsertSavedSmartView(ctx, models.SavedSmartView{
		TenantID: "tenant-1", UserID: "user-1", Name: "Wastewater view", FiltersJSON: filterJSON,
		AlertsEnabled: true, AlertFrequency: "immediate",
		AlertChannels: []models.NotificationChannel{{Type: "email", Destination: "ops@example.org", Enabled: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tender, err := s.GetTender(ctx, "match")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.triggerSmartViewAlertsForTender(ctx, "tenant-1", tender, "test"); err != nil {
		t.Fatal(err)
	}
	if err := s.triggerSmartViewAlertsForTender(ctx, "tenant-1", tender, "test"); err != nil {
		t.Fatal(err)
	}
	deliveries, err := s.ListSmartAlertDeliveries(ctx, "tenant-1", view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 1 || deliveries[0].Status != "sent" {
		t.Fatalf("expected one deduplicated sent alert, got %#v", deliveries)
	}
	if sender.calls != 1 || len(sender.last.To) != 1 || sender.last.To[0] != "ops@example.org" {
		t.Fatalf("expected one outbound email to ops@example.org, calls=%d last=%#v", sender.calls, sender.last)
	}
}

func TestSmartEmailAlertsDisabledSkipsEmailSend(t *testing.T) {
	s := newSmartTestStore(t)
	ctx := t.Context()
	group, err := s.UpsertSmartKeywordGroup(ctx, models.SmartKeywordGroup{TenantID: "tenant-1", Name: "Water", TagName: "Water", Enabled: true, MatchMode: models.SmartMatchModeAny, MinMatchCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertSmartKeyword(ctx, models.SmartKeyword{TenantID: "tenant-1", GroupID: group.ID, Value: "water", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertSmartExtractionSettings(ctx, models.SmartExtractionSettings{TenantID: "tenant-1", Enabled: true, AlertsEnabled: true, EmailAlertsEnabled: false}); err != nil {
		t.Fatal(err)
	}
	sender := &fakeSmartEmailSender{}
	s.SetEmailSender(sender)
	view, err := s.UpsertSavedSmartView(ctx, models.SavedSmartView{
		TenantID: "tenant-1", UserID: "user-1", Name: "Water view", FiltersJSON: `{"GroupTags":["Water"]}`,
		AlertsEnabled: true, AlertFrequency: "immediate",
		AlertChannels: []models.NotificationChannel{{Type: "email", Destination: "ops@example.org", Enabled: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tender := models.Tender{ID: "water-1", Title: "Water treatment", GroupTags: []string{"Water"}}
	if err := s.triggerSmartViewAlertsForTender(ctx, "tenant-1", tender, "test"); err != nil {
		t.Fatal(err)
	}
	deliveries, err := s.ListSmartAlertDeliveries(ctx, "tenant-1", view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sender.calls != 0 {
		t.Fatalf("expected disabled email alerts to avoid sender call, got %d", sender.calls)
	}
	if len(deliveries) != 1 || deliveries[0].Status != "skipped" || !strings.Contains(deliveries[0].Error, "off") {
		t.Fatalf("expected skipped delivery when email alerts are off, got %#v", deliveries)
	}
}

func TestEmailSettingsDefaultAndUpsert(t *testing.T) {
	s := newSmartTestStore(t)
	ctx := t.Context()
	settings, err := s.GetEmailSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Enabled || settings.SMTPPort != 587 || settings.SMTPSecurityMode != "starttls" || !settings.SMTPAuthRequired {
		t.Fatalf("unexpected default email settings: %#v", settings)
	}
	settings.Enabled = true
	settings.SMTPHost = "smtp.example.org"
	settings.SMTPFromEmail = "alerts@example.org"
	settings.SMTPPassword = "secret"
	if err := s.UpsertEmailSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetEmailSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || got.SMTPHost != "smtp.example.org" || got.SMTPPassword != "secret" || got.ID != "global" {
		t.Fatalf("stored email settings mismatch: %#v", got)
	}
}

func TestSmartSeedIsDefaultTenantScopedAndIdempotent(t *testing.T) {
	s := newSmartTestStore(t)
	ctx := t.Context()
	seed := filepath.Join("..", "..", "internal", "seeddata", "africa_water_wastewater_tender_keywords.csv")
	expectedPurposes, expectedKeywords := readSmartSeedCSV(t, seed)
	if err := s.SeedSmartKeywordsFromCSV(ctx, "default-tenant", seed); err != nil {
		t.Fatal(err)
	}
	if err := s.SeedSmartKeywordsFromCSV(ctx, "default-tenant", seed); err != nil {
		t.Fatal(err)
	}
	groups, err := s.ListSmartKeywordGroups(ctx, "default-tenant")
	if err != nil {
		t.Fatal(err)
	}
	keywords, err := s.ListSmartKeywords(ctx, "default-tenant")
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 19 || len(keywords) != 471 {
		t.Fatalf("expected seeded 19 groups and 471 keywords without duplicates, got groups=%d keywords=%d", len(groups), len(keywords))
	}
	groupsByName := map[string]models.SmartKeywordGroup{}
	for _, group := range groups {
		groupsByName[group.Name] = group
		if want := expectedPurposes[group.Name]; group.Description != want {
			t.Fatalf("seeded group %q description mismatch: got %q want %q", group.Name, group.Description, want)
		}
	}
	for groupName := range expectedPurposes {
		if _, ok := groupsByName[groupName]; !ok {
			t.Fatalf("seeded group missing from csv: %s", groupName)
		}
	}
	actualKeywords := map[string]map[string]bool{}
	for _, keyword := range keywords {
		group := groupNameForSeedKeyword(t, groups, keyword)
		if actualKeywords[group] == nil {
			actualKeywords[group] = map[string]bool{}
		}
		actualKeywords[group][keyword.Value] = true
	}
	for groupName, expectedGroupKeywords := range expectedKeywords {
		for keyword := range expectedGroupKeywords {
			if !actualKeywords[groupName][keyword] {
				t.Fatalf("seeded keyword missing from csv mapping: group=%q keyword=%q", groupName, keyword)
			}
		}
	}
	keywords[0].Enabled = false
	if _, err := s.UpsertSmartKeyword(ctx, keywords[0]); err != nil {
		t.Fatal(err)
	}
	if err := s.SeedSmartKeywordsFromCSV(ctx, "default-tenant", seed); err != nil {
		t.Fatal(err)
	}
	updated, err := s.smartKeywordByID(ctx, "default-tenant", keywords[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Enabled {
		t.Fatal("reseed should preserve user-disabled seeded keyword")
	}
	for _, group := range groups {
		if group.Enabled || group.MatchMode != models.SmartMatchModeAny || group.MinMatchCount != 1 || group.Priority != 0 || group.TagName == "" {
			t.Fatalf("seeded group missing explicit defaults: %#v", group)
		}
	}
	otherGroups, err := s.ListSmartKeywordGroups(ctx, "other-tenant")
	if err != nil {
		t.Fatal(err)
	}
	if len(otherGroups) != 0 {
		t.Fatalf("seed leaked into non-default tenant: %#v", otherGroups)
	}
}

func readSmartSeedCSV(t *testing.T, path string) (map[string]string, map[string]map[string]bool) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader := csv.NewReader(file)
	rows, err := reader.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	purposes := map[string]string{}
	keywords := map[string]map[string]bool{}
	for _, row := range rows[1:] {
		if len(row) < 3 {
			continue
		}
		groupName := strings.Join(strings.Fields(strings.TrimSpace(row[0])), " ")
		purpose := strings.TrimSpace(row[1])
		keyword := strings.TrimSpace(row[2])
		if groupName == "" || keyword == "" {
			continue
		}
		if _, ok := purposes[groupName]; !ok {
			purposes[groupName] = purpose
		}
		if keywords[groupName] == nil {
			keywords[groupName] = map[string]bool{}
		}
		keywords[groupName][keyword] = true
	}
	return purposes, keywords
}

func groupNameForSeedKeyword(t *testing.T, groups []models.SmartKeywordGroup, keyword models.SmartKeyword) string {
	t.Helper()
	for _, group := range groups {
		if keyword.GroupID == group.ID {
			return group.Name
		}
	}
	t.Fatalf("keyword %q has no seeded group", keyword.Value)
	return ""
}
