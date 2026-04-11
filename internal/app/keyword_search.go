package app

import (
	"net/http"
	"net/url"
	"strconv"

	"openbid/internal/models"
	"openbid/internal/store"
)

func keywordMatchFilterFromRequest(r *http.Request) store.KeywordMatchFilter {
	pageSize := atoi(r.URL.Query().Get("page_size"), 20)
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	return store.KeywordMatchFilter{
		Query:    r.URL.Query().Get("q"),
		Source:   r.URL.Query().Get("source"),
		Province: r.URL.Query().Get("province"),
		Status:   r.URL.Query().Get("status"),
		Keyword:  r.URL.Query().Get("keyword"),
		Sort:     r.URL.Query().Get("sort"),
		Page:     atoi(r.URL.Query().Get("page"), 1),
		PageSize: pageSize,
	}
}

func keywordPageLink(values url.Values, page int) string {
	return queryPageLink("/keyword-search", values, "page", page)
}

func (a *App) KeywordSearchPage(w http.ResponseWriter, r *http.Request) {
	u, t, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	filter := keywordMatchFilterFromRequest(r)
	keywords, _ := a.Store.ListKeywords(r.Context(), t.ID, u.ID)
	summary, _ := a.Store.KeywordSearchSummary(r.Context(), t.ID, u.ID)
	filterOptions, _ := a.Store.TenderFilterOptions(r.Context(), t.ID)
	items, total, err := a.Store.ListKeywordTenderMatches(r.Context(), t.ID, u.ID, filter)
	if err != nil {
		a.serverError(w, r, "unable to load keyword matches", err)
		return
	}
	totalPages := total / filter.PageSize
	if total%filter.PageSize != 0 {
		totalPages++
	}
	if totalPages == 0 {
		totalPages = 1
	}
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.Page > totalPages {
		filter.Page = totalPages
	}
	query := r.URL.Query()
	a.render(w, r, "keyword_search.html", map[string]any{
		"Title":         "Keyword Search",
		"User":          u,
		"Tenant":        t,
		"Summary":       summary,
		"Keywords":      keywords,
		"Items":         items,
		"Total":         total,
		"Filter":        filter,
		"FilterOptions": tenderFilterOptionsForView(filterOptions, store.ListFilter{Source: filter.Source, Province: filter.Province, Status: filter.Status}),
		"ReturnTo":      r.URL.RequestURI(),
		"HasKeywords":   len(keywords) > 0,
		"CurrentPage":   filter.Page,
		"TotalPages":    totalPages,
		"HasPrevPage":   filter.Page > 1,
		"HasNextPage":   filter.Page < totalPages,
		"PrevPageURL":   keywordPageLink(query, filter.Page-1),
		"NextPageURL":   keywordPageLink(query, filter.Page+1),
	})
}

func (a *App) SaveKeyword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	u, t, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	enabled := r.FormValue("enabled") == "1"
	if r.FormValue("id") == "" && r.FormValue("enabled") == "" {
		enabled = true
	}
	keyword, err := a.Store.UpsertKeyword(r.Context(), models.Keyword{
		ID:       r.FormValue("id"),
		TenantID: t.ID,
		UserID:   u.ID,
		Value:    r.FormValue("value"),
		Enabled:  enabled,
	})
	if err != nil {
		a.redirectAfterAction(w, r, "/keyword-search", "error", "Keyword could not be saved")
		return
	}
	a.auditAction(r.Context(), actionContext{User: u, Tenant: t}, "update", "keyword", keyword.ID, "Keyword saved", map[string]string{"keyword": keyword.Value})
	a.redirectAfterAction(w, r, "/keyword-search", "success", "Keyword saved and matches refreshed")
}

func (a *App) DeleteKeyword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	u, t, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	id := r.FormValue("id")
	if err := a.Store.DeleteKeyword(r.Context(), t.ID, u.ID, id); err != nil {
		a.redirectAfterAction(w, r, "/keyword-search", "error", "Keyword could not be deleted")
		return
	}
	a.auditAction(r.Context(), actionContext{User: u, Tenant: t}, "delete", "keyword", id, "Keyword deleted", nil)
	a.redirectAfterAction(w, r, "/keyword-search", "success", "Keyword deleted and matches refreshed")
}

func (a *App) RefreshKeywordSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	u, t, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	summary, err := a.Store.RefreshKeywordMatches(r.Context(), t.ID, u.ID)
	if err != nil {
		a.redirectAfterAction(w, r, "/keyword-search", "error", "Keyword refresh failed")
		return
	}
	a.auditAction(r.Context(), actionContext{User: u, Tenant: t}, "refresh", "keyword_search", summary.Profile.ID, "Keyword matches refreshed", map[string]string{"matches": strconv.Itoa(summary.MatchedTenderCount)})
	a.redirectAfterAction(w, r, "/keyword-search", "success", "Keyword matches refreshed")
}
