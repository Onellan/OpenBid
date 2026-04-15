package store

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"openbid/internal/models"
	"openbid/internal/smartkeywords"
)

func normalizeSmartGroup(group models.SmartKeywordGroup) models.SmartKeywordGroup {
	group.Name = strings.Join(strings.Fields(strings.TrimSpace(group.Name)), " ")
	group.TagName = strings.Join(strings.Fields(strings.TrimSpace(group.TagName)), " ")
	if group.TagName == "" {
		group.TagName = group.Name
	}
	if group.MatchMode != models.SmartMatchModeAll {
		group.MatchMode = models.SmartMatchModeAny
	}
	if group.MinMatchCount <= 0 {
		group.MinMatchCount = 1
	}
	group.ExcludeTerms = uniqueDisplayTerms(group.ExcludeTerms)
	return group
}

func normalizeSmartKeyword(keyword models.SmartKeyword) models.SmartKeyword {
	keyword.Value = smartkeywords.DisplayTerm(keyword.Value)
	keyword.NormalizedValue = smartkeywords.NormalizeTerm(keyword.Value)
	return keyword
}

func uniqueDisplayTerms(values []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		display := smartkeywords.DisplayTerm(value)
		key := smartkeywords.NormalizeTerm(display)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, display)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

func parseTermsCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return uniqueDisplayTerms(strings.Split(value, ","))
}

func encodeChannels(values []models.NotificationChannel) string {
	if len(values) == 0 {
		return "[]"
	}
	b, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func decodeChannels(value string) []models.NotificationChannel {
	var out []models.NotificationChannel
	if err := json.Unmarshal([]byte(strings.TrimSpace(value)), &out); err != nil {
		return nil
	}
	return out
}

func scanSmartSettings(scanner interface{ Scan(dest ...any) error }) (models.SmartExtractionSettings, error) {
	var settings models.SmartExtractionSettings
	var enabled, alertsEnabled int
	var lastReprocessedAt, createdAt, updatedAt string
	err := scanner.Scan(&settings.TenantID, &enabled, &alertsEnabled, &settings.RefreshStatus, &settings.RefreshMessage, &lastReprocessedAt, &createdAt, &updatedAt)
	if err != nil {
		return models.SmartExtractionSettings{}, err
	}
	settings.Enabled = intToBool(enabled)
	settings.AlertsEnabled = intToBool(alertsEnabled)
	settings.LastReprocessedAt = parseSQLiteTime(lastReprocessedAt)
	settings.CreatedAt = parseSQLiteTime(createdAt)
	settings.UpdatedAt = parseSQLiteTime(updatedAt)
	return settings, nil
}

func scanSmartGroup(scanner interface{ Scan(dest ...any) error }) (models.SmartKeywordGroup, error) {
	var group models.SmartKeywordGroup
	var enabled int
	var matchMode, excludeTerms, createdAt, updatedAt string
	err := scanner.Scan(&group.ID, &group.TenantID, &group.Name, &group.TagName, &group.Description, &enabled, &matchMode, &excludeTerms, &group.MinMatchCount, &group.Priority, &createdAt, &updatedAt)
	if err != nil {
		return models.SmartKeywordGroup{}, err
	}
	group.Enabled = intToBool(enabled)
	group.MatchMode = models.SmartMatchMode(matchMode)
	group.ExcludeTerms = decodeStringSlice(excludeTerms)
	group.CreatedAt = parseSQLiteTime(createdAt)
	group.UpdatedAt = parseSQLiteTime(updatedAt)
	return normalizeSmartGroup(group), nil
}

func scanSmartKeyword(scanner interface{ Scan(dest ...any) error }) (models.SmartKeyword, error) {
	var keyword models.SmartKeyword
	var enabled int
	var createdAt, updatedAt string
	err := scanner.Scan(&keyword.ID, &keyword.TenantID, &keyword.GroupID, &keyword.Value, &keyword.NormalizedValue, &enabled, &createdAt, &updatedAt)
	if err != nil {
		return models.SmartKeyword{}, err
	}
	keyword.Enabled = intToBool(enabled)
	keyword.CreatedAt = parseSQLiteTime(createdAt)
	keyword.UpdatedAt = parseSQLiteTime(updatedAt)
	return keyword, nil
}

func scanSavedSmartView(scanner interface{ Scan(dest ...any) error }) (models.SavedSmartView, error) {
	var view models.SavedSmartView
	var pinned, alertsEnabled, alertPaused int
	var channels, createdAt, updatedAt string
	err := scanner.Scan(&view.ID, &view.TenantID, &view.UserID, &view.Name, &view.FiltersJSON, &pinned, &alertsEnabled, &alertPaused, &view.AlertFrequency, &channels, &createdAt, &updatedAt)
	if err != nil {
		return models.SavedSmartView{}, err
	}
	view.Pinned = intToBool(pinned)
	view.AlertsEnabled = intToBool(alertsEnabled)
	view.AlertPaused = intToBool(alertPaused)
	view.AlertChannels = decodeChannels(channels)
	view.CreatedAt = parseSQLiteTime(createdAt)
	view.UpdatedAt = parseSQLiteTime(updatedAt)
	return view, nil
}

func scanSmartAlertDelivery(scanner interface{ Scan(dest ...any) error }) (models.SmartAlertDelivery, error) {
	var delivery models.SmartAlertDelivery
	var createdAt, sentAt string
	err := scanner.Scan(&delivery.ID, &delivery.TenantID, &delivery.ViewID, &delivery.TenderID, &delivery.ChannelType, &delivery.Destination, &delivery.Frequency, &delivery.Status, &delivery.Error, &delivery.DedupKey, &delivery.Message, &createdAt, &sentAt)
	if err != nil {
		return models.SmartAlertDelivery{}, err
	}
	delivery.CreatedAt = parseSQLiteTime(createdAt)
	delivery.SentAt = parseSQLiteTime(sentAt)
	return delivery, nil
}

func (s *SQLiteStore) GetSmartExtractionSettings(ctx context.Context, tenantID string) (models.SmartExtractionSettings, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return models.SmartExtractionSettings{}, fmt.Errorf("smart extraction settings require tenant_id")
	}
	row := s.db.QueryRowContext(ctx, `
		select tenant_id, enabled, alerts_enabled, refresh_status, refresh_message, last_reprocessed_at, created_at, updated_at
		from smart_extraction_settings
		where tenant_id = ?
	`, tenantID)
	settings, err := scanSmartSettings(row)
	if errors.Is(err, sql.ErrNoRows) {
		now := time.Now().UTC()
		return models.SmartExtractionSettings{
			TenantID:       tenantID,
			RefreshStatus:  "pending",
			RefreshMessage: "Smart Keyword Extraction is disabled.",
			CreatedAt:      now,
			UpdatedAt:      now,
		}, nil
	}
	return settings, err
}

