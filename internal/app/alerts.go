package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type OperationalAlert struct {
	Code     string
	Name     string
	Severity string
	Summary  string
	Details  []HealthDetail
}

type operationalAlertResponse struct {
	GeneratedAt string             `json:"generated_at"`
	Alerts      []OperationalAlert `json:"alerts"`
}

type AlertNotifier struct {
	webhookURL string
	client     *http.Client

	mu     sync.Mutex
	active map[string]string
}

func NewAlertNotifier(webhookURL string) *AlertNotifier {
	webhookURL = strings.TrimSpace(webhookURL)
	if webhookURL == "" {
		return nil
	}
	return &AlertNotifier{
		webhookURL: webhookURL,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		active: map[string]string{},
	}
}

func (a *App) HealthAlertsJSON(w http.ResponseWriter, r *http.Request) {
	_, _, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !canViewPlatformHealth(m.Role) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	now := time.Now().UTC()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(operationalAlertResponse{
		GeneratedAt: now.Format(time.RFC3339),
		Alerts:      a.operationalAlerts(r.Context(), now),
	})
}

func (a *App) RunAlertMonitor(ctx context.Context) {
	if a.AlertNotifier == nil || a.Config.AlertEvalSeconds <= 0 {
		return
	}
	a.evaluateAndNotifyAlerts(ctx)
	ticker := time.NewTicker(time.Duration(a.Config.AlertEvalSeconds) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.evaluateAndNotifyAlerts(ctx)
		}
	}
}

func (a *App) evaluateAndNotifyAlerts(ctx context.Context) {
	now := time.Now().UTC()
	a.AlertNotifier.Sync(ctx, a.operationalAlerts(ctx, now), now)
}

func (a *App) operationalAlerts(ctx context.Context, now time.Time) []OperationalAlert {
	alerts := make([]OperationalAlert, 0, 6)
	if alert, ok := a.backupAlert(now); ok {
		alerts = append(alerts, alert)
	}
	if alert, ok := a.loginThrottleAlert(now); ok {
		alerts = append(alerts, alert)
	}
	if alert, ok := a.extractorHealthAlert(ctx); ok {
		alerts = append(alerts, alert)
	}
	if alert, ok := a.extractorFailureAlert(ctx); ok {
		alerts = append(alerts, alert)
	}
	if alert, ok := a.workerBacklogAlert(ctx, now); ok {
		alerts = append(alerts, alert)
	}
	sort.Slice(alerts, func(i, j int) bool {
		ri := alertSeverityRank(alerts[i].Severity)
		rj := alertSeverityRank(alerts[j].Severity)
		if ri != rj {
			return ri < rj
		}
		return alerts[i].Name < alerts[j].Name
	})
	return alerts
}

func alertSeverityRank(severity string) int {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical":
		return 0
	case "danger":
		return 1
	case "warning":
		return 2
	default:
		return 3
	}
}

func (a *App) backupAlert(now time.Time) (OperationalAlert, bool) {
	dir := strings.TrimSpace(a.Config.BackupDir)
	if dir == "" {
		return OperationalAlert{
			Code:     "backup_dir_unconfigured",
			Name:     "Backups",
			Severity: "warning",
			Summary:  "Backup directory is not configured for alert checks.",
		}, true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return OperationalAlert{
			Code:     "backup_dir_unreadable",
			Name:     "Backups",
			Severity: "danger",
			Summary:  "Backup directory could not be read.",
			Details: []HealthDetail{
				{Label: "Backup dir", Value: filepath.Clean(dir)},
				{Label: "Error", Value: err.Error()},
			},
		}, true
	}
	var latestName string
	var latestMod time.Time
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(latestMod) {
			latestMod = info.ModTime()
			latestName = entry.Name()
		}
	}
	if latestMod.IsZero() {
		return OperationalAlert{
			Code:     "backup_missing",
			Name:     "Backups",
			Severity: "danger",
			Summary:  "No backup files were found in the configured backup directory.",
			Details:  []HealthDetail{{Label: "Backup dir", Value: filepath.Clean(dir)}},
		}, true
	}
	age := now.Sub(latestMod.UTC())
	maxAge := time.Duration(a.Config.AlertBackupMaxAgeMinutes) * time.Minute
	if age <= maxAge {
		return OperationalAlert{}, false
	}
	return OperationalAlert{
		Code:     "backup_stale",
		Name:     "Backups",
		Severity: "danger",
		Summary:  "The most recent backup is older than the configured freshness threshold.",
		Details: []HealthDetail{
			{Label: "Latest backup", Value: latestName},
			{Label: "Backup age", Value: age.Truncate(time.Minute).String()},
			{Label: "Allowed age", Value: maxAge.String()},
			{Label: "Backup dir", Value: filepath.Clean(dir)},
		},
	}, true
}

func (a *App) loginThrottleAlert(now time.Time) (OperationalAlert, bool) {
	if a.LoginRateLimiter == nil {
		return OperationalAlert{}, false
	}
	snapshot := a.LoginRateLimiter.Snapshot(now)
	if snapshot.RecentBlockedEvents < a.Config.AlertLoginThrottleThreshold {
		return OperationalAlert{}, false
	}
	return OperationalAlert{
		Code:     "login_throttling",
		Name:     "Login throttling",
		Severity: "warning",
		Summary:  "Repeated login throttling events were observed within the active limiter window.",
		Details: []HealthDetail{
			{Label: "Recent throttle events", Value: intString(snapshot.RecentBlockedEvents)},
			{Label: "Active blocked keys", Value: intString(snapshot.ActiveBlockedKeys)},
			{Label: "Limiter window", Value: snapshot.Window.String()},
			{Label: "Alert threshold", Value: intString(a.Config.AlertLoginThrottleThreshold)},
		},
	}, true
}

