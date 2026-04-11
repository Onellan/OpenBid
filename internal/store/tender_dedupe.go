package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"openbid/internal/models"
	"sort"
	"strings"
	"time"
)

type sqlExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func sqliteUpsertJSONExec[T any](ctx context.Context, exec sqlExecer, table, id string, v T) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = exec.ExecContext(ctx, "insert into "+table+"(id,payload) values(?,?) on conflict(id) do update set payload=excluded.payload", id, string(b))
	return err
}

func (s *SQLiteStore) DeduplicateTenders(ctx context.Context) (int, error) {
	tenders, err := s.listAllTenders(ctx)
	if err != nil {
		return 0, err
	}
	groups := map[string][]models.Tender{}
	for _, tender := range tenders {
		key := tenderDedupKey(tender)
		if key == "" {
			continue
		}
		groups[key] = append(groups[key], tender)
	}

	alias := map[string]string{}
	mergedCanonicals := map[string]models.Tender{}
	impactedIDs := map[string]bool{}
	removed := 0
	for _, group := range groups {
		if len(group) <= 1 {
			continue
		}
		sort.Slice(group, func(i, j int) bool {
			return tenderDedupScore(group[i]) > tenderDedupScore(group[j])
		})
		canonical := group[0]
		for _, item := range group[1:] {
			canonical = mergeTenderRecords(canonical, item)
			if item.ID != canonical.ID {
				alias[item.ID] = canonical.ID
				impactedIDs[item.ID] = true
				removed++
			}
		}
		impactedIDs[canonical.ID] = true
		mergedCanonicals[canonical.ID] = canonical
	}

	workflows, err := s.ListWorkflows(ctx, "")
	if err != nil {
		return 0, err
	}
	bookmarks, err := s.listAllBookmarks(ctx)
	if err != nil {
		return 0, err
	}
	jobs, err := s.ListJobs(ctx)
	if err != nil {
		return 0, err
	}
	workflowEvents, err := sqliteListJSON[models.WorkflowEvent](ctx, s.db, "workflow_events")
	if err != nil {
		return 0, err
	}
	auditEntries, err := sqliteListJSON[models.AuditEntry](ctx, s.db, "audit_entries")
	if err != nil {
		return 0, err
	}

	documentURLTenderIDs := map[string]map[string]bool{}
	documentURLToTenderID := map[string]string{}
	for _, tender := range tenders {
		id := tender.ID
		if canonical, ok := alias[id]; ok {
			id = canonical
		}
		url := strings.TrimSpace(tender.DocumentURL)
		if url == "" {
			continue
		}
		if documentURLTenderIDs[url] == nil {
			documentURLTenderIDs[url] = map[string]bool{}
		}
		documentURLTenderIDs[url][id] = true
		documentURLToTenderID[url] = id
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	for id, tender := range mergedCanonicals {
		if err = sqliteUpsertJSONExec(ctx, tx, "tenders", id, tender); err != nil {
			return 0, err
		}
	}

	if len(impactedIDs) > 0 {
		mergedWorkflows := map[string]models.Workflow{}
		for _, workflow := range workflows {
			if !impactedIDs[workflow.TenderID] {
				continue
			}
			if canonical, ok := alias[workflow.TenderID]; ok {
				workflow.TenderID = canonical
			}
			key := workflow.TenantID + "|" + workflow.TenderID
			mergedWorkflows[key] = mergeWorkflowRecord(mergedWorkflows[key], workflow)
		}
		if err = deleteByTenderIDs(ctx, tx, "workflow_records", impactedIDs); err != nil {
			return 0, err
		}
		for _, workflow := range mergedWorkflows {
			if workflow.ID == "" {
				workflow.ID = newid()
			}
			if workflow.UpdatedAt.IsZero() {
				workflow.UpdatedAt = time.Now().UTC()
			}
			if _, err = tx.ExecContext(ctx, `
				insert into workflow_records(id, tenant_id, tender_id, status, priority, assigned_user, notes, updated_at)
				values(?,?,?,?,?,?,?,?)
				on conflict(tenant_id, tender_id) do update set
					status=excluded.status,
					priority=excluded.priority,
					assigned_user=excluded.assigned_user,
					notes=excluded.notes,
					updated_at=excluded.updated_at
			`, workflow.ID, workflow.TenantID, workflow.TenderID, workflow.Status, workflow.Priority, workflow.AssignedUser, workflow.Notes, sqliteTimeString(workflow.UpdatedAt)); err != nil {
				return 0, err
			}
		}

		mergedBookmarks := map[string]models.Bookmark{}
		for _, bookmark := range bookmarks {
			if !impactedIDs[bookmark.TenderID] {
				continue
			}
			if canonical, ok := alias[bookmark.TenderID]; ok {
				bookmark.TenderID = canonical
			}
			key := bookmark.TenantID + "|" + bookmark.UserID + "|" + bookmark.TenderID
			mergedBookmarks[key] = mergeBookmarkRecord(mergedBookmarks[key], bookmark)
		}
		if err = deleteByTenderIDs(ctx, tx, "bookmark_records", impactedIDs); err != nil {
			return 0, err
		}
		for _, bookmark := range mergedBookmarks {
			if bookmark.ID == "" {
				bookmark.ID = newid()
			}
			if bookmark.CreatedAt.IsZero() {
				bookmark.CreatedAt = time.Now().UTC()
			}
			if bookmark.UpdatedAt.IsZero() {
				bookmark.UpdatedAt = bookmark.CreatedAt
			}
			if _, err = tx.ExecContext(ctx, `
				insert into bookmark_records(id, tenant_id, user_id, tender_id, note, created_at, updated_at)
				values(?,?,?,?,?,?,?)
				on conflict(tenant_id, user_id, tender_id) do update set
					note=excluded.note,
					updated_at=excluded.updated_at
			`, bookmark.ID, bookmark.TenantID, bookmark.UserID, bookmark.TenderID, bookmark.Note, sqliteTimeString(bookmark.CreatedAt), sqliteTimeString(bookmark.UpdatedAt)); err != nil {
				return 0, err
			}
		}
	}

	mergedJobs := map[string]models.ExtractionJob{}
	impactedJobIDs := map[string]bool{}
	for _, job := range jobs {
		updated := false
		if canonical, ok := alias[job.TenderID]; ok {
			job.TenderID = canonical
			updated = true
		}
		if strings.TrimSpace(job.TenderID) == "" {
			url := strings.TrimSpace(job.DocumentURL)
			if url != "" && len(documentURLTenderIDs[url]) == 1 {
				job.TenderID = documentURLToTenderID[url]
				updated = true
			}
		}
		if !updated {
			continue
		}
		impactedJobIDs[job.ID] = true
		key := job.TenderID + "|" + strings.TrimSpace(job.DocumentURL) + "|" + string(job.State)
		mergedJobs[key] = mergeJobRecord(mergedJobs[key], job)
	}
	for id := range impactedJobIDs {
		if err = sqliteDeleteTx(ctx, tx, "jobs", id); err != nil {
			return 0, err
		}
	}
	for _, job := range mergedJobs {
		if job.ID == "" {
			job.ID = newid()
		}
		if job.CreatedAt.IsZero() {
			job.CreatedAt = time.Now().UTC()
		}
		if job.UpdatedAt.IsZero() {
			job.UpdatedAt = job.CreatedAt
		}
		if err = sqliteUpsertJSONExec(ctx, tx, "jobs", job.ID, job); err != nil {
			return 0, err
		}
	}

	for _, event := range workflowEvents {
		if canonical, ok := alias[event.TenderID]; ok {
			event.TenderID = canonical
			if err = sqliteUpsertJSONExec(ctx, tx, "workflow_events", event.ID, event); err != nil {
				return 0, err
			}
		}
	}
	for _, entry := range auditEntries {
		if strings.EqualFold(entry.Entity, "tender") {
			if canonical, ok := alias[entry.EntityID]; ok {
				entry.EntityID = canonical
				if err = sqliteUpsertJSONExec(ctx, tx, "audit_entries", entry.ID, entry); err != nil {
					return 0, err
				}
			}
		}
	}

	for duplicateID := range alias {
		if err = sqliteDeleteTx(ctx, tx, "tenders", duplicateID); err != nil {
			return 0, err
		}
	}

	if err = tx.Commit(); err != nil {
		return 0, err
	}
	if removed > 0 {
		_ = s.refreshAllKeywordProfiles(ctx)
	}
	return removed, nil
}

func sqliteDeleteTx(ctx context.Context, tx *sql.Tx, table, id string) error {
	_, err := tx.ExecContext(ctx, "delete from "+table+" where id = ?", id)
	return err
}

func deleteByTenderIDs(ctx context.Context, tx *sql.Tx, table string, ids map[string]bool) error {
	for id := range ids {
		if _, err := tx.ExecContext(ctx, "delete from "+table+" where tender_id = ?", id); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) listAllBookmarks(ctx context.Context) ([]models.Bookmark, error) {
	rows, err := s.db.QueryContext(ctx, `
		select id, tenant_id, user_id, tender_id, note, created_at, updated_at
		from bookmark_records
		order by updated_at desc, id asc
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Bookmark{}
	for rows.Next() {
		item, err := scanBookmark(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func tenderDedupKey(t models.Tender) string {
	sourceKey := strings.TrimSpace(strings.ToLower(t.SourceKey))
	if sourceKey == "" {
		return ""
	}
	if externalID := strings.TrimSpace(strings.ToLower(t.ExternalID)); externalID != "" {
		return "source-external|" + sourceKey + "|" + externalID
	}
	if documentURL := strings.TrimSpace(strings.ToLower(t.DocumentURL)); documentURL != "" {
		return "source-document|" + sourceKey + "|" + documentURL
	}
	if originalURL := strings.TrimSpace(strings.ToLower(t.OriginalURL)); originalURL != "" && strings.TrimSpace(strings.ToLower(t.TenderNumber)) != "" {
		return "source-listing|" + sourceKey + "|" + originalURL + "|" + strings.TrimSpace(strings.ToLower(t.TenderNumber))
	}
	title := strings.TrimSpace(strings.ToLower(t.Title))
	issuer := strings.TrimSpace(strings.ToLower(t.Issuer))
	closingDate := strings.TrimSpace(strings.ToLower(t.ClosingDate))
	if title == "" {
		return ""
	}
	return "source-title|" + sourceKey + "|" + title + "|" + issuer + "|" + closingDate
}

func tenderDedupScore(t models.Tender) int {
	score := 0
	for _, value := range []string{t.ID, t.ExternalID, t.Title, t.Issuer, t.Province, t.Category, t.TenderNumber, t.PublishedDate, t.ClosingDate, t.Status, t.CIDBGrading, t.Summary, t.OriginalURL, t.DocumentURL, t.Scope, t.TenderType} {
		if strings.TrimSpace(value) != "" {
			score += 2
		}
	}
	if t.EngineeringRelevant {
		score++
	}
	score += len(t.Documents) * 3
	score += len(t.Contacts) * 2
	score += len(t.Briefings) * 2
	score += len(t.Requirements) * 2
	score += len(t.PageFacts) + len(t.DocumentFacts) + len(t.ExtractedFacts) + len(t.SourceMetadata)
	score += extractionStateRank(t.DocumentStatus)
	if !t.UpdatedAt.IsZero() {
		score++
	}
	return score
}

func mergeTenderRecords(current, incoming models.Tender) models.Tender {
	if tenderDedupScore(incoming) > tenderDedupScore(current) {
		current, incoming = incoming, current
	}
	if current.ExternalID == "" {
		current.ExternalID = incoming.ExternalID
	}
	if current.Title == "" {
		current.Title = incoming.Title
	}
	if current.Issuer == "" {
		current.Issuer = incoming.Issuer
	}
	if current.Province == "" {
		current.Province = incoming.Province
	}
	if current.Category == "" {
		current.Category = incoming.Category
	}
	if current.TenderNumber == "" {
		current.TenderNumber = incoming.TenderNumber
	}
	if current.PublishedDate == "" {
		current.PublishedDate = incoming.PublishedDate
	}
	if current.ClosingDate == "" {
		current.ClosingDate = incoming.ClosingDate
	}
	if current.Status == "" {
		current.Status = incoming.Status
	}
	if current.CIDBGrading == "" {
		current.CIDBGrading = incoming.CIDBGrading
	}
	if current.Summary == "" {
		current.Summary = incoming.Summary
	}
	if current.OriginalURL == "" {
		current.OriginalURL = incoming.OriginalURL
	}
	if current.DocumentURL == "" {
		current.DocumentURL = incoming.DocumentURL
	}
	if current.Scope == "" {
		current.Scope = incoming.Scope
	}
	if current.TenderType == "" {
		current.TenderType = incoming.TenderType
	}
	if !current.EngineeringRelevant && incoming.EngineeringRelevant {
		current.EngineeringRelevant = true
	}
	if incoming.RelevanceScore > current.RelevanceScore {
		current.RelevanceScore = incoming.RelevanceScore
	}
	if extractionStateRank(incoming.DocumentStatus) > extractionStateRank(current.DocumentStatus) {
		current.DocumentStatus = incoming.DocumentStatus
	}
	current.ExtractedFacts = mergeStringMap(current.ExtractedFacts, incoming.ExtractedFacts)
	current.PageFacts = mergeStringMap(current.PageFacts, incoming.PageFacts)
	current.DocumentFacts = mergeStringMap(current.DocumentFacts, incoming.DocumentFacts)
	current.SourceMetadata = mergeStringMap(current.SourceMetadata, incoming.SourceMetadata)
	current.Documents = mergeDocuments(current.Documents, incoming.Documents)
	current.Contacts = mergeContacts(current.Contacts, incoming.Contacts)
	current.Briefings = mergeBriefings(current.Briefings, incoming.Briefings)
	current.Requirements = mergeRequirements(current.Requirements, incoming.Requirements)
	if current.Location == (models.TenderLocation{}) {
		current.Location = incoming.Location
	}
	if current.Submission == (models.TenderSubmission{}) {
		current.Submission = incoming.Submission
	}
	if current.Evaluation == (models.TenderEvaluation{}) {
		current.Evaluation = incoming.Evaluation
	}
	if current.CreatedAt.IsZero() || (!incoming.CreatedAt.IsZero() && incoming.CreatedAt.Before(current.CreatedAt)) {
		current.CreatedAt = incoming.CreatedAt
	}
	if incoming.UpdatedAt.After(current.UpdatedAt) {
		current.UpdatedAt = incoming.UpdatedAt
	}
	return current
}

func mergeStringMap(current, incoming map[string]string) map[string]string {
	if len(current) == 0 && len(incoming) == 0 {
		return map[string]string{}
	}
	out := map[string]string{}
	for key, value := range incoming {
		if strings.TrimSpace(value) != "" {
			out[key] = value
		}
	}
	for key, value := range current {
		if strings.TrimSpace(value) != "" {
			out[key] = value
		}
	}
	return out
}

func mergeDocuments(current, incoming []models.TenderDocument) []models.TenderDocument {
	seen := map[string]bool{}
	out := []models.TenderDocument{}
	for _, doc := range append(current, incoming...) {
		key := strings.TrimSpace(doc.URL) + "|" + strings.TrimSpace(doc.FileName) + "|" + strings.TrimSpace(doc.Role)
		if key == "||" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, doc)
	}
	return out
}

func mergeContacts(current, incoming []models.TenderContact) []models.TenderContact {
	seen := map[string]bool{}
	out := []models.TenderContact{}
	for _, item := range append(current, incoming...) {
		key := strings.TrimSpace(item.Name) + "|" + strings.TrimSpace(item.Email) + "|" + strings.TrimSpace(item.Telephone)
		if key == "||" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
	}
	return out
}

func mergeBriefings(current, incoming []models.TenderBriefing) []models.TenderBriefing {
	seen := map[string]bool{}
	out := []models.TenderBriefing{}
	for _, item := range append(current, incoming...) {
		key := strings.TrimSpace(item.Label) + "|" + strings.TrimSpace(item.DateTime) + "|" + strings.TrimSpace(item.Venue)
		if key == "||" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
	}
	return out
}

func mergeRequirements(current, incoming []models.TenderRequirement) []models.TenderRequirement {
	seen := map[string]bool{}
	out := []models.TenderRequirement{}
	for _, item := range append(current, incoming...) {
		key := strings.TrimSpace(item.Category) + "|" + strings.TrimSpace(item.Description)
		if key == "|" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
	}
	return out
}

func mergeWorkflowRecord(current, incoming models.Workflow) models.Workflow {
	if current.ID == "" {
		return incoming
	}
	if strings.TrimSpace(current.Status) == "" {
		current.Status = incoming.Status
	}
	if strings.TrimSpace(current.Priority) == "" {
		current.Priority = incoming.Priority
	}
	if strings.TrimSpace(current.AssignedUser) == "" {
		current.AssignedUser = incoming.AssignedUser
	}
	if strings.TrimSpace(current.Notes) == "" {
		current.Notes = incoming.Notes
	}
	if incoming.UpdatedAt.After(current.UpdatedAt) {
		current.UpdatedAt = incoming.UpdatedAt
	}
	return current
}

func mergeBookmarkRecord(current, incoming models.Bookmark) models.Bookmark {
	if current.ID == "" {
		return incoming
	}
	if strings.TrimSpace(current.Note) == "" {
		current.Note = incoming.Note
	}
	if current.CreatedAt.IsZero() || (!incoming.CreatedAt.IsZero() && incoming.CreatedAt.Before(current.CreatedAt)) {
		current.CreatedAt = incoming.CreatedAt
	}
	if incoming.UpdatedAt.After(current.UpdatedAt) {
		current.UpdatedAt = incoming.UpdatedAt
	}
	return current
}

func mergeJobRecord(current, incoming models.ExtractionJob) models.ExtractionJob {
	if current.ID == "" {
		return incoming
	}
	if incoming.Attempts > current.Attempts {
		current.Attempts = incoming.Attempts
	}
	if strings.TrimSpace(current.LastError) == "" {
		current.LastError = incoming.LastError
	}
	if incoming.NextAttemptAt.After(current.NextAttemptAt) {
		current.NextAttemptAt = incoming.NextAttemptAt
	}
	if incoming.UpdatedAt.After(current.UpdatedAt) {
		current.UpdatedAt = incoming.UpdatedAt
	}
	if extractionStateRank(incoming.State) > extractionStateRank(current.State) {
		current.State = incoming.State
	}
	if current.TenderID == "" {
		current.TenderID = incoming.TenderID
	}
	return current
}

func extractionStateRank(state models.ExtractionState) int {
	switch state {
	case models.ExtractionCompleted:
		return 5
	case models.ExtractionProcessing:
		return 4
	case models.ExtractionRetry:
		return 3
	case models.ExtractionQueued:
		return 2
	case models.ExtractionFailed:
		return 1
	default:
		return 0
	}
}
