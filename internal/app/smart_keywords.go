package app

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"openbid/internal/mail"
	"openbid/internal/models"
)

func smartKeywordSeedCSVPath() string {
	candidates := []string{filepath.Join("internal", "seeddata", "africa_water_wastewater_tender_keywords.csv")}
	if _, filename, _, ok := runtime.Caller(0); ok {
		candidates = append(candidates, filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", "internal", "seeddata", "africa_water_wastewater_tender_keywords.csv")))
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return candidates[0]
}

type SmartKeywordGroupView struct {
	Group    models.SmartKeywordGroup
	Keywords []models.SmartKeyword
}

type smartKeywordReadiness struct {
	Ready               bool
	ActiveInputCount    int
	StatusTone          string
	StatusLabel         string
	StatusMessage       string
	DependencyMessage   string
	EnableControlLocked bool
}

type smartKeywordPageSummary struct {
	ActiveKeywords       int
	TotalKeywords        int
	StandaloneKeywords   int
	KeywordGroups        int
	EnabledGroups        int
	PreviewMatches       int
	PreviewTotal         int
	SavedViews           int
	AlertDeliveries      int
	LastReprocessedLabel string
}

func (a *App) SmartKeywordsPage(w http.ResponseWriter, r *http.Request) {
	u, t, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	settings, _ := a.Store.GetSmartExtractionSettings(r.Context(), t.ID)
	groups, _ := a.Store.ListSmartKeywordGroups(r.Context(), t.ID)
	keywords, _ := a.Store.ListSmartKeywords(r.Context(), t.ID)
	views, _ := a.Store.ListSavedSmartViews(r.Context(), t.ID, u.ID)
	deliveries, _ := a.Store.ListSmartAlertDeliveries(r.Context(), t.ID, "")
	preview, _ := a.Store.PreviewSmartKeywords(r.Context(), t.ID, 50)
	groupViews := smartKeywordGroupViews(groups, keywords)
	standalone := []models.SmartKeyword{}
	for _, keyword := range keywords {
		if strings.TrimSpace(keyword.GroupID) == "" {
			standalone = append(standalone, keyword)
		}
	}
	readiness := smartKeywordSettingsReadiness(settings, groups, keywords)
	emailReadiness := mail.Readiness{Status: "not_configured", StatusLabel: "Email not configured", StatusTone: "warning", Summary: "Admin email settings have not been configured."}
	if a.Email != nil {
		if current, _, err := a.Email.Readiness(r.Context()); err == nil {
			emailReadiness = current
		}
	}
	summary := smartKeywordSummary(settings, readiness, groups, keywords, standalone, views, deliveries, preview)
	a.render(w, r, "smart_keywords.html", map[string]any{
		"Title":                 "Smart Keyword Extraction",
		"User":                  u,
		"Tenant":                t,
		"Settings":              settings,
		"SmartKeywordReadiness": readiness,
		"EmailReadiness":        emailReadiness,
		"Summary":               summary,
		"Groups":                groupViews,
		"GroupOptions":          groups,
		"Standalone":            standalone,
		"Views":                 views,
		"Deliveries":            deliveries,
		"Preview":               preview,
	})
}

func smartKeywordSummary(settings models.SmartExtractionSettings, readiness smartKeywordReadiness, groups []models.SmartKeywordGroup, keywords []models.SmartKeyword, standalone []models.SmartKeyword, views []models.SavedSmartView, deliveries []models.SmartAlertDelivery, preview []models.SmartTenderPreview) smartKeywordPageSummary {
	enabledGroups := 0
	for _, group := range groups {
		if group.Enabled {
			enabledGroups++
		}
	}
	previewMatches := 0
	for _, item := range preview {
		if item.Evaluation.Accepted {
			previewMatches++
		}
	}
	return smartKeywordPageSummary{
		ActiveKeywords:       readiness.ActiveInputCount,
		TotalKeywords:        len(keywords),
		StandaloneKeywords:   len(standalone),
		KeywordGroups:        len(groups),
		EnabledGroups:        enabledGroups,
		PreviewMatches:       previewMatches,
		PreviewTotal:         len(preview),
		SavedViews:           len(views),
		AlertDeliveries:      len(deliveries),
		LastReprocessedLabel: smartKeywordLastReprocessedLabel(settings.LastReprocessedAt),
	}
}

func smartKeywordLastReprocessedLabel(value time.Time) string {
	if value.IsZero() {
		return "Not reprocessed yet"
	}
	return value.Local().Format("2006-01-02 15:04")
}

func (a *App) SmartKeywordGroupPage(w http.ResponseWriter, r *http.Request) {
	u, t, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	groupID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/smart-keywords/groups/"))
	if groupID == "" || strings.Contains(groupID, "/") {
		http.NotFound(w, r)
		return
	}
	groups, _ := a.Store.ListSmartKeywordGroups(r.Context(), t.ID)
	keywords, _ := a.Store.ListSmartKeywords(r.Context(), t.ID)
	for _, groupView := range smartKeywordGroupViews(groups, keywords) {
		if groupView.Group.ID == groupID {
			a.render(w, r, "smart_keyword_group.html", map[string]any{
				"Title":    "Smart Keyword Group",
				"User":     u,
				"Tenant":   t,
				"Group":    groupView.Group,
				"Keywords": groupView.Keywords,
				"ReturnTo": "/smart-keywords/groups/" + groupID,
			})
			return
		}
	}
	http.NotFound(w, r)
}

func smartKeywordGroupViews(groups []models.SmartKeywordGroup, keywords []models.SmartKeyword) []SmartKeywordGroupView {
	out := make([]SmartKeywordGroupView, 0, len(groups))
	for _, group := range groups {
		view := SmartKeywordGroupView{Group: group}
		for _, keyword := range keywords {
			if keyword.GroupID == group.ID {
				view.Keywords = append(view.Keywords, keyword)
			}
		}
		out = append(out, view)
	}
	return out
}

func smartKeywordSettingsReadiness(settings models.SmartExtractionSettings, groups []models.SmartKeywordGroup, keywords []models.SmartKeyword) smartKeywordReadiness {
	enabledGroups := map[string]bool{}
	for _, group := range groups {
		if group.Enabled {
			enabledGroups[group.ID] = true
		}
	}
	activeInputs := 0
	for _, keyword := range keywords {
		if !keyword.Enabled || strings.TrimSpace(keyword.NormalizedValue) == "" {
			continue
		}
		groupID := strings.TrimSpace(keyword.GroupID)
		if groupID == "" || enabledGroups[groupID] {
			activeInputs++
		}
	}
	ready := activeInputs > 0
	out := smartKeywordReadiness{
		Ready:             ready,
		ActiveInputCount:  activeInputs,
		DependencyMessage: "Activate at least one standalone keyword or keyword group before enabling this feature.",
		StatusTone:        "warning",
		StatusLabel:       "Disabled",
		StatusMessage:     "No active keywords or keyword groups found.",
	}
	if !ready {
		out.EnableControlLocked = !settings.Enabled
		if settings.Enabled {
			out.StatusLabel = "Unavailable"
			out.StatusMessage = "Unavailable until at least one keyword or keyword group is active."
		}
		return out
	}
	if settings.Enabled {
		out.StatusTone = "success"
		out.StatusLabel = "Enabled"
		out.StatusMessage = strings.TrimSpace(settings.RefreshMessage)
		if out.StatusMessage == "" {
			out.StatusMessage = "Smart extraction is active."
		}
		return out
	}
	out.StatusTone = "info"
	out.StatusLabel = "Disabled"
	out.StatusMessage = "Ready to enable; active keywords are available."
	return out
}

func (a *App) SaveSmartKeywordSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	u, t, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !canEditWorkspace(u, m) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	current, _ := a.Store.GetSmartExtractionSettings(r.Context(), t.ID)
	current.Enabled = r.FormValue("enabled") == "1"
	current.AlertsEnabled = r.FormValue("alerts_enabled") == "1"
	current.EmailAlertsEnabled = r.FormValue("email_alerts_enabled") == "1"
	if err := a.Store.UpsertSmartExtractionSettings(r.Context(), current); err != nil {
		a.redirectAfterAction(w, r, "/smart-keywords", "error", err.Error())
		return
	}
	a.redirectAfterAction(w, r, "/smart-keywords", "success", "Smart Keyword Extraction settings saved")
}

func (a *App) SaveSmartKeywordGroup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	_, t, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	group, err := a.Store.UpsertSmartKeywordGroup(r.Context(), models.SmartKeywordGroup{
		ID:            r.FormValue("id"),
		TenantID:      t.ID,
		Name:          r.FormValue("name"),
		TagName:       r.FormValue("tag_name"),
		Description:   r.FormValue("description"),
		Enabled:       r.FormValue("enabled") == "1",
		MatchMode:     models.SmartMatchMode(r.FormValue("match_mode")),
		ExcludeTerms:  splitCSV(r.FormValue("exclude_terms")),
		MinMatchCount: atoi(r.FormValue("min_match_count"), 1),
		Priority:      atoi(r.FormValue("priority"), 0),
	})
	if err != nil {
		a.redirectAfterAction(w, r, "/smart-keywords", "error", "Keyword group could not be saved")
		return
	}
	a.redirectAfterAction(w, r, smartKeywordReturnPath(r, "/smart-keywords/groups/"+group.ID), "success", "Keyword group saved")
}

