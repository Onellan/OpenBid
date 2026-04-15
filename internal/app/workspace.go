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
	"sync"
	"time"

	"openbid/internal/models"
	"openbid/internal/store"
	"openbid/internal/tenderstate"
)

type QueueItem struct {
	Job           models.ExtractionJob
	Tender        models.Tender
	Title         string
	DetailURL     string
	IsMaintenance bool
}

type QueueSection struct {
	Key         string
	Title       string
	Tone        string
	Items       []QueueItem
	PageItems   []QueueItem
	Total       int
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
	Skipped    int
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

type AuditDisplayEntry struct {
	Entry       models.AuditEntry
	DetailLines []string
}

type tenderFilterViewOptions struct {
	Sources        []store.NamedValue
	Provinces      []string
	Statuses       []string
	Categories     []string
	Issuers        []string
	CIDBGradings   []string
	WorkflowStatus []string
	DocumentStatus []string
	GroupTags      []string
}

func queueSummaryFromCounts(counts store.JobStateCounts) QueueSummary {
	return QueueSummary{
		Queued:     counts.Queued + counts.Retry,
		Processing: counts.Processing,
		Failed:     counts.Failed,
		Completed:  counts.Completed,
		Skipped:    counts.Skipped,
	}
}

func jobCountForState(counts store.JobStateCounts, state models.ExtractionState) int {
	switch state {
	case models.ExtractionQueued:
		return counts.Queued
	case models.ExtractionProcessing:
		return counts.Processing
	case models.ExtractionRetry:
		return counts.Retry
	case models.ExtractionFailed:
		return counts.Failed
	case models.ExtractionCompleted:
		return counts.Completed
	case models.ExtractionSkipped:
		return counts.Skipped
	default:
		return 0
	}
}

func skipExpiredExtraction(tender *models.Tender, now time.Time) bool {
	if !tenderstate.IsExpired(*tender, now) {
		return false
	}
	tenderstate.MarkExtractionSkipped(tender, now)
	return true
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

func buildQueueSectionPage(key, title, tone string, items []QueueItem, total int, query url.Values, open bool) QueueSection {
	const pageSize = 10
	param := key + "_page"
	currentPage, err := strconv.Atoi(query.Get(param))
	if err != nil || currentPage < 1 {
		currentPage = 1
	}
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
	section := QueueSection{
		Key:         key,
		Title:       title,
		Tone:        tone,
		Items:       items,
		PageItems:   items,
		Total:       total,
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

func queueItemsForJobs(jobs []models.ExtractionJob, tenderByID map[string]models.Tender) []QueueItem {
	items := make([]QueueItem, 0, len(jobs))
	for _, job := range jobs {
		tender := tenderByID[job.TenderID]
		item := QueueItem{Job: job, Tender: tender, Title: tender.Title, DetailURL: "/tenders/" + tender.ID}
		if job.JobType == models.JobTypeExpiredTenderCleanup {
			item.Title = models.ExpiredTenderCleanupJobName
			if strings.TrimSpace(job.JobName) != "" {
				item.Title = job.JobName
			}
			item.DetailURL = "/queue#expired-tender-cleanup"
			item.IsMaintenance = true
		}
		items = append(items, item)
	}
	return items
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

func ensureCurrentStringOption(options []string, current string) []string {
	current = strings.TrimSpace(current)
	if current == "" {
		return options
	}
	for _, option := range options {
		if strings.EqualFold(option, current) {
			return options
		}
	}
	return append([]string{current}, options...)
}

func ensureCurrentNamedOption(options []store.NamedValue, current string) []store.NamedValue {
	current = strings.TrimSpace(current)
	if current == "" {
		return options
	}
	for _, option := range options {
		if option.Value == current {
			return options
		}
	}
	return append([]store.NamedValue{{Value: current, Label: current}}, options...)
}

func tenderFilterOptionsForView(base store.TenderFilterOptions, filter store.ListFilter) tenderFilterViewOptions {
	return tenderFilterViewOptions{
		Sources:        ensureCurrentNamedOption(base.Sources, filter.Source),
		Provinces:      ensureCurrentStringOption(base.Provinces, filter.Province),
		Statuses:       ensureCurrentStringOption(base.Statuses, filter.Status),
		Categories:     ensureCurrentStringOption(base.Categories, filter.Category),
		Issuers:        ensureCurrentStringOption(base.Issuers, filter.Issuer),
		CIDBGradings:   ensureCurrentStringOption(base.CIDBGradings, filter.CIDB),
		WorkflowStatus: ensureCurrentStringOption(base.WorkflowStatus, filter.WorkflowStatus),
		DocumentStatus: ensureCurrentStringOption(base.DocumentStatus, filter.DocumentStatus),
		GroupTags:      ensureCurrentStringOption(base.GroupTags, filter.GroupTag),
	}
}

func tenderFilterFromRequest(r *http.Request, tenantID, userID string, page, pageSize int) store.ListFilter {
	return store.NormalizeFilter(store.ListFilter{
		Query: r.URL.Query().Get("q"), Source: r.URL.Query().Get("source"), Province: r.URL.Query().Get("province"),
		Category: r.URL.Query().Get("category"), Issuer: r.URL.Query().Get("issuer"), Status: r.URL.Query().Get("status"),
		CIDB: r.URL.Query().Get("cidb"), WorkflowStatus: r.URL.Query().Get("workflow_status"),
		DocumentStatus: r.URL.Query().Get("document_status"), GroupTag: r.URL.Query().Get("group_tag"), BookmarkedOnly: r.URL.Query().Get("bookmarked_only") == "1",
		HasDocuments: r.URL.Query().Get("has_documents") == "1", Sort: r.URL.Query().Get("sort"), View: r.URL.Query().Get("view"),
		Page: page, PageSize: pageSize, TenantID: tenantID, UserID: userID,
	})
}

func (a *App) bookmarkedTenders(ctx context.Context, tenantID, userID string) ([]BookmarkedTender, error) {
	bookmarks, err := a.Store.ListBookmarks(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}
	tenderIDs := make([]string, 0, len(bookmarks))
	for _, bookmark := range bookmarks {
		tenderIDs = append(tenderIDs, bookmark.TenderID)
	}
	workflowByTender, _ := a.Store.GetWorkflowsByTenderIDs(ctx, tenantID, tenderIDs)
	tenderByID, err := a.Store.GetTendersByIDs(ctx, tenderIDs)
	if err != nil {
		return nil, err
	}
	items := make([]BookmarkedTender, 0, len(bookmarks))
	for _, bookmark := range bookmarks {
		tender, ok := tenderByID[bookmark.TenderID]
		if !ok {
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
	var (
		d                 models.Dashboard
		bookmarkCount     int
		savedCount        int
		keywordSummary    models.KeywordSearchSummary
		jobCounts         store.JobStateCounts
		sourceHealthCount int
		wg                sync.WaitGroup
	)
	wg.Add(6)
	go func() {
		defer wg.Done()
		d, _ = a.Store.Dashboard(r.Context(), t.ID, a.Config.LowMemoryMode, false)
	}()
	go func() {
		defer wg.Done()
		bookmarkCount, _ = a.Store.CountBookmarks(r.Context(), t.ID, u.ID)
	}()
	go func() {
		defer wg.Done()
		savedCount, _ = a.Store.CountSavedSearches(r.Context(), t.ID, u.ID)
	}()
	go func() {
		defer wg.Done()
		keywordSummary, _ = a.Store.KeywordSearchSummary(r.Context(), t.ID, u.ID)
	}()
	go func() {
		defer wg.Done()
		jobCounts, _ = a.Store.JobStateCounts(r.Context())
	}()
	go func() {
		defer wg.Done()
		sourceHealth, _ := a.Store.ListSourceHealth(r.Context())
		sourceHealthCount = len(sourceHealth)
	}()
	wg.Wait()
	a.render(w, r, "home.html", map[string]any{
		"Title":             "Home",
		"User":              u,
		"Tenant":            t,
		"Dashboard":         d,
		"BookmarkCount":     bookmarkCount,
		"SavedCount":        savedCount,
		"KeywordSummary":    keywordSummary,
		"QueueSummary":      queueSummaryFromCounts(jobCounts),
		"SourceHealthCount": sourceHealthCount,
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
	pageSize := atoi(r.URL.Query().Get("page_size"), 10)
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 10
	}
	f := tenderFilterFromRequest(r, t.ID, u.ID, atoi(r.URL.Query().Get("page"), 1), pageSize)
	filterOptions, _ := a.Store.TenderFilterOptions(r.Context(), t.ID)
	items, total, _ := a.Store.ListTenders(r.Context(), f)
	tenderIDs := make([]string, 0, len(items))
	for _, item := range items {
		tenderIDs = append(tenderIDs, item.ID)
	}
	bookmarkByTender, _ := a.Store.GetBookmarksByTenderIDs(r.Context(), t.ID, u.ID, tenderIDs)
	bookmarked := map[string]bool{}
	for tenderID := range bookmarkByTender {
		bookmarked[tenderID] = true
	}
	workflowByTender, _ := a.Store.GetWorkflowsByTenderIDs(r.Context(), t.ID, tenderIDs)
	params := map[string]string{
		"q": f.Query, "source": f.Source, "province": f.Province, "category": f.Category, "issuer": f.Issuer, "status": f.Status,
		"cidb": f.CIDB, "workflow_status": f.WorkflowStatus, "document_status": f.DocumentStatus, "group_tag": f.GroupTag, "sort": f.Sort, "view": f.View,
		"page_size": strconv.Itoa(f.PageSize),
	}
	if f.BookmarkedOnly {
		params["bookmarked_only"] = "1"
	}
	if f.HasDocuments {
		params["has_documents"] = "1"
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
		"FilterOptions": tenderFilterOptionsForView(filterOptions, f),
		"ReturnTo":      r.URL.RequestURI(),
		"CurrentPage":   f.Page, "TotalPages": totalPages, "HasPrevPage": f.Page > 1, "HasNextPage": f.Page < totalPages,
		"PrevPageURL": pageLink("/tenders", params, f.Page-1), "NextPageURL": pageLink("/tenders", params, f.Page+1),
	})
}

func (a *App) ExportCSV(w http.ResponseWriter, r *http.Request) {
	u, t, _, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
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
	exportFilter := tenderFilterFromRequest(r, t.ID, u.ID, 1, 250)
	for page := 1; ; page++ {
		exportFilter.Page = page
		items, _, err := a.Store.ListTenders(r.Context(), exportFilter)
		if err != nil {
			a.serverError(w, r, "unable to export tenders", err)
			return
		}
		if len(items) == 0 {
			break
		}
		tenderIDs := make([]string, 0, len(items))
		for _, tender := range items {
			tenderIDs = append(tenderIDs, tender.ID)
		}
		workflowByTender, err := a.Store.GetWorkflowsByTenderIDs(r.Context(), t.ID, tenderIDs)
		if err != nil {
			a.serverError(w, r, "unable to load export workflows", err)
			return
		}
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
		if err := cw.Error(); err != nil {
			return
		}
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
	u, t, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if !canEditWorkspace(u, m) {
		http.Error(w, "forbidden", 403)
		return
	}
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
	u, t, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if !canQueueWork(u, m) {
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
	if skipExpiredExtraction(&tender, time.Now().UTC()) {
		if err := a.Store.UpsertTender(r.Context(), tender); err != nil {
			a.serverError(w, r, "unable to update tender status", err)
			return
		}
		a.auditAction(r.Context(), actionContext{User: u, Tenant: t, Member: m}, "skip", "queue_job", tender.ID, "Extraction skipped because tender is expired", map[string]string{"reason": tender.ExtractionSkippedReason})
		a.redirectAfterAction(w, r, "/tenders", "error", "Extraction not queued because the tender is expired")
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
	if !canEditWorkspace(u, m) {
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
				if skipExpiredExtraction(&tender, time.Now().UTC()) {
					if err := a.Store.UpsertTender(r.Context(), tender); err != nil {
						a.serverError(w, r, "unable to mark expired tender skipped", err)
						return
					}
					continue
				}
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
	var bookmark models.Bookmark
	if bookmarks, _ := a.Store.GetBookmarksByTenderIDs(r.Context(), t.ID, u.ID, []string{id}); len(bookmarks) > 0 {
		bookmark = bookmarks[id]
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
	a.renderAuditLogPage(w, r, false)
}

func (a *App) SecurityAuditLogPage(w http.ResponseWriter, r *http.Request) {
	a.renderAuditLogPage(w, r, true)
}

func (a *App) renderAuditLogPage(w http.ResponseWriter, r *http.Request, securityFocus bool) {
	u, t, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if !canManageAudit(u, m) {
		http.Error(w, "forbidden", 403)
		return
	}
	pageSize := 20
	currentPage := atoi(r.URL.Query().Get("page"), 1)
	var (
		items []models.AuditEntry
		total int
		err   error
	)
	if securityFocus {
		items, total, err = a.Store.ListSecurityAuditEntriesPage(r.Context(), t.ID, currentPage, pageSize)
	} else {
		items, total, err = a.Store.ListAuditEntriesPage(r.Context(), t.ID, currentPage, pageSize)
	}
	if err != nil {
		a.serverError(w, r, "unable to load audit entries", err)
		return
	}
	if currentPage < 1 {
		currentPage = 1
	}
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
	query := r.URL.Query()
	basePath := "/audit-log"
	title := "Audit log"
	copyText := fmt.Sprintf("Track administrative and workflow actions for the %s workspace.", t.Name)
	focusTitle := "Recent actions"
	focusCopy := "Latest audited actions for this tenant, collapsed only when you want more room on the page."
	if securityFocus {
		basePath = "/audit-log/security"
		title = "Security audit"
		copyText = fmt.Sprintf("Focus incident triage on auth-sensitive and tenant administration activity for the %s workspace.", t.Name)
		focusTitle = "Security-sensitive events"
		focusCopy = "Lockouts, throttling, MFA changes, password resets, and tenant or user administration actions for this tenant."
	}
	a.render(w, r, "audit_log.html", map[string]any{
		"Title":               title,
		"User":                u,
		"Tenant":              t,
		"Items":               auditDisplayEntries(items),
		"EntryCount":          total,
		"CurrentPage":         currentPage,
		"TotalPages":          totalPages,
		"HasPrevPage":         currentPage > 1,
		"HasNextPage":         currentPage < totalPages,
		"PrevPageURL":         queryPageLink(basePath, query, "page", currentPage-1),
		"NextPageURL":         queryPageLink(basePath, query, "page", currentPage+1),
		"PageTitle":           title,
		"PageCopy":            copyText,
		"FocusTitle":          focusTitle,
		"FocusCopy":           focusCopy,
		"SecurityFocus":       securityFocus,
		"AuditLogURL":         "/audit-log",
		"SecurityAuditLogURL": "/audit-log/security",
	})
}

func auditDisplayEntries(items []models.AuditEntry) []AuditDisplayEntry {
	rows := make([]AuditDisplayEntry, 0, len(items))
	for _, item := range items {
		rows = append(rows, AuditDisplayEntry{
			Entry:       item,
			DetailLines: formatAuditMetadata(item.Metadata),
		})
	}
	return rows
}

func (a *App) QueuePage(w http.ResponseWriter, r *http.Request) {
	u, t, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	query := r.URL.Query()
	counts, _ := a.Store.JobStateCounts(r.Context())
	type queueStateView struct {
		Key   string
		Title string
		Tone  string
		State models.ExtractionState
		Open  bool
	}
	stateViews := []queueStateView{
		{Key: "failed", Title: "Failed", Tone: "danger", State: models.ExtractionFailed, Open: true},
		{Key: "processing", Title: "Processing", Tone: "warning", State: models.ExtractionProcessing},
		{Key: "retry", Title: "Retry", Tone: "warning", State: models.ExtractionRetry},
		{Key: "queued", Title: "Queued", Tone: "info", State: models.ExtractionQueued},
		{Key: "skipped", Title: "Skipped", Tone: "warning", State: models.ExtractionSkipped},
		{Key: "completed", Title: "Completed", Tone: "success", State: models.ExtractionCompleted},
	}
	sections := make([]QueueSection, len(stateViews))
	sectionItems := make([][]QueueItem, len(stateViews))
	var wg sync.WaitGroup
	for i, view := range stateViews {
		i, view := i, view
		wg.Add(1)
		go func() {
			defer wg.Done()
			page := atoi(query.Get(view.Key+"_page"), 1)
			jobs, _ := a.Store.ListValidJobsByState(r.Context(), view.State, page, 10)
			tenderIDs := make([]string, 0, len(jobs))
			for _, job := range jobs {
				tenderIDs = append(tenderIDs, job.TenderID)
			}
			tenderByID, _ := a.Store.GetTendersByIDs(r.Context(), tenderIDs)
			items := queueItemsForJobs(jobs, tenderByID)
			sectionItems[i] = items
			sections[i] = buildQueueSectionPage(view.Key, view.Title, view.Tone, items, jobCountForState(counts, view.State), query, view.Open)
		}()
	}
	wg.Wait()
	allPageItems := []QueueItem{}
	for _, items := range sectionItems {
		allPageItems = append(allPageItems, items...)
	}
	a.render(w, r, "queue.html", map[string]any{
		"Title":         "Queue",
		"User":          u,
		"Tenant":        t,
		"CanEditQueue":  canQueueWork(u, m),
		"CanRunCleanup": canQueueWork(u, m),
		"QueueItems":    allPageItems,
		"QueueSummary":  queueSummaryFromCounts(counts),
		"QueueSections": sections,
	})
}

func (a *App) CleanupExpiredTenders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	u, t, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !canQueueWork(u, m) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := a.Store.QueueJob(r.Context(), models.ExtractionJob{
		ID:       models.ExpiredTenderCleanupJobID,
		JobType:  models.JobTypeExpiredTenderCleanup,
		JobName:  models.ExpiredTenderCleanupJobName,
		TenantID: t.ID,
		UserID:   u.ID,
		State:    models.ExtractionQueued,
	}); err != nil {
		a.serverError(w, r, "unable to queue expired tender cleanup", err)
		return
	}
	a.auditAction(r.Context(), actionContext{User: u, Tenant: t, Member: m}, "queue", "expired_tender_cleanup", models.ExpiredTenderCleanupJobID, "Expired tender cleanup queued", nil)
	message := "Expired tender cleanup queued. Track it in the queue below."
	a.redirectAfterAction(w, r, "/queue#expired-tender-cleanup", "success", message)
}

func (a *App) QueueRequeue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.ensureCSRF(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	u, _, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if !canQueueWork(u, m) {
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
	if skipExpiredExtraction(&tender, time.Now().UTC()) {
		if err := a.Store.UpsertTender(r.Context(), tender); err != nil {
			a.serverError(w, r, "unable to update tender status", err)
			return
		}
		a.redirectAfterAction(w, r, "/queue", "error", "Extraction not requeued because the tender is expired")
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
	u, t, m, ok := a.currentUserTenant(r)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if !canEditWorkspace(u, m) {
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
