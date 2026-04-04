package app

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"openbid/internal/models"
	"openbid/internal/store"
)

type QueueItem struct {
	Job    models.ExtractionJob
	Tender models.Tender
}

type QueueSection struct {
	Key         string
	Title       string
	Tone        string
	Items       []QueueItem
	PageItems   []QueueItem
	CurrentPage int
	TotalPages  int
	HasPrevPage bool
	HasNextPage bool
	PrevPageURL string
	NextPageURL string
	Open        bool
}

type QueueSummary struct {
	Queued     int
	Processing int
	Failed     int
	Completed  int
}

type BookmarkedTender struct {
	Bookmark models.Bookmark
	Tender   models.Tender
	Workflow models.Workflow
}

type detailFactSection struct {
	Title string
	Facts map[string]string
}

func queueSummary(jobs []models.ExtractionJob) QueueSummary {
	summary := QueueSummary{}
	for _, job := range jobs {
		switch job.State {
		case models.ExtractionQueued:
			summary.Queued++
		case models.ExtractionProcessing:
			summary.Processing++
		case models.ExtractionFailed:
			summary.Failed++
		case models.ExtractionCompleted:
			summary.Completed++
		}
	}
	return summary
}

func cloneURLValues(values url.Values) url.Values {
	cloned := url.Values{}
	for key, current := range values {
		cloned[key] = append([]string{}, current...)
	}
	return cloned
}

func queuePageLink(values url.Values, param string, page int) string {
	return queryPageLink("/queue", values, param, page)
}

func queryPageLink(path string, values url.Values, param string, page int) string {
	if page < 1 {
		page = 1
	}
	query := cloneURLValues(values)
	query.Set(param, strconv.Itoa(page))
	encoded := query.Encode()
	if encoded == "" {
		return path
	}
	return path + "?" + encoded
}

func buildQueueSection(key, title, tone string, items []QueueItem, query url.Values, open bool) QueueSection {
	const pageSize = 10
	param := key + "_page"
	currentPage, err := strconv.Atoi(query.Get(param))
	if err != nil || currentPage < 1 {
		currentPage = 1
	}
	total := len(items)
	totalPages := total / pageSize
	if total%pageSize != 0 {
		totalPages++
	}
	if totalPages == 0 {
		totalPages = 1
	}
	if currentPage > totalPages {
		currentPage = totalPages
	}
	start := (currentPage - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	pageItems := items[start:end]
	section := QueueSection{
		Key:         key,
		Title:       title,
		Tone:        tone,
		Items:       items,
		PageItems:   pageItems,
		CurrentPage: currentPage,
		TotalPages:  totalPages,
		Open:        open,
	}
	if currentPage > 1 {
		section.HasPrevPage = true
		section.PrevPageURL = queuePageLink(query, param, currentPage-1)
	}
	if currentPage < totalPages {
		section.HasNextPage = true
		section.NextPageURL = queuePageLink(query, param, currentPage+1)
	}
	return section
}

func (a *App) listRenderableJobs(ctx context.Context) []models.ExtractionJob {
	jobs, _ := a.Store.ListJobs(ctx)
	if len(jobs) == 0 {
		return jobs
	}
	pruned := false
	filtered := make([]models.ExtractionJob, 0, len(jobs))
	for _, job := range jobs {
		if strings.TrimSpace(job.TenderID) == "" {
			if job.ID != "" {
				_ = a.Store.DeleteJob(ctx, job.ID)
			}
			pruned = true
			continue
		}
		if _, err := a.Store.GetTender(ctx, job.TenderID); err != nil {
			if job.ID != "" {
				_ = a.Store.DeleteJob(ctx, job.ID)
			}
			pruned = true
			continue
		}
		filtered = append(filtered, job)
	}
	if pruned {
		return filtered
	}
	return jobs
}

func csvJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil || len(data) == 0 || string(data) == "null" {
		return ""
	}
	return string(data)
}

func joinedNames(contacts []models.TenderContact) string {
	out := []string{}
	for _, contact := range contacts {
		if name := strings.TrimSpace(contact.Name); name != "" {
			out = append(out, name)
		}
	}
	return strings.Join(out, "; ")
}

func joinedDocumentNames(documents []models.TenderDocument) string {
	out := []string{}
	for _, document := range documents {
		if name := strings.TrimSpace(document.FileName); name != "" {
			out = append(out, name)
		}
	}
	return strings.Join(out, "; ")
}

