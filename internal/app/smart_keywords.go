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
	a.render(w, r, "smart_keywords.html", map[string]any{
		"Title":        "Smart Keyword Extraction",
		"User":         u,
		"Tenant":       t,
		"Settings":     settings,
		"Groups":       groupViews,
		"GroupOptions": groups,
		"Standalone":   standalone,
		"Views":        views,
		"Deliveries":   deliveries,
		"Preview":      preview,
	})
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