func (s *SQLiteStore) UpsertSmartExtractionSettings(ctx context.Context, settings models.SmartExtractionSettings) error {
	settings.TenantID = strings.TrimSpace(settings.TenantID)
	if settings.TenantID == "" {
		return fmt.Errorf("smart extraction settings require tenant_id")
	}
	if settings.Enabled {
		active, err := s.activeSmartKeywordCount(ctx, settings.TenantID)
		if err != nil {
			return err
		}
		if active == 0 {
			return fmt.Errorf("enable at least one standalone keyword or group keyword before turning on Smart Keyword Extraction")
		}
	}
	existing, _ := s.GetSmartExtractionSettings(ctx, settings.TenantID)
	now := time.Now().UTC()
	if existing.CreatedAt.IsZero() {
		existing.CreatedAt = now
	}
	if strings.TrimSpace(settings.RefreshStatus) == "" {
		settings.RefreshStatus = existing.RefreshStatus
	}
	if strings.TrimSpace(settings.RefreshMessage) == "" {
		settings.RefreshMessage = existing.RefreshMessage
	}
	settings.CreatedAt = existing.CreatedAt
	settings.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
		insert into smart_extraction_settings(tenant_id, enabled, alerts_enabled, refresh_status, refresh_message, last_reprocessed_at, created_at, updated_at)
		values(?,?,?,?,?,?,?,?)
		on conflict(tenant_id) do update set
			enabled=excluded.enabled,
			alerts_enabled=excluded.alerts_enabled,
			refresh_status=excluded.refresh_status,
			refresh_message=excluded.refresh_message,
			last_reprocessed_at=coalesce(nullif(excluded.last_reprocessed_at, ''), smart_extraction_settings.last_reprocessed_at),
			updated_at=excluded.updated_at
	`, settings.TenantID, boolToInt(settings.Enabled), boolToInt(settings.AlertsEnabled), settings.RefreshStatus, settings.RefreshMessage, sqliteTimeString(settings.LastReprocessedAt), sqliteTimeString(settings.CreatedAt), sqliteTimeString(settings.UpdatedAt))
	return err
}

func (s *SQLiteStore) activeSmartKeywordCount(ctx context.Context, tenantID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		select count(*)
		from smart_keyword_records k
		left join smart_keyword_groups g on g.id = k.group_id and g.tenant_id = k.tenant_id
		where k.tenant_id = ? and k.enabled = 1 and k.normalized_value <> ''
		  and (k.group_id = '' or (g.enabled = 1 and g.id is not null))
	`, strings.TrimSpace(tenantID)).Scan(&count)
	return count, err
}