func factSectionsForTender(item models.Tender) []detailFactSection {
	sections := []detailFactSection{}
	if len(item.PageFacts) > 0 {
		sections = append(sections, detailFactSection{Title: "Page-derived facts", Facts: item.PageFacts})
	}
	if len(item.DocumentFacts) > 0 {
		sections = append(sections, detailFactSection{Title: "Document-derived facts", Facts: item.DocumentFacts})
	}
	if len(item.ExtractedFacts) > 0 {
		sections = append(sections, detailFactSection{Title: "Combined extracted facts", Facts: item.ExtractedFacts})
	}
	return sections
}

func (a *App) bookmarkedTenders(ctx context.Context, tenantID, userID string) ([]BookmarkedTender, error) {
	bookmarks, err := a.Store.ListBookmarks(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}
	workflows, _ := a.Store.ListWorkflows(ctx, tenantID)
	workflowByTender := map[string]models.Workflow{}
	for _, wf := range workflows {
		workflowByTender[wf.TenderID] = wf
	}
	items := make([]BookmarkedTender, 0, len(bookmarks))
	for _, bookmark := range bookmarks {
		tender, err := a.Store.GetTender(ctx, bookmark.TenderID)
		if err != nil {
			continue
		}
		items = append(items, BookmarkedTender{
			Bookmark: bookmark,
			Tender:   tender,
			Workflow: workflowByTender[tender.ID],
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Bookmark.UpdatedAt.After(items[j].Bookmark.UpdatedAt) })
	return items, nil
}

func (a *App) Home(w http.ResponseWriter, r *http.Request) {
	u, t, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	d, _ := a.Store.Dashboard(r.Context(), t.ID, a.Config.LowMemoryMode, false)
	bookmarks, _ := a.Store.ListBookmarks(r.Context(), t.ID, u.ID)
	searches, _ := a.Store.ListSavedSearches(r.Context(), t.ID, u.ID)
	jobs := a.listRenderableJobs(r.Context())
	a.render(w, r, "home.html", map[string]any{
		"Title":         "Home",
		"User":          u,
		"Tenant":        t,
		"Dashboard":     d,
		"BookmarkCount": len(bookmarks),
		"SavedCount":    len(searches),
		"QueueSummary":  queueSummary(jobs),
	})
}

func (a *App) Dashboard(w http.ResponseWriter, r *http.Request) {
	a.Home(w, r)
}

func (a *App) BookmarksPage(w http.ResponseWriter, r *http.Request) {
	u, t, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	items, _ := a.bookmarkedTenders(r.Context(), t.ID, u.ID)
	a.render(w, r, "bookmarks.html", map[string]any{
		"Title":         "Bookmarks",
		"User":          u,
		"Tenant":        t,
		"Items":         items,
		"BookmarkCount": len(items),
	})
}

func (a *App) Tenders(w http.ResponseWriter, r *http.Request) {
	u, t, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	pageSize := atoi(r.URL.Query().Get("page_size"), 20)
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	f := store.NormalizeFilter(store.ListFilter{
		Query: r.URL.Query().Get("q"), Source: r.URL.Query().Get("source"), Province: r.URL.Query().Get("province"),
		Category: r.URL.Query().Get("category"), Issuer: r.URL.Query().Get("issuer"), Status: r.URL.Query().Get("status"),
		CIDB: r.URL.Query().Get("cidb"), WorkflowStatus: r.URL.Query().Get("workflow_status"),
		DocumentStatus: r.URL.Query().Get("document_status"), BookmarkedOnly: r.URL.Query().Get("bookmarked_only") == "1",
		HasDocuments: r.URL.Query().Get("has_documents") == "1", Sort: r.URL.Query().Get("sort"), View: r.URL.Query().Get("view"),
		Page: atoi(r.URL.Query().Get("page"), 1), PageSize: pageSize, TenantID: t.ID, UserID: u.ID,
	})
	items, total, _ := a.Store.ListTenders(r.Context(), f)
	bookmarks, _ := a.Store.ListBookmarks(r.Context(), t.ID, u.ID)
	bookmarkByTender := map[string]models.Bookmark{}
	bookmarked := map[string]bool{}
	for _, bookmark := range bookmarks {
		bookmarkByTender[bookmark.TenderID] = bookmark
		bookmarked[bookmark.TenderID] = true
	}
	workflows, _ := a.Store.ListWorkflows(r.Context(), t.ID)
	workflowByTender := map[string]models.Workflow{}
	for _, workflow := range workflows {
		workflowByTender[workflow.TenderID] = workflow
	}
	params := map[string]string{
		"q": f.Query, "source": f.Source, "province": f.Province, "category": f.Category, "issuer": f.Issuer, "status": f.Status,
		"cidb": f.CIDB, "workflow_status": f.WorkflowStatus, "document_status": f.DocumentStatus, "sort": f.Sort, "view": f.View,
		"page_size": strconv.Itoa(f.PageSize),
	}
	if f.BookmarkedOnly {
		params["bookmarked_only"] = "1"
	}
	totalPages := total / f.PageSize
	if total%f.PageSize != 0 {
		totalPages++
	}
	if totalPages == 0 {
		totalPages = 1
	}
	a.render(w, r, "tenders.html", map[string]any{
		"Title": "Tenders", "User": u, "Tenant": t, "Items": items, "Total": total,
		"Filter": f, "Bookmarks": bookmarkByTender, "Bookmarked": bookmarked, "Workflows": workflowByTender,
		"ReturnTo":    r.URL.RequestURI(),
		"CurrentPage": f.Page, "TotalPages": totalPages, "HasPrevPage": f.Page > 1, "HasNextPage": f.Page < totalPages,
		"PrevPageURL": pageLink("/tenders", params, f.Page-1), "NextPageURL": pageLink("/tenders", params, f.Page+1),
	})
}

func (a *App) ExportCSV(w http.ResponseWriter, r *http.Request) {
	u, t, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	items, _, _ := a.Store.ListTenders(r.Context(), store.ListFilter{Query: r.URL.Query().Get("q"), TenantID: t.ID, UserID: u.ID, Page: 1, PageSize: 5000})
	workflows, _ := a.Store.ListWorkflows(r.Context(), t.ID)
	workflowByTender := map[string]models.Workflow{}
	for _, workflow := range workflows {
		workflowByTender[workflow.TenderID] = workflow
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", "attachment; filename=tenders.csv")
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{
		"id", "title", "issuer", "source", "province", "category", "tender_type", "tender_number",
		"published_date", "closing_date", "status", "relevance_score", "cidb_grading", "validity_days",
		"document_status", "workflow_status", "workflow_priority", "assigned_user",
		"document_url", "original_url", "excerpt", "scope",
		"document_count", "contact_count", "briefing_count", "requirement_count",
		"document_names", "contact_names",
		"submission_method", "submission_delivery_location", "submission_address", "electronic_submission", "physical_submission", "two_envelope_submission",
		"evaluation_method", "price_points", "preference_points", "minimum_functionality_score",
		"location_site", "location_delivery", "location_town", "location_postal_code", "location_province",
		"closing_details", "briefing_details", "submission_details", "contact_details", "cidb_hints",
		"documents_json", "contacts_json", "briefings_json", "requirements_json", "page_facts_json", "document_facts_json", "source_metadata_json",
	})
	for _, tender := range items {
		facts := tender.ExtractedFacts
		if facts == nil {
			facts = map[string]string{}
		}
		workflow := workflowByTender[tender.ID]
		_ = cw.Write([]string{
			tender.ID, tender.Title, tender.Issuer, tender.SourceKey, tender.Province, tender.Category, tender.TenderType, tender.TenderNumber,
			tender.PublishedDate, tender.ClosingDate, tender.Status, fmt.Sprintf("%.2f", tender.RelevanceScore), tender.CIDBGrading, strconv.Itoa(tender.ValidityDays),
			string(tender.DocumentStatus), workflow.Status, workflow.Priority, workflow.AssignedUser,
			tender.DocumentURL, tender.OriginalURL, tender.Excerpt, tender.Scope,
			strconv.Itoa(len(tender.Documents)), strconv.Itoa(len(tender.Contacts)), strconv.Itoa(len(tender.Briefings)), strconv.Itoa(len(tender.Requirements)),
			joinedDocumentNames(tender.Documents), joinedNames(tender.Contacts),
			tender.Submission.Method, tender.Submission.DeliveryLocation, tender.Submission.Address, strconv.FormatBool(tender.Submission.ElectronicAllowed), strconv.FormatBool(tender.Submission.PhysicalAllowed), strconv.FormatBool(tender.Submission.TwoEnvelope),
			tender.Evaluation.Method, strconv.Itoa(tender.Evaluation.PricePoints), strconv.Itoa(tender.Evaluation.PreferencePoints), fmt.Sprintf("%.2f", tender.Evaluation.MinimumFunctionalityScore),
			tender.Location.Site, tender.Location.DeliveryLocation, tender.Location.Town, tender.Location.PostalCode, tender.Location.Province,
			facts["closing_details"], facts["briefing_details"], facts["submission_details"], facts["contact_details"], facts["cidb_hints"],
			csvJSON(tender.Documents), csvJSON(tender.Contacts), csvJSON(tender.Briefings), csvJSON(tender.Requirements), csvJSON(tender.PageFacts), csvJSON(tender.DocumentFacts), csvJSON(tender.SourceMetadata),
		})
	}
	cw.Flush()
}

func (a *App) ToggleBookmark(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	u, t, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if err := a.Store.UpsertBookmark(r.Context(), models.Bookmark{TenantID: t.ID, UserID: u.ID, TenderID: r.FormValue("tender_id"), Note: r.FormValue("note")}); err != nil {
		a.serverError(w, r, "unable to save bookmark", err)
		return
	}
	a.auditAction(r.Context(), actionContext{User: u, Tenant: t}, "update", "bookmark", r.FormValue("tender_id"), "Bookmark saved", map[string]string{"note": r.FormValue("note")})
	a.redirectAfterAction(w, r, "/tenders", "success", "Bookmark saved")
}

func (a *App) UpdateWorkflow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	_, t, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if !canEditWorkspace(m.Role) {
		http.Error(w, "forbidden", 403)
		return
	}
	u, _, _, _ := a.currentUserTenant(r)
	workflow := models.Workflow{TenantID: t.ID, TenderID: r.FormValue("tender_id"), Status: r.FormValue("status"), Priority: r.FormValue("priority"), AssignedUser: r.FormValue("assigned_user"), Notes: r.FormValue("notes")}
	if err := a.Store.UpsertWorkflow(r.Context(), workflow); err != nil {
		a.serverError(w, r, "unable to update workflow", err)
		return
	}
	ac := actionContext{User: u, Tenant: t, Member: m}
	a.addWorkflowSnapshot(r.Context(), ac, workflow)
	a.auditAction(r.Context(), ac, "update", "workflow", workflow.TenderID, "Workflow updated", map[string]string{"status": workflow.Status, "priority": workflow.Priority})
	a.redirectAfterAction(w, r, "/tenders", "success", "Workflow updated")
}

