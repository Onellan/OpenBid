package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"openbid/internal/models"
)

func TestLoginRateLimiterSnapshotCountsRecentBlocks(t *testing.T) {
	limiter := NewLoginRateLimiter(10*time.Minute, 2)
	now := time.Date(2026, 4, 4, 8, 0, 0, 0, time.UTC)
	limiter.RegisterFailure("203.0.113.10", now)
	limiter.RegisterFailure("203.0.113.10", now.Add(time.Minute))

	snapshot := limiter.Snapshot(now.Add(2 * time.Minute))
	if snapshot.ActiveBlockedKeys != 1 || snapshot.RecentBlockedEvents != 1 {
		t.Fatalf("unexpected limiter snapshot: %#v", snapshot)
	}

	snapshot = limiter.Snapshot(now.Add(11 * time.Minute))
	if snapshot.ActiveBlockedKeys != 0 || snapshot.RecentBlockedEvents != 0 {
		t.Fatalf("expected limiter snapshot to age out, got %#v", snapshot)
	}
}

func TestHealthAlertsJSONShowsBackupAlertForAdmins(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, _ := adminSession(t, a)
	emptyDir := filepath.Join(t.TempDir(), "empty-backups")
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	a.Config.BackupDir = emptyDir

	req := httptest.NewRequest(http.MethodGet, "/health/alerts.json", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", w.Code)
	}

	var payload operationalAlertResponse
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Alerts) == 0 || payload.Alerts[0].Code != "backup_missing" {
		t.Fatalf("expected backup_missing alert, got %#v", payload.Alerts)
	}
}

func TestHealthAlertsJSONIsForbiddenForViewer(t *testing.T) {
	a := newTestApp(t)
	_, _, cookie, _ := sessionForRole(t, a, models.TenantRoleViewer)

	req := httptest.NewRequest(http.MethodGet, "/health/alerts.json", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Server.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 got %d", w.Code)
	}
}

func TestOperationalAlertsIncludeBacklogAndExtractorFailures(t *testing.T) {
	a := newTestApp(t)
	a.Config.AlertBacklogMaxJobs = 1
	a.Config.AlertBacklogMaxAgeMinutes = 30
	a.Config.AlertExtractorFailureThreshold = 1

	tender := models.Tender{
		ID:          "alert-tender",
		Title:       "Alert tender",
		Issuer:      "City",
		SourceKey:   "treasury",
		DocumentURL: "https://example.org/doc.pdf",
	}
	if err := a.Store.UpsertTender(t.Context(), tender); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.QueueJob(t.Context(), models.ExtractionJob{
		ID:          "job-failed",
		TenderID:    tender.ID,
		DocumentURL: tender.DocumentURL,
		State:       models.ExtractionFailed,
		CreatedAt:   time.Now().Add(-2 * time.Hour).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.QueueJob(t.Context(), models.ExtractionJob{
		ID:            "job-queued",
		TenderID:      tender.ID,
		DocumentURL:   "https://example.org/doc-2.pdf",
		State:         models.ExtractionQueued,
		CreatedAt:     time.Now().Add(-2 * time.Hour).UTC(),
		NextAttemptAt: time.Now().Add(-90 * time.Minute).UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	alerts := a.operationalAlerts(t.Context(), time.Now().UTC())
	joined := make([]string, 0, len(alerts))
	for _, alert := range alerts {
		joined = append(joined, alert.Code)
	}
	got := strings.Join(joined, ",")
	if !strings.Contains(got, "extractor_failures_accumulating") {
		t.Fatalf("expected extractor failure alert, got %s", got)
	}
	if !strings.Contains(got, "worker_backlog_growth") {
		t.Fatalf("expected backlog alert, got %s", got)
	}
}

func TestAlertNotifierSyncFiresAndResolvesWithoutDuplicatePosts(t *testing.T) {
	type webhookPayload struct {
		Status  string `json:"status"`
		Code    string `json:"code"`
		Summary string `json:"summary"`
	}

	var (
		mu       sync.Mutex
		received []webhookPayload
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var payload webhookPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		mu.Lock()
		received = append(received, payload)
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	notifier := NewAlertNotifier(server.URL)
	if notifier == nil {
		t.Fatal("expected notifier to be created")
	}

	now := time.Date(2026, 4, 4, 12, 0, 0, 0, time.UTC)
	alert := OperationalAlert{
		Code:     "worker_backlog_growth",
		Name:     "Worker backlog",
		Severity: "warning",
		Summary:  "Backlog is growing",
	}

	notifier.Sync(t.Context(), []OperationalAlert{alert}, now)
	notifier.Sync(t.Context(), []OperationalAlert{alert}, now.Add(time.Minute))

	alert.Summary = "Backlog is still growing"
	notifier.Sync(t.Context(), []OperationalAlert{alert}, now.Add(2*time.Minute))
	notifier.Sync(t.Context(), nil, now.Add(3*time.Minute))

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 3 {
		t.Fatalf("expected 3 webhook posts, got %d: %#v", len(received), received)
	}
	if received[0].Status != "firing" || received[0].Code != "worker_backlog_growth" || received[0].Summary != "Backlog is growing" {
		t.Fatalf("unexpected first payload: %#v", received[0])
	}
	if received[1].Status != "firing" || received[1].Summary != "Backlog is still growing" {
		t.Fatalf("unexpected second payload: %#v", received[1])
	}
	if received[2].Status != "resolved" || received[2].Code != "worker_backlog_growth" || received[2].Summary != "Backlog is still growing" {
		t.Fatalf("unexpected resolution payload: %#v", received[2])
	}
}