func (a *App) DeleteSmartKeywordGroup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	_, t, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := a.Store.DeleteSmartKeywordGroup(r.Context(), t.ID, r.FormValue("id")); err != nil {
		a.redirectAfterAction(w, r, "/smart-keywords", "error", "Keyword group could not be deleted")
		return
	}
	a.redirectAfterAction(w, r, "/smart-keywords", "success", "Keyword group deleted")
}

func (a *App) SaveSmartKeyword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	_, t, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	enabled := r.FormValue("enabled") == "1"
	if r.FormValue("id") == "" && r.FormValue("enabled") == "" {
		enabled = true
	}
	_, err := a.Store.UpsertSmartKeyword(r.Context(), models.SmartKeyword{
		ID:       r.FormValue("id"),
		TenantID: t.ID,
		GroupID:  r.FormValue("group_id"),
		Value:    r.FormValue("value"),
		Enabled:  enabled,
	})
	if err != nil {
		a.redirectAfterAction(w, r, "/smart-keywords", "error", "Keyword could not be saved")
		return
	}
	a.redirectAfterAction(w, r, smartKeywordReturnPath(r, "/smart-keywords"), "success", "Keyword saved")
}

func (a *App) DeleteSmartKeyword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	_, t, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := a.Store.DeleteSmartKeyword(r.Context(), t.ID, r.FormValue("id")); err != nil {
		a.redirectAfterAction(w, r, "/smart-keywords", "error", "Keyword could not be deleted")
		return
	}
	a.redirectAfterAction(w, r, smartKeywordReturnPath(r, "/smart-keywords"), "success", "Keyword deleted")
}