func (a *App) QueueExtraction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	_, t, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if !canEditWorkspace(m.Role) {
		http.Error(w, "forbidden", 403)
		return
	}
	tender, err := a.Store.GetTender(r.Context(), r.FormValue("tender_id"))
	if err != nil {
		a.notFound(w, r, "tender not found", err)
		return
	}
	if tender.DocumentURL == "" {
		http.Error(w, "no document url", 400)
		return
	}
	tender.DocumentStatus = models.ExtractionQueued
	if err := a.Store.UpsertTender(r.Context(), tender); err != nil {
		a.serverError(w, r, "unable to update tender status", err)
		return
	}
	if err := a.Store.QueueJob(r.Context(), models.ExtractionJob{TenderID: tender.ID, DocumentURL: tender.DocumentURL, State: models.ExtractionQueued}); err != nil {
		a.serverError(w, r, "unable to queue extraction", err)
		return
	}
	ac := actionContext{Tenant: t, Member: m}
	if u, _, _, ok := a.currentUserTenant(r); ok {
		ac.User = u
	}
	a.auditAction(r.Context(), ac, "create", "queue_job", tender.ID, "Extraction queued", nil)
	a.redirectAfterAction(w, r, "/tenders", "success", "Extraction queued")
}

func (a *App) SavedSearches(w http.ResponseWriter, r *http.Request) {
	u, t, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if r.Method == http.MethodGet {
		items, _ := a.Store.ListSavedSearches(r.Context(), t.ID, u.ID)
		a.render(w, r, "saved_searches.html", map[string]any{"Title": "Saved searches", "User": u, "Tenant": t, "Items": items, "CSRFToken": a.mustCSRF(r)})
		return
	}
	if !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	if err := a.Store.UpsertSavedSearch(r.Context(), models.SavedSearch{ID: r.FormValue("id"), TenantID: t.ID, UserID: u.ID, Name: r.FormValue("name"), Query: r.FormValue("query"), Filters: r.FormValue("filters")}); err != nil {
		a.serverError(w, r, "unable to save saved search", err)
		return
	}
	a.auditAction(r.Context(), actionContext{User: u, Tenant: t}, "create", "saved_search", "", "Saved search saved", map[string]string{"name": r.FormValue("name")})
	a.redirectAfterAction(w, r, "/saved-searches", "success", "Saved search saved")
}