func (s *SQLiteStore) ListSmartKeywordGroups(ctx context.Context, tenantID string) ([]models.SmartKeywordGroup, error) {
	rows, err := s.db.QueryContext(ctx, `
		select id, tenant_id, name, tag_name, description, enabled, match_mode, exclude_terms, min_match_count, priority, created_at, updated_at
		from smart_keyword_groups
		where tenant_id = ?
		order by lower(name) asc, id asc
	`, strings.TrimSpace(tenantID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.SmartKeywordGroup{}
	for rows.Next() {
		group, err := scanSmartGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, group)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) UpsertSmartKeywordGroup(ctx context.Context, group models.SmartKeywordGroup) (models.SmartKeywordGroup, error) {
	group = normalizeSmartGroup(group)
	group.TenantID = strings.TrimSpace(group.TenantID)
	if group.TenantID == "" || group.Name == "" {
		return models.SmartKeywordGroup{}, fmt.Errorf("keyword group requires tenant_id and name")
	}
	now := time.Now().UTC()
	if group.ID != "" {
		existing, err := s.smartGroupByID(ctx, group.TenantID, group.ID)
		if err != nil {
			return models.SmartKeywordGroup{}, err
		}
		group.CreatedAt = existing.CreatedAt
	}
	if group.ID == "" {
		group.ID = newid()
		group.CreatedAt = now
	}
	if group.CreatedAt.IsZero() {
		group.CreatedAt = now
	}
	group.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
		insert into smart_keyword_groups(id, tenant_id, name, tag_name, description, enabled, match_mode, exclude_terms, min_match_count, priority, created_at, updated_at)
		values(?,?,?,?,?,?,?,?,?,?,?,?)
		on conflict(id) do update set
			name=excluded.name,
			tag_name=excluded.tag_name,
			description=excluded.description,
			enabled=excluded.enabled,
			match_mode=excluded.match_mode,
			exclude_terms=excluded.exclude_terms,
			min_match_count=excluded.min_match_count,
			priority=excluded.priority,
			updated_at=excluded.updated_at
	`, group.ID, group.TenantID, group.Name, group.TagName, group.Description, boolToInt(group.Enabled), string(group.MatchMode), encodeStringSlice(group.ExcludeTerms), group.MinMatchCount, group.Priority, sqliteTimeString(group.CreatedAt), sqliteTimeString(group.UpdatedAt))
	if err != nil {
		return models.SmartKeywordGroup{}, err
	}
	return group, nil
}

func (s *SQLiteStore) smartGroupByID(ctx context.Context, tenantID, id string) (models.SmartKeywordGroup, error) {
	row := s.db.QueryRowContext(ctx, `
		select id, tenant_id, name, tag_name, description, enabled, match_mode, exclude_terms, min_match_count, priority, created_at, updated_at
		from smart_keyword_groups
		where tenant_id = ? and id = ?
	`, strings.TrimSpace(tenantID), strings.TrimSpace(id))
	group, err := scanSmartGroup(row)
	if errors.Is(err, sql.ErrNoRows) {
		return models.SmartKeywordGroup{}, ErrNotFound
	}
	return group, err
}

func (s *SQLiteStore) DeleteSmartKeywordGroup(ctx context.Context, tenantID, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "delete from smart_keyword_records where tenant_id = ? and group_id = ?", strings.TrimSpace(tenantID), strings.TrimSpace(id)); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err = tx.ExecContext(ctx, "delete from smart_keyword_groups where tenant_id = ? and id = ?", strings.TrimSpace(tenantID), strings.TrimSpace(id)); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) ListSmartKeywords(ctx context.Context, tenantID string) ([]models.SmartKeyword, error) {
	rows, err := s.db.QueryContext(ctx, `
		select id, tenant_id, group_id, value, normalized_value, enabled, created_at, updated_at
		from smart_keyword_records
		where tenant_id = ?
		order by case when group_id = '' then 0 else 1 end, lower(value) asc, id asc
	`, strings.TrimSpace(tenantID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.SmartKeyword{}
	for rows.Next() {
		keyword, err := scanSmartKeyword(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, keyword)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) UpsertSmartKeyword(ctx context.Context, keyword models.SmartKeyword) (models.SmartKeyword, error) {
	keyword.TenantID = strings.TrimSpace(keyword.TenantID)
	keyword.GroupID = strings.TrimSpace(keyword.GroupID)
	keyword = normalizeSmartKeyword(keyword)
	if keyword.TenantID == "" || keyword.NormalizedValue == "" {
		return models.SmartKeyword{}, fmt.Errorf("keyword requires tenant_id and value")
	}
	if keyword.GroupID != "" {
		if _, err := s.smartGroupByID(ctx, keyword.TenantID, keyword.GroupID); err != nil {
			return models.SmartKeyword{}, err
		}
	}
	now := time.Now().UTC()
	if keyword.ID != "" {
		existing, err := s.smartKeywordByID(ctx, keyword.TenantID, keyword.ID)
		if err != nil {
			return models.SmartKeyword{}, err
		}
		keyword.CreatedAt = existing.CreatedAt
	}
	if keyword.ID == "" {
		var existingID, createdAt string
		err := s.db.QueryRowContext(ctx, `
			select id, created_at
			from smart_keyword_records
			where tenant_id = ? and coalesce(group_id, '') = ? and normalized_value = ?
			limit 1
		`, keyword.TenantID, keyword.GroupID, keyword.NormalizedValue).Scan(&existingID, &createdAt)
		if err == nil {
			keyword.ID = existingID
			keyword.CreatedAt = parseSQLiteTime(createdAt)
		} else if errors.Is(err, sql.ErrNoRows) {
			keyword.ID = newid()
			keyword.CreatedAt = now
		} else {
			return models.SmartKeyword{}, err
		}
	}
	if keyword.CreatedAt.IsZero() {
		keyword.CreatedAt = now
	}
	keyword.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
		insert into smart_keyword_records(id, tenant_id, group_id, value, normalized_value, enabled, created_at, updated_at)
		values(?,?,?,?,?,?,?,?)
		on conflict(id) do update set
			group_id=excluded.group_id,
			value=excluded.value,
			normalized_value=excluded.normalized_value,
			enabled=excluded.enabled,
			updated_at=excluded.updated_at
	`, keyword.ID, keyword.TenantID, keyword.GroupID, keyword.Value, keyword.NormalizedValue, boolToInt(keyword.Enabled), sqliteTimeString(keyword.CreatedAt), sqliteTimeString(keyword.UpdatedAt))
	if err != nil {
		return models.SmartKeyword{}, err
	}
	return keyword, nil
}

func (s *SQLiteStore) smartKeywordByID(ctx context.Context, tenantID, id string) (models.SmartKeyword, error) {
	row := s.db.QueryRowContext(ctx, `
		select id, tenant_id, group_id, value, normalized_value, enabled, created_at, updated_at
		from smart_keyword_records
		where tenant_id = ? and id = ?
	`, strings.TrimSpace(tenantID), strings.TrimSpace(id))
	keyword, err := scanSmartKeyword(row)
	if errors.Is(err, sql.ErrNoRows) {
		return models.SmartKeyword{}, ErrNotFound
	}
	return keyword, err
}

func (s *SQLiteStore) DeleteSmartKeyword(ctx context.Context, tenantID, id string) error {
	_, err := s.db.ExecContext(ctx, "delete from smart_keyword_records where tenant_id = ? and id = ?", strings.TrimSpace(tenantID), strings.TrimSpace(id))
	return err
}

func (s *SQLiteStore) smartConfig(ctx context.Context, tenantID string) (models.SmartExtractionSettings, []models.SmartKeywordGroup, []models.SmartKeyword, error) {
	settings, err := s.GetSmartExtractionSettings(ctx, tenantID)
	if err != nil {
		return settings, nil, nil, err
	}
	groups, err := s.ListSmartKeywordGroups(ctx, tenantID)
	if err != nil {
		return settings, nil, nil, err
	}
	keywords, err := s.ListSmartKeywords(ctx, tenantID)
	if err != nil {
		return settings, nil, nil, err
	}
	return settings, groups, keywords, nil
}

func (s *SQLiteStore) EvaluateSmartTenderForExtraction(ctx context.Context, tender models.Tender) (models.Tender, models.SmartKeywordEvaluation, bool, error) {
	rows, err := s.db.QueryContext(ctx, "select tenant_id from smart_extraction_settings where enabled = 1 order by tenant_id asc")
	if err != nil {
		return tender, models.SmartKeywordEvaluation{}, true, err
	}
	defer rows.Close()
	tenantIDs := []string{}
	for rows.Next() {
		var tenantID string
		if err := rows.Scan(&tenantID); err != nil {
			return tender, models.SmartKeywordEvaluation{}, true, err
		}
		tenantIDs = append(tenantIDs, tenantID)
	}
	if err := rows.Err(); err != nil {
		return tender, models.SmartKeywordEvaluation{}, true, err
	}
	if len(tenantIDs) == 0 {
		return tender, models.SmartKeywordEvaluation{Accepted: true, Reasons: []string{"Smart Keyword Extraction is disabled."}}, true, nil
	}
	combined := models.SmartKeywordEvaluation{Enabled: true}
	accepted := false
	for _, tenantID := range tenantIDs {
		settings, groups, keywords, err := s.smartConfig(ctx, tenantID)
		if err != nil {
			return tender, combined, false, err
		}
		evaluation := smartkeywords.Evaluate(tender, settings.Enabled, groups, keywords)
		if evaluation.Accepted {
			accepted = true
		}
		combined.ActiveKeywordCount += evaluation.ActiveKeywordCount
		combined.MatchedKeywords = append(combined.MatchedKeywords, evaluation.MatchedKeywords...)
		combined.StandaloneMatches = append(combined.StandaloneMatches, evaluation.StandaloneMatches...)
		combined.GroupTags = append(combined.GroupTags, evaluation.GroupTags...)
		combined.GroupMatches = append(combined.GroupMatches, evaluation.GroupMatches...)
		combined.Reasons = append(combined.Reasons, evaluation.Reasons...)
	}
	combined.Accepted = accepted
	combined.MatchedKeywords = uniqueDisplayTerms(combined.MatchedKeywords)
	combined.StandaloneMatches = uniqueDisplayTerms(combined.StandaloneMatches)
	combined.GroupTags = uniqueDisplayTerms(combined.GroupTags)
	tender.GroupTags = combined.GroupTags
	return tender, combined, accepted, nil
}

func (s *SQLiteStore) refreshSmartMatchForTenant(ctx context.Context, tenantID string, tender models.Tender, triggerAlerts bool) (models.SmartKeywordEvaluation, error) {
	settings, groups, keywords, err := s.smartConfig(ctx, tenantID)
	if err != nil {
		return models.SmartKeywordEvaluation{}, err
	}
	return s.refreshSmartMatchWithConfig(ctx, tenantID, tender, settings, groups, keywords, triggerAlerts)
}

func (s *SQLiteStore) refreshSmartMatchWithConfig(ctx context.Context, tenantID string, tender models.Tender, settings models.SmartExtractionSettings, groups []models.SmartKeywordGroup, keywords []models.SmartKeyword, triggerAlerts bool) (models.SmartKeywordEvaluation, error) {
	evaluation, err := writeSmartTenderMatch(ctx, s.db, tenantID, tender, settings, groups, keywords)
	if err != nil {
		return models.SmartKeywordEvaluation{}, err
	}
	log.Printf("smart keyword evaluation tenant=%s tender=%s accepted=%t group_tags=%s reasons=%s", tenantID, tender.ID, evaluation.Accepted, strings.Join(evaluation.GroupTags, ","), strings.Join(evaluation.Reasons, "; "))
	if triggerAlerts && evaluation.Accepted {
		if err := s.triggerSmartViewAlertsForTender(ctx, tenantID, tender, "tender_change"); err != nil {
			log.Printf("smart alert trigger failed tenant=%s tender=%s error=%v", tenantID, tender.ID, err)
		}
	}
	return evaluation, nil
}

func writeSmartTenderMatch(ctx context.Context, exec sqlExecer, tenantID string, tender models.Tender, settings models.SmartExtractionSettings, groups []models.SmartKeywordGroup, keywords []models.SmartKeyword) (models.SmartKeywordEvaluation, error) {
	evaluation := smartkeywords.Evaluate(tender, settings.Enabled, groups, keywords)
	now := time.Now().UTC()
	_, err := exec.ExecContext(ctx, `
		insert into smart_tender_matches(tenant_id, tender_id, accepted, group_tags, matched_keywords, standalone_keywords, reasons, updated_at)
		values(?,?,?,?,?,?,?,?)
		on conflict(tenant_id, tender_id) do update set
			accepted=excluded.accepted,
			group_tags=excluded.group_tags,
			matched_keywords=excluded.matched_keywords,
			standalone_keywords=excluded.standalone_keywords,
			reasons=excluded.reasons,
			updated_at=excluded.updated_at
	`, tenantID, tender.ID, boolToInt(evaluation.Accepted), encodeStringSlice(evaluation.GroupTags), encodeStringSlice(evaluation.MatchedKeywords), encodeStringSlice(evaluation.StandaloneMatches), encodeStringSlice(evaluation.Reasons), sqliteTimeString(now))
	if err != nil {
		return models.SmartKeywordEvaluation{}, err
	}
	return evaluation, nil
}

func (s *SQLiteStore) refreshSmartMatchesForTender(ctx context.Context, tender models.Tender, triggerAlerts bool) error {
	rows, err := s.db.QueryContext(ctx, "select tenant_id from smart_extraction_settings order by tenant_id asc")
	if err != nil {
		return err
	}
	defer rows.Close()
	tenantIDs := []string{}
	for rows.Next() {
		var tenantID string
		if err := rows.Scan(&tenantID); err != nil {
			return err
		}
		tenantIDs = append(tenantIDs, tenantID)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, tenantID := range tenantIDs {
		if _, err := s.refreshSmartMatchForTenant(ctx, tenantID, tender, triggerAlerts); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) PreviewSmartKeywords(ctx context.Context, tenantID string, limit int) ([]models.SmartTenderPreview, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	settings, groups, keywords, err := s.smartConfig(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	tenders, err := s.listAllTenders(ctx)
	if err != nil {
		return nil, err
	}
	out := []models.SmartTenderPreview{}
	for _, tender := range tenders {
		if tenderArchived(tender) {
			continue
		}
		out = append(out, models.SmartTenderPreview{
			Tender:     tender,
			Evaluation: smartkeywords.Evaluate(tender, settings.Enabled, groups, keywords),
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *SQLiteStore) ReprocessSmartKeywords(ctx context.Context, tenantID string) (models.SmartReprocessResult, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return models.SmartReprocessResult{}, fmt.Errorf("tenant_id is required")
	}
	settings, groups, keywords, err := s.smartConfig(ctx, tenantID)
	if err != nil {
		return models.SmartReprocessResult{}, err
	}
	tenders, err := s.listAllTenders(ctx)
	if err != nil {
		return models.SmartReprocessResult{}, err
	}
	result := models.SmartReprocessResult{TenantID: tenantID, UpdatedAt: time.Now().UTC()}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return models.SmartReprocessResult{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	acceptedForAlerts := []models.Tender{}
	for _, tender := range tenders {
		if tenderArchived(tender) {
			continue
		}
		evaluation, err := writeSmartTenderMatch(ctx, tx, tenantID, tender, settings, groups, keywords)
		if err != nil {
			return models.SmartReprocessResult{}, err
		}
		result.Processed++
		originalGroupTags := strings.Join(tender.GroupTags, "\x00")
		if evaluation.Accepted {
			result.Accepted++
			tender.GroupTags = evaluation.GroupTags
			acceptedForAlerts = append(acceptedForAlerts, tender)
		} else {
			result.Excluded++
			tender.GroupTags = nil
		}
		if originalGroupTags != strings.Join(tender.GroupTags, "\x00") {
			if err := sqliteUpsertJSONExec(ctx, tx, "tenders", tender.ID, tender); err != nil {
				return models.SmartReprocessResult{}, err
			}
			if err := upsertTenderDashboardIndex(ctx, tx, tender); err != nil {
				return models.SmartReprocessResult{}, err
			}
		}
	}
	_, err = tx.ExecContext(ctx, `
		update smart_extraction_settings
		set refresh_status = ?, refresh_message = ?, last_reprocessed_at = ?, updated_at = ?
		where tenant_id = ?
	`, "success", fmt.Sprintf("Reprocessed %d tenders.", result.Processed), sqliteTimeString(result.UpdatedAt), sqliteTimeString(result.UpdatedAt), tenantID)
	if err != nil {
		return models.SmartReprocessResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return models.SmartReprocessResult{}, err
	}
	committed = true
	log.Printf("smart keyword reprocess tenant=%s processed=%d accepted=%d excluded=%d", tenantID, result.Processed, result.Accepted, result.Excluded)
	for _, tender := range acceptedForAlerts {
		if err := s.triggerSmartViewAlertsForTender(ctx, tenantID, tender, "smart_reprocess"); err != nil {
			log.Printf("smart alert trigger failed tenant=%s tender=%s error=%v", tenantID, tender.ID, err)
		}
	}
	return result, nil
}

func (s *SQLiteStore) SeedSmartKeywordsFromCSV(ctx context.Context, tenantID, path string) error {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" || strings.TrimSpace(path) == "" {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	reader := csv.NewReader(file)
	rows, err := reader.ReadAll()
	if err != nil {
		return err
	}
	if len(rows) <= 1 {
		return nil
	}
	groupsByName := map[string]models.SmartKeywordGroup{}
	existingGroups, err := s.ListSmartKeywordGroups(ctx, tenantID)
	if err != nil {
		return err
	}
	for _, group := range existingGroups {
		groupsByName[smartkeywords.NormalizeTerm(group.Name)] = group
	}
	createdGroups, createdKeywords := 0, 0
	for _, row := range rows[1:] {
		if len(row) < 3 {
			continue
		}
		groupName := strings.Join(strings.Fields(strings.TrimSpace(row[0])), " ")
		purpose := strings.TrimSpace(row[1])
		keywordValue := strings.TrimSpace(row[2])
		if groupName == "" || keywordValue == "" {
			continue
		}
		groupKey := smartkeywords.NormalizeTerm(groupName)
		group, ok := groupsByName[groupKey]
		if !ok {
			group, err = s.UpsertSmartKeywordGroup(ctx, models.SmartKeywordGroup{
				TenantID: tenantID, Name: groupName, TagName: groupName, Description: purpose,
				Enabled: false, MatchMode: models.SmartMatchModeAny, ExcludeTerms: []string{}, MinMatchCount: 1, Priority: 0,
			})
			if err != nil {
				return err
			}
			groupsByName[groupKey] = group
			createdGroups++
		} else {
			backfilled := false
			if group.TagName == "" {
				group.TagName = group.Name
				backfilled = true
			}
			if group.MatchMode == "" {
				group.MatchMode = models.SmartMatchModeAny
				backfilled = true
			}
			if group.MinMatchCount <= 0 {
				group.MinMatchCount = 1
				backfilled = true
			}
			if group.Description == "" && purpose != "" {
				group.Description = purpose
				backfilled = true
			}
			if backfilled {
				if group, err = s.UpsertSmartKeywordGroup(ctx, group); err != nil {
					return err
				}
				groupsByName[groupKey] = group
			}
		}
		existingKeyword, err := s.smartKeywordByNaturalKey(ctx, tenantID, group.ID, smartkeywords.NormalizeTerm(keywordValue))
		if err == nil {
			if existingKeyword.Value == "" || existingKeyword.NormalizedValue == "" {
				existingKeyword.Value = keywordValue
				existingKeyword.NormalizedValue = smartkeywords.NormalizeTerm(keywordValue)
				if _, err := s.UpsertSmartKeyword(ctx, existingKeyword); err != nil {
					return err
				}
			}
			continue
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		if _, err := s.UpsertSmartKeyword(ctx, models.SmartKeyword{TenantID: tenantID, GroupID: group.ID, Value: keywordValue, Enabled: true}); err != nil {
			return err
		}
		createdKeywords++
	}
	log.Printf("smart seed completed tenant=%s created_groups=%d created_keywords=%d source=%s", tenantID, createdGroups, createdKeywords, path)
	return nil
}

func (s *SQLiteStore) smartKeywordByNaturalKey(ctx context.Context, tenantID, groupID, normalized string) (models.SmartKeyword, error) {
	row := s.db.QueryRowContext(ctx, `
		select id, tenant_id, group_id, value, normalized_value, enabled, created_at, updated_at
		from smart_keyword_records
		where tenant_id = ? and coalesce(group_id, '') = ? and normalized_value = ?
	`, tenantID, groupID, normalized)
	keyword, err := scanSmartKeyword(row)
	if errors.Is(err, sql.ErrNoRows) {
		return models.SmartKeyword{}, ErrNotFound
	}
	return keyword, err
}

func (s *SQLiteStore) ListSavedSmartViews(ctx context.Context, tenantID, userID string) ([]models.SavedSmartView, error) {
	rows, err := s.db.QueryContext(ctx, `
		select id, tenant_id, user_id, name, filters_json, pinned, alerts_enabled, alert_paused, alert_frequency, alert_channels, created_at, updated_at
		from saved_smart_views
		where tenant_id = ? and user_id = ?
		order by pinned desc, lower(name) asc
	`, strings.TrimSpace(tenantID), strings.TrimSpace(userID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.SavedSmartView{}
	for rows.Next() {
		view, err := scanSavedSmartView(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, view)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) UpsertSavedSmartView(ctx context.Context, view models.SavedSmartView) (models.SavedSmartView, error) {
	view.TenantID = strings.TrimSpace(view.TenantID)
	view.UserID = strings.TrimSpace(view.UserID)
	view.Name = strings.Join(strings.Fields(strings.TrimSpace(view.Name)), " ")
	if view.TenantID == "" || view.UserID == "" || view.Name == "" {
		return models.SavedSmartView{}, fmt.Errorf("saved smart view requires tenant_id, user_id, and name")
	}
	if strings.TrimSpace(view.FiltersJSON) == "" {
		view.FiltersJSON = "{}"
	}
	var filter models.SmartViewFilters
	if err := json.Unmarshal([]byte(view.FiltersJSON), &filter); err != nil {
		return models.SavedSmartView{}, fmt.Errorf("invalid smart view filters: %w", err)
	}
	if view.AlertFrequency == "" {
		view.AlertFrequency = "immediate"
	}
	now := time.Now().UTC()
	if view.ID != "" {
		existing, err := s.savedSmartViewByID(ctx, view.TenantID, view.UserID, view.ID)
		if err != nil {
			return models.SavedSmartView{}, err
		}
		view.CreatedAt = existing.CreatedAt
	}
	if view.ID == "" {
		view.ID = newid()
		view.CreatedAt = now
	}
	if view.CreatedAt.IsZero() {
		view.CreatedAt = now
	}
	view.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
		insert into saved_smart_views(id, tenant_id, user_id, name, filters_json, pinned, alerts_enabled, alert_paused, alert_frequency, alert_channels, created_at, updated_at)
		values(?,?,?,?,?,?,?,?,?,?,?,?)
		on conflict(id) do update set
			name=excluded.name,
			filters_json=excluded.filters_json,
			pinned=excluded.pinned,
			alerts_enabled=excluded.alerts_enabled,
			alert_paused=excluded.alert_paused,
			alert_frequency=excluded.alert_frequency,
			alert_channels=excluded.alert_channels,
			updated_at=excluded.updated_at
	`, view.ID, view.TenantID, view.UserID, view.Name, view.FiltersJSON, boolToInt(view.Pinned), boolToInt(view.AlertsEnabled), boolToInt(view.AlertPaused), view.AlertFrequency, encodeChannels(view.AlertChannels), sqliteTimeString(view.CreatedAt), sqliteTimeString(view.UpdatedAt))
	if err != nil {
		return models.SavedSmartView{}, err
	}
	return view, nil
}

func (s *SQLiteStore) savedSmartViewByID(ctx context.Context, tenantID, userID, id string) (models.SavedSmartView, error) {
	row := s.db.QueryRowContext(ctx, `
		select id, tenant_id, user_id, name, filters_json, pinned, alerts_enabled, alert_paused, alert_frequency, alert_channels, created_at, updated_at
		from saved_smart_views
		where tenant_id = ? and user_id = ? and id = ?
	`, strings.TrimSpace(tenantID), strings.TrimSpace(userID), strings.TrimSpace(id))
	view, err := scanSavedSmartView(row)
	if errors.Is(err, sql.ErrNoRows) {
		return models.SavedSmartView{}, ErrNotFound
	}
	return view, err
}

func (s *SQLiteStore) DeleteSavedSmartView(ctx context.Context, tenantID, userID, id string) error {
	_, err := s.db.ExecContext(ctx, "delete from saved_smart_views where tenant_id = ? and user_id = ? and id = ?", strings.TrimSpace(tenantID), strings.TrimSpace(userID), strings.TrimSpace(id))
	return err
}

func (s *SQLiteStore) viewMatchesTender(view models.SavedSmartView, tender models.Tender) bool {
	var filter models.SmartViewFilters
	if err := json.Unmarshal([]byte(view.FiltersJSON), &filter); err != nil {
		return false
	}
	if filter.Query != "" && !strings.Contains(smartkeywords.TenderSearchText(tender), smartkeywords.NormalizeTerm(filter.Query)) {
		return false
	}
	if filter.Source != "" && tender.SourceKey != filter.Source {
		return false
	}
	if filter.Issuer != "" && !ContainsCI(tender.Issuer, filter.Issuer) {
		return false
	}
	if filter.Category != "" && !strings.EqualFold(tender.Category, filter.Category) {
		return false
	}
	if filter.Status != "" && !strings.EqualFold(tender.Status, filter.Status) {
		return false
	}
	if filter.DateFrom != "" && tender.PublishedDate < filter.DateFrom {
		return false
	}
	if filter.DateTo != "" && tender.PublishedDate > filter.DateTo {
		return false
	}
	for _, wanted := range filter.GroupTags {
		found := false
		for _, tag := range tender.GroupTags {
			if strings.EqualFold(tag, wanted) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (s *SQLiteStore) triggerSmartViewAlertsForTender(ctx context.Context, tenantID string, tender models.Tender, trigger string) error {
	settings, err := s.GetSmartExtractionSettings(ctx, tenantID)
	if err != nil || !settings.AlertsEnabled {
		return err
	}
	rows, err := s.db.QueryContext(ctx, `
		select id, tenant_id, user_id, name, filters_json, pinned, alerts_enabled, alert_paused, alert_frequency, alert_channels, created_at, updated_at
		from saved_smart_views
		where tenant_id = ? and alerts_enabled = 1 and alert_paused = 0
	`, tenantID)
	if err != nil {
		return err
	}
	defer rows.Close()
	views := []models.SavedSmartView{}
	for rows.Next() {
		view, err := scanSavedSmartView(rows)
		if err != nil {
			return err
		}
		views = append(views, view)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, view := range views {
		if !s.viewMatchesTender(view, tender) {
			continue
		}
		for _, channel := range view.AlertChannels {
			if !channel.Enabled {
				continue
			}
			if _, err := s.recordSmartAlertDelivery(ctx, tenantID, view, tender, channel, trigger, false); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *SQLiteStore) recordSmartAlertDelivery(ctx context.Context, tenantID string, view models.SavedSmartView, tender models.Tender, channel models.NotificationChannel, trigger string, force bool) (models.SmartAlertDelivery, error) {
	now := time.Now().UTC()
	frequency := strings.TrimSpace(view.AlertFrequency)
	if frequency == "" {
		frequency = "immediate"
	}
	dedupKey := strings.Join([]string{tenantID, view.ID, tender.ID, strings.ToLower(channel.Type), strings.ToLower(channel.Destination), strings.Join(tender.GroupTags, "|")}, "|")
	status := "sent"
	if frequency != "immediate" && !force {
		status = "pending_digest"
	}
	delivery := models.SmartAlertDelivery{
		ID:          newid(),
		TenantID:    tenantID,
		ViewID:      view.ID,
		TenderID:    tender.ID,
		ChannelType: channel.Type,
		Destination: channel.Destination,
		Frequency:   frequency,
		Status:      status,
		DedupKey:    dedupKey,
		Message:     fmt.Sprintf("%s matched %s via %s. Group Tags: %s", tender.Title, view.Name, trigger, strings.Join(tender.GroupTags, ", ")),
		CreatedAt:   now,
		SentAt:      now,
	}
	result, err := s.db.ExecContext(ctx, `
		insert into smart_alert_deliveries(id, tenant_id, view_id, tender_id, channel_type, destination, frequency, status, error, dedup_key, message, created_at, sent_at)
		values(?,?,?,?,?,?,?,?,?,?,?,?,?)
		on conflict(dedup_key) do nothing
	`, delivery.ID, delivery.TenantID, delivery.ViewID, delivery.TenderID, delivery.ChannelType, delivery.Destination, delivery.Frequency, delivery.Status, delivery.Error, delivery.DedupKey, delivery.Message, sqliteTimeString(delivery.CreatedAt), sqliteTimeString(delivery.SentAt))
	if err != nil {
		return models.SmartAlertDelivery{}, err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		log.Printf("smart alert duplicate suppressed tenant=%s view=%s tender=%s channel=%s", tenantID, view.ID, tender.ID, channel.Type)
		return delivery, nil
	}
	log.Printf("smart alert delivery tenant=%s view=%s tender=%s channel=%s status=%s", tenantID, view.ID, tender.ID, channel.Type, delivery.Status)
	return delivery, nil
}

func (s *SQLiteStore) ListSmartAlertDeliveries(ctx context.Context, tenantID, viewID string) ([]models.SmartAlertDelivery, error) {
	rows, err := s.db.QueryContext(ctx, `
		select id, tenant_id, view_id, tender_id, channel_type, destination, frequency, status, error, dedup_key, message, created_at, sent_at
		from smart_alert_deliveries
		where tenant_id = ? and (? = '' or view_id = ?)
		order by created_at desc
	`, strings.TrimSpace(tenantID), strings.TrimSpace(viewID), strings.TrimSpace(viewID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.SmartAlertDelivery{}
	for rows.Next() {
		delivery, err := scanSmartAlertDelivery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, delivery)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) TestSmartViewAlert(ctx context.Context, tenantID, userID, viewID string) (models.SmartAlertDelivery, error) {
	view, err := s.savedSmartViewByID(ctx, tenantID, userID, viewID)
	if err != nil {
		return models.SmartAlertDelivery{}, err
	}
	channel := models.NotificationChannel{Type: "in_app", Destination: userID, Enabled: true}
	if len(view.AlertChannels) > 0 {
		channel = view.AlertChannels[0]
	}
	tender := models.Tender{ID: "test-alert", Title: "Test Smart View alert", SourceKey: "openbid", Issuer: "OpenBid", GroupTags: []string{"Test"}}
	return s.recordSmartAlertDelivery(ctx, tenantID, view, tender, channel, "test", true)
}