func smartKeywordReturnPath(r *http.Request, fallback string) string {
	returnTo := strings.TrimSpace(r.FormValue("return_to"))
	if returnTo == "/smart-keywords" || strings.HasPrefix(returnTo, "/smart-keywords/groups/") {
		return returnTo
	}
	return fallback
}

func (a *App) ReprocessSmartKeywords(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	_, t, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	result, err := a.Store.ReprocessSmartKeywords(r.Context(), t.ID)
	if err != nil {
		a.redirectAfterAction(w, r, "/smart-keywords", "error", "Reprocessing failed")
		return
	}
	a.redirectAfterAction(w, r, "/smart-keywords", "success", "Reprocessed "+strconv.Itoa(result.Processed)+" tenders")
}

func (a *App) SaveSmartView(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	u, t, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	filter := models.SmartViewFilters{
		Query:     r.FormValue("query"),
		Source:    r.FormValue("source"),
		Issuer:    r.FormValue("issuer"),
		Category:  r.FormValue("category"),
		Status:    r.FormValue("status"),
		GroupTags: splitCSV(r.FormValue("group_tags")),
	}
	filtersJSON, _ := json.Marshal(filter)
	channels := []models.NotificationChannel{}
	for _, typ := range r.Form["channel_type"] {
		typ = strings.TrimSpace(typ)
		if typ == "" {
			continue
		}
		destination := strings.TrimSpace(r.FormValue(typ + "_destination"))
		channels = append(channels, models.NotificationChannel{ID: typ, Type: typ, Destination: destination, Enabled: destination != ""})
	}
	_, err := a.Store.UpsertSavedSmartView(r.Context(), models.SavedSmartView{
		ID:             r.FormValue("id"),
		TenantID:       t.ID,
		UserID:         u.ID,
		Name:           r.FormValue("name"),
		FiltersJSON:    string(filtersJSON),
		Pinned:         r.FormValue("pinned") == "1",
		AlertsEnabled:  r.FormValue("alerts_enabled") == "1",
		AlertPaused:    r.FormValue("alert_paused") == "1",
		AlertFrequency: r.FormValue("alert_frequency"),
		AlertChannels:  channels,
	})
	if err != nil {
		a.redirectAfterAction(w, r, "/smart-keywords", "error", "Saved Smart View could not be saved")
		return
	}
	a.redirectAfterAction(w, r, "/smart-keywords", "success", "Saved Smart View saved")
}

func (a *App) DeleteSmartView(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	u, t, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := a.Store.DeleteSavedSmartView(r.Context(), t.ID, u.ID, r.FormValue("id")); err != nil {
		a.redirectAfterAction(w, r, "/smart-keywords", "error", "Saved Smart View could not be deleted")
		return
	}
	a.redirectAfterAction(w, r, "/smart-keywords", "success", "Saved Smart View deleted")
}

func (a *App) TestSmartViewAlert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	u, t, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if _, err := a.Store.TestSmartViewAlert(r.Context(), t.ID, u.ID, r.FormValue("id")); err != nil {
		a.redirectAfterAction(w, r, "/smart-keywords", "error", "Test alert failed")
		return
	}
	a.redirectAfterAction(w, r, "/smart-keywords", "success", "Test alert recorded")
}

func splitCSV(value string) []string {
	out := []string{}
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func smartOpenURL(view models.SavedSmartView) string {
	var filter models.SmartViewFilters
	_ = json.Unmarshal([]byte(view.FiltersJSON), &filter)
	query := url.Values{}
	query.Set("q", filter.Query)
	query.Set("source", filter.Source)
	query.Set("issuer", filter.Issuer)
	query.Set("category", filter.Category)
	query.Set("status", filter.Status)
	if len(filter.GroupTags) > 0 {
		query.Set("group_tag", filter.GroupTags[0])
	}
	return "/tenders?" + query.Encode()
}