func (a *App) DeleteSavedSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	u, t, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if err := a.Store.DeleteSavedSearch(r.Context(), t.ID, u.ID, r.FormValue("id")); err != nil {
		a.serverError(w, r, "unable to delete saved search", err)
		return
	}
	a.auditAction(r.Context(), actionContext{User: u, Tenant: t}, "delete", "saved_search", r.FormValue("id"), "Saved search deleted", nil)
	a.redirectAfterAction(w, r, "/saved-searches", "success", "Saved search deleted")
}

func parseSelectedIDs(raw string) []string {
	parts := strings.Split(raw, ",")
	out := []string{}
	seen := map[string]bool{}
	for _, part := range parts {
		id := strings.TrimSpace(part)
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func (a *App) BulkTenders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	u, t, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if !canEditWorkspace(m.Role) {
		http.Error(w, "forbidden", 403)
		return
	}
	for _, id := range parseSelectedIDs(r.FormValue("selected_ids")) {
		switch r.FormValue("action") {
		case "bookmark":
			if err := a.Store.UpsertBookmark(r.Context(), models.Bookmark{TenantID: t.ID, UserID: u.ID, TenderID: id, Note: r.FormValue("notes")}); err != nil {
				a.serverError(w, r, "unable to apply bulk bookmark", err)
				return
			}
		case "queue":
			tender, err := a.Store.GetTender(r.Context(), id)
			if err == nil && tender.DocumentURL != "" {
				tender.DocumentStatus = models.ExtractionQueued
				if err := a.Store.UpsertTender(r.Context(), tender); err != nil {
					a.serverError(w, r, "unable to queue tender document", err)
					return
				}
				if err := a.Store.QueueJob(r.Context(), models.ExtractionJob{TenderID: tender.ID, DocumentURL: tender.DocumentURL, State: models.ExtractionQueued}); err != nil {
					a.serverError(w, r, "unable to queue extraction job", err)
					return
				}
			}
		default:
			workflow := models.Workflow{TenantID: t.ID, TenderID: id, Status: r.FormValue("status"), Priority: r.FormValue("priority"), AssignedUser: r.FormValue("assigned_user"), Notes: r.FormValue("notes")}
			if err := a.Store.UpsertWorkflow(r.Context(), workflow); err != nil {
				a.serverError(w, r, "unable to update workflow", err)
				return
			}
			a.addWorkflowSnapshot(r.Context(), actionContext{User: u, Tenant: t, Member: m}, workflow)
		}
	}
	a.redirectAfterAction(w, r, "/tenders", "success", "Bulk action applied")
}

func (a *App) TenderDetail(w http.ResponseWriter, r *http.Request) {
	u, t, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/tenders/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	item, err := a.Store.GetTender(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	workflow, _ := a.Store.GetWorkflow(r.Context(), t.ID, id)
	history, _ := a.Store.ListWorkflowEvents(r.Context(), t.ID, id)
	bookmarks, _ := a.Store.ListBookmarks(r.Context(), t.ID, u.ID)
	var bookmark models.Bookmark
	for _, itemBookmark := range bookmarks {
		if itemBookmark.TenderID == id {
			bookmark = itemBookmark
			break
		}
	}
	a.render(w, r, "tender_detail.html", map[string]any{
		"Title":           "Opportunity detail",
		"User":            u,
		"Tenant":          t,
		"Item":            item,
		"Bookmark":        bookmark,
		"Workflow":        workflow,
		"WorkflowHistory": history,
		"FactSections":    factSectionsForTender(item),
	})
}

func (a *App) AuditLogPage(w http.ResponseWriter, r *http.Request) {
	u, t, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if !canManageAudit(m.Role) {
		http.Error(w, "forbidden", 403)
		return
	}
	items, err := a.Store.ListAuditEntries(r.Context(), t.ID)
	if err != nil {
		a.serverError(w, r, "unable to load audit entries", err)
		return
	}
	pageSize := 20
	currentPage := atoi(r.URL.Query().Get("page"), 1)
	if currentPage < 1 {
		currentPage = 1
	}
	total := len(items)
	totalPages := total / pageSize
	if total%pageSize != 0 {
		totalPages++
	}
	if totalPages == 0 {
		totalPages = 1
	}
	if currentPage > totalPages {
		currentPage = totalPages
	}
	start := (currentPage - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	pageItems := items[start:end]
	query := r.URL.Query()
	a.render(w, r, "audit_log.html", map[string]any{
		"Title":       "Audit log",
		"User":        u,
		"Tenant":      t,
		"Items":       pageItems,
		"EntryCount":  total,
		"CurrentPage": currentPage,
		"TotalPages":  totalPages,
		"HasPrevPage": currentPage > 1,
		"HasNextPage": currentPage < totalPages,
		"PrevPageURL": queryPageLink("/audit-log", query, "page", currentPage-1),
		"NextPageURL": queryPageLink("/audit-log", query, "page", currentPage+1),
	})
}

func (a *App) QueuePage(w http.ResponseWriter, r *http.Request) {
	u, t, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	jobs := a.listRenderableJobs(r.Context())
	items := make([]QueueItem, 0, len(jobs))
	for _, job := range jobs {
		tender, _ := a.Store.GetTender(r.Context(), job.TenderID)
		items = append(items, QueueItem{Job: job, Tender: tender})
	}
	grouped := map[models.ExtractionState][]QueueItem{
		models.ExtractionFailed:     {},
		models.ExtractionProcessing: {},
		models.ExtractionRetry:      {},
		models.ExtractionQueued:     {},
		models.ExtractionCompleted:  {},
	}
	for _, item := range items {
		grouped[item.Job.State] = append(grouped[item.Job.State], item)
	}
	query := r.URL.Query()
	sections := []QueueSection{
		buildQueueSection("failed", "Failed", "danger", grouped[models.ExtractionFailed], query, true),
		buildQueueSection("processing", "Processing", "warning", grouped[models.ExtractionProcessing], query, false),
		buildQueueSection("retry", "Retry", "warning", grouped[models.ExtractionRetry], query, false),
		buildQueueSection("queued", "Queued", "info", grouped[models.ExtractionQueued], query, false),
		buildQueueSection("completed", "Completed", "success", grouped[models.ExtractionCompleted], query, false),
	}
	a.render(w, r, "queue.html", map[string]any{
		"Title":         "Queue",
		"User":          u,
		"Tenant":        t,
		"QueueItems":    items,
		"QueueSummary":  queueSummary(jobs),
		"QueueSections": sections,
	})
}

func (a *App) QueueRequeue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	_, _, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if !canEditWorkspace(m.Role) {
		http.Error(w, "forbidden", 403)
		return
	}
	tender, err := a.Store.GetTender(r.Context(), r.FormValue("tender_id"))
	if err != nil {
		a.notFound(w, r, "tender not found", err)
		return
	}
	if tender.DocumentURL == "" {
		http.Error(w, "no document url", 400)
		return
	}
	tender.DocumentStatus = models.ExtractionQueued
	if err := a.Store.UpsertTender(r.Context(), tender); err != nil {
		a.serverError(w, r, "unable to update tender status", err)
		return
	}
	if err := a.Store.QueueJob(r.Context(), models.ExtractionJob{TenderID: tender.ID, DocumentURL: tender.DocumentURL, State: models.ExtractionQueued}); err != nil {
		a.serverError(w, r, "unable to requeue extraction", err)
		return
	}
	a.redirectAfterAction(w, r, "/queue", "success", "Job requeued")
}

func (a *App) ResetWorkflow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	_, t, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if !canEditWorkspace(m.Role) {
		http.Error(w, "forbidden", 403)
		return
	}
	workflow := models.Workflow{
		TenantID: t.ID, TenderID: r.FormValue("tender_id"),
		Status: "", Priority: "", AssignedUser: "", Notes: "",
	}
	if err := a.Store.UpsertWorkflow(r.Context(), workflow); err != nil {
		a.serverError(w, r, "unable to reset workflow", err)
		return
	}
	a.addWorkflowSnapshot(r.Context(), actionContext{Tenant: t, Member: m}, workflow)
	a.redirectAfterAction(w, r, "/tenders", "success", "Workflow reset")
}

func (a *App) RemoveBookmark(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	u, t, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if err := a.Store.DeleteBookmark(r.Context(), t.ID, u.ID, r.FormValue("tender_id")); err != nil {
		a.serverError(w, r, "unable to remove bookmark", err)
		return
	}
	a.redirectAfterAction(w, r, "/tenders", "success", "Bookmark removed")
}