func (a *App) extractorHealthAlert(ctx context.Context) (OperationalAlert, bool) {
	if a.Extractor == nil || strings.TrimSpace(a.Config.ExtractorURL) == "" {
		return OperationalAlert{}, false
	}
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := a.Extractor.Healthz(checkCtx); err == nil {
		return OperationalAlert{}, false
	} else {
		return OperationalAlert{
			Code:     "extractor_unhealthy",
			Name:     "Extractor",
			Severity: "critical",
			Summary:  "The extractor service failed its health check.",
			Details: []HealthDetail{
				{Label: "Base URL", Value: a.Config.ExtractorURL},
				{Label: "Error", Value: err.Error()},
			},
		}, true
	}
}

func (a *App) extractorFailureAlert(ctx context.Context) (OperationalAlert, bool) {
	snapshot, err := a.Store.JobAlertSnapshot(ctx)
	if err != nil {
		return OperationalAlert{
			Code:     "extractor_failures_unknown",
			Name:     "Extraction failures",
			Severity: "warning",
			Summary:  "Extraction failure counts could not be evaluated.",
			Details:  []HealthDetail{{Label: "Error", Value: err.Error()}},
		}, true
	}
	if snapshot.Failed+snapshot.Retry < a.Config.AlertExtractorFailureThreshold {
		return OperationalAlert{}, false
	}
	return OperationalAlert{
		Code:     "extractor_failures_accumulating",
		Name:     "Extraction failures",
		Severity: "warning",
		Summary:  "Document extraction failures are accumulating beyond the configured threshold.",
		Details: []HealthDetail{
			{Label: "Failed jobs", Value: intString(snapshot.Failed)},
			{Label: "Retry jobs", Value: intString(snapshot.Retry)},
			{Label: "Alert threshold", Value: intString(a.Config.AlertExtractorFailureThreshold)},
		},
	}, true
}

func (a *App) workerBacklogAlert(ctx context.Context, now time.Time) (OperationalAlert, bool) {
	snapshot, err := a.Store.JobAlertSnapshot(ctx)
	if err != nil {
		return OperationalAlert{
			Code:     "worker_backlog_unknown",
			Name:     "Worker backlog",
			Severity: "warning",
			Summary:  "Worker backlog metrics could not be evaluated.",
			Details:  []HealthDetail{{Label: "Error", Value: err.Error()}},
		}, true
	}
	pending := snapshot.Queued + snapshot.Retry + snapshot.Processing
	oldestAge := time.Duration(0)
	if !snapshot.OldestPendingAt.IsZero() {
		oldestAge = now.Sub(snapshot.OldestPendingAt.UTC())
	}
	maxAge := time.Duration(a.Config.AlertBacklogMaxAgeMinutes) * time.Minute
	if pending < a.Config.AlertBacklogMaxJobs && oldestAge <= maxAge {
		return OperationalAlert{}, false
	}
	return OperationalAlert{
		Code:     "worker_backlog_growth",
		Name:     "Worker backlog",
		Severity: "warning",
		Summary:  "Extraction backlog is growing beyond the configured volume or age threshold.",
		Details: []HealthDetail{
			{Label: "Pending jobs", Value: intString(pending)},
			{Label: "Pending threshold", Value: intString(a.Config.AlertBacklogMaxJobs)},
			{Label: "Oldest pending age", Value: oldestAge.Truncate(time.Minute).String()},
			{Label: "Age threshold", Value: maxAge.String()},
		},
	}, true
}

func intString(value int) string {
	return strconv.Itoa(value)
}

func (n *AlertNotifier) Sync(ctx context.Context, alerts []OperationalAlert, now time.Time) {
	if n == nil {
		return
	}
	current := make(map[string]string, len(alerts))
	for _, alert := range alerts {
		current[alert.Code] = alert.Summary
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	for _, alert := range alerts {
		if previous, ok := n.active[alert.Code]; ok && previous == alert.Summary {
			continue
		}
		if err := n.send(ctx, "firing", alert, now); err != nil {
			log.Printf("alert webhook send failed for code=%s: %v", alert.Code, err)
			continue
		}
		n.active[alert.Code] = alert.Summary
	}
	for code, summary := range n.active {
		if _, ok := current[code]; ok {
			continue
		}
		alert := OperationalAlert{
			Code:     code,
			Name:     code,
			Severity: "info",
			Summary:  summary,
		}
		if err := n.send(ctx, "resolved", alert, now); err != nil {
			log.Printf("alert webhook resolution send failed for code=%s: %v", code, err)
			continue
		}
		delete(n.active, code)
	}
}

func (n *AlertNotifier) send(ctx context.Context, status string, alert OperationalAlert, now time.Time) error {
	payload := map[string]any{
		"app":         "openbid",
		"status":      status,
		"code":        alert.Code,
		"name":        alert.Name,
		"severity":    alert.Severity,
		"summary":     alert.Summary,
		"details":     alert.Details,
		"observed_at": now.Format(time.RFC3339),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.webhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %d", resp.StatusCode)
	}
	return nil
}
