package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"openbid/internal/models"
)

func normalizeKeywordValue(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func keywordComparable(value string) string {
	return strings.ToLower(normalizeKeywordValue(value))
}

func encodeKeywordList(values []string) string {
	return encodeStringSlice(values)
}

func decodeKeywordList(value string) []string {
	return decodeStringSlice(value)
}

func scanKeywordProfile(scanner interface {
	Scan(dest ...any) error
}) (models.KeywordProfile, error) {
	var profile models.KeywordProfile
	var lastRefreshedAt, createdAt, updatedAt string
	err := scanner.Scan(
		&profile.ID, &profile.TenantID, &profile.UserID, &profile.Name, &profile.RefreshStatus,
		&profile.RefreshMessage, &profile.MatchCount, &lastRefreshedAt, &createdAt, &updatedAt,
	)
	if err != nil {
		return models.KeywordProfile{}, err
	}
	profile.LastRefreshedAt = parseSQLiteTime(lastRefreshedAt)
	profile.CreatedAt = parseSQLiteTime(createdAt)
	profile.UpdatedAt = parseSQLiteTime(updatedAt)
	return profile, nil
}

func scanKeyword(scanner interface {
	Scan(dest ...any) error
}) (models.Keyword, error) {
	var keyword models.Keyword
	var enabled int
	var createdAt, updatedAt string
	err := scanner.Scan(&keyword.ID, &keyword.ProfileID, &keyword.TenantID, &keyword.UserID, &keyword.Value, &enabled, &createdAt, &updatedAt)
	if err != nil {
		return models.Keyword{}, err
	}
	keyword.Enabled = intToBool(enabled)
	keyword.CreatedAt = parseSQLiteTime(createdAt)
	keyword.UpdatedAt = parseSQLiteTime(updatedAt)
	return keyword, nil
}

func scanKeywordTenderMatch(scanner interface {
	Scan(dest ...any) error
}) (models.KeywordTenderMatch, error) {
	var match models.KeywordTenderMatch
	var matchedKeywords string
	var refreshedAt, createdAt, updatedAt string
	err := scanner.Scan(
		&match.ID, &match.ProfileID, &match.TenantID, &match.UserID, &match.TenderID,
		&matchedKeywords, &match.MatchCount, &refreshedAt, &createdAt, &updatedAt,
	)
	if err != nil {
		return models.KeywordTenderMatch{}, err
	}
	match.MatchedKeywords = decodeKeywordList(matchedKeywords)
	match.RefreshedAt = parseSQLiteTime(refreshedAt)
	match.CreatedAt = parseSQLiteTime(createdAt)
	match.UpdatedAt = parseSQLiteTime(updatedAt)
	return match, nil
}

func (s *SQLiteStore) ensureKeywordProfile(ctx context.Context, tenantID, userID string) (models.KeywordProfile, error) {
	tenantID = strings.TrimSpace(tenantID)
	userID = strings.TrimSpace(userID)
	if tenantID == "" || userID == "" {
		return models.KeywordProfile{}, fmt.Errorf("keyword profile requires tenant_id and user_id")
	}
	profile, err := s.getKeywordProfile(ctx, tenantID, userID)
	if err == nil {
		return profile, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return models.KeywordProfile{}, err
	}
	now := time.Now().UTC()
	profile = models.KeywordProfile{
		ID:             newid(),
		TenantID:       tenantID,
		UserID:         userID,
		Name:           "Default keyword search",
		RefreshStatus:  "pending",
		RefreshMessage: "Add keywords or refresh to calculate matches.",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	_, err = s.db.ExecContext(ctx, `
		insert into keyword_profiles(id, tenant_id, user_id, name, refresh_status, refresh_message, match_count, last_refreshed_at, created_at, updated_at)
		values(?,?,?,?,?,?,?,?,?,?)
		on conflict(tenant_id, user_id) do nothing
	`, profile.ID, profile.TenantID, profile.UserID, profile.Name, profile.RefreshStatus, profile.RefreshMessage, profile.MatchCount, sqliteTimeString(profile.LastRefreshedAt), sqliteTimeString(profile.CreatedAt), sqliteTimeString(profile.UpdatedAt))
	if err != nil {
		return models.KeywordProfile{}, err
	}
	return s.getKeywordProfile(ctx, tenantID, userID)
}

func (s *SQLiteStore) getKeywordProfile(ctx context.Context, tenantID, userID string) (models.KeywordProfile, error) {
	row := s.db.QueryRowContext(ctx, `
		select id, tenant_id, user_id, name, refresh_status, refresh_message, match_count, last_refreshed_at, created_at, updated_at
		from keyword_profiles
		where tenant_id = ? and user_id = ?
		limit 1
	`, strings.TrimSpace(tenantID), strings.TrimSpace(userID))
	profile, err := scanKeywordProfile(row)
	if errors.Is(err, sql.ErrNoRows) {
		return models.KeywordProfile{}, ErrNotFound
	}
	return profile, err
}

func (s *SQLiteStore) GetKeywordProfile(ctx context.Context, tenantID, userID string) (models.KeywordProfile, error) {
	return s.ensureKeywordProfile(ctx, tenantID, userID)
}

func (s *SQLiteStore) ListKeywords(ctx context.Context, tenantID, userID string) ([]models.Keyword, error) {
	profile, err := s.ensureKeywordProfile(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		select id, profile_id, tenant_id, user_id, value, enabled, created_at, updated_at
		from keyword_records
		where profile_id = ? and tenant_id = ? and user_id = ?
		order by enabled desc, lower(value) asc, created_at asc
	`, profile.ID, profile.TenantID, profile.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Keyword{}
	for rows.Next() {
		keyword, err := scanKeyword(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, keyword)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) UpsertKeyword(ctx context.Context, keyword models.Keyword) (models.Keyword, error) {
	profile, err := s.ensureKeywordProfile(ctx, keyword.TenantID, keyword.UserID)
	if err != nil {
		return models.Keyword{}, err
	}
	keyword.Value = normalizeKeywordValue(keyword.Value)
	if keyword.Value == "" {
		return models.Keyword{}, fmt.Errorf("keyword value must not be empty")
	}
	now := time.Now().UTC()
	keyword.ProfileID = profile.ID
	keyword.TenantID = profile.TenantID
	keyword.UserID = profile.UserID
	if strings.TrimSpace(keyword.ID) != "" {
		existing, err := s.keywordByID(ctx, profile.TenantID, profile.UserID, keyword.ID)
		if err == nil {
			keyword.CreatedAt = existing.CreatedAt
		}
		if errors.Is(err, ErrNotFound) {
			return models.Keyword{}, ErrNotFound
		}
		if err != nil && !errors.Is(err, ErrNotFound) {
			return models.Keyword{}, err
		}
	}
	if keyword.ID == "" {
		keyword.ID = newid()
		keyword.CreatedAt = now
	}
	if keyword.CreatedAt.IsZero() {
		keyword.CreatedAt = now
	}
	keyword.UpdatedAt = now
	_, err = s.db.ExecContext(ctx, `
		insert into keyword_records(id, profile_id, tenant_id, user_id, value, enabled, created_at, updated_at)
		values(?,?,?,?,?,?,?,?)
		on conflict(id) do update set
			value=excluded.value,
			enabled=excluded.enabled,
			updated_at=excluded.updated_at
	`, keyword.ID, keyword.ProfileID, keyword.TenantID, keyword.UserID, keyword.Value, boolToInt(keyword.Enabled), sqliteTimeString(keyword.CreatedAt), sqliteTimeString(keyword.UpdatedAt))
	if err != nil {
		return models.Keyword{}, err
	}
	if _, err := s.RefreshKeywordMatches(ctx, profile.TenantID, profile.UserID); err != nil {
		return models.Keyword{}, err
	}
	return keyword, nil
}

func (s *SQLiteStore) keywordByID(ctx context.Context, tenantID, userID, id string) (models.Keyword, error) {
	row := s.db.QueryRowContext(ctx, `
		select id, profile_id, tenant_id, user_id, value, enabled, created_at, updated_at
		from keyword_records
		where id = ? and tenant_id = ? and user_id = ?
	`, strings.TrimSpace(id), strings.TrimSpace(tenantID), strings.TrimSpace(userID))
	keyword, err := scanKeyword(row)
	if errors.Is(err, sql.ErrNoRows) {
		return models.Keyword{}, ErrNotFound
	}
	return keyword, err
}

func (s *SQLiteStore) DeleteKeyword(ctx context.Context, tenantID, userID, id string) error {
	profile, err := s.ensureKeywordProfile(ctx, tenantID, userID)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		delete from keyword_records
		where id = ? and profile_id = ? and tenant_id = ? and user_id = ?
	`, strings.TrimSpace(id), profile.ID, profile.TenantID, profile.UserID)
	if err != nil {
		return err
	}
	_, err = s.RefreshKeywordMatches(ctx, profile.TenantID, profile.UserID)
	return err
}

func (s *SQLiteStore) activeKeywords(ctx context.Context, profile models.KeywordProfile) ([]models.Keyword, error) {
	rows, err := s.db.QueryContext(ctx, `
		select id, profile_id, tenant_id, user_id, value, enabled, created_at, updated_at
		from keyword_records
		where profile_id = ? and tenant_id = ? and user_id = ? and enabled = 1
		order by lower(value) asc
	`, profile.ID, profile.TenantID, profile.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Keyword{}
	for rows.Next() {
		keyword, err := scanKeyword(rows)
		if err != nil {
			return nil, err
		}
		if keywordComparable(keyword.Value) == "" {
			continue
		}
		out = append(out, keyword)
	}
	return out, rows.Err()
}

func keywordTenderText(t models.Tender) string {
	parts := []string{t.Title, t.Summary, t.Excerpt}
	appendFacts := func(facts map[string]string) {
		for _, value := range facts {
			parts = append(parts, value)
		}
	}
	appendFacts(t.PageFacts)
	appendFacts(t.DocumentFacts)
	appendFacts(t.ExtractedFacts)
	return strings.ToLower(strings.Join(parts, "\n"))
}

func matchedKeywordsForTender(t models.Tender, keywords []models.Keyword) []string {
	text := keywordTenderText(t)
	if strings.TrimSpace(text) == "" || len(keywords) == 0 {
		return nil
	}
	matches := []string{}
	seen := map[string]bool{}
	for _, keyword := range keywords {
		value := normalizeKeywordValue(keyword.Value)
		key := keywordComparable(value)
		if key == "" || seen[key] {
			continue
		}
		if strings.Contains(text, key) {
			seen[key] = true
			matches = append(matches, value)
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		return strings.ToLower(matches[i]) < strings.ToLower(matches[j])
	})
	return matches
}

func (s *SQLiteStore) upsertKeywordMatch(ctx context.Context, profile models.KeywordProfile, tenderID string, matches []string, refreshedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		insert into keyword_match_records(id, profile_id, tenant_id, user_id, tender_id, matched_keywords, match_count, refreshed_at, created_at, updated_at)
		values(?,?,?,?,?,?,?,?,?,?)
		on conflict(profile_id, tender_id) do update set
			matched_keywords=excluded.matched_keywords,
			match_count=excluded.match_count,
			refreshed_at=excluded.refreshed_at,
			updated_at=excluded.updated_at
	`, newid(), profile.ID, profile.TenantID, profile.UserID, tenderID, encodeKeywordList(matches), len(matches), sqliteTimeString(refreshedAt), sqliteTimeString(refreshedAt), sqliteTimeString(refreshedAt))
	return err
}

func (s *SQLiteStore) refreshKeywordProfileForTender(ctx context.Context, profile models.KeywordProfile, tender models.Tender, refreshedAt time.Time) error {
	keywords, err := s.activeKeywords(ctx, profile)
	if err != nil {
		return err
	}
	matches := matchedKeywordsForTender(tender, keywords)
	if len(matches) == 0 {
		_, err = s.db.ExecContext(ctx, `
			delete from keyword_match_records
			where profile_id = ? and tender_id = ?
		`, profile.ID, tender.ID)
		return err
	}
	return s.upsertKeywordMatch(ctx, profile, tender.ID, matches, refreshedAt)
}

func (s *SQLiteStore) refreshAllKeywordProfilesForTender(ctx context.Context, tender models.Tender) error {
	rows, err := s.db.QueryContext(ctx, `
		select id, tenant_id, user_id, name, refresh_status, refresh_message, match_count, last_refreshed_at, created_at, updated_at
		from keyword_profiles
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	profiles := []models.KeywordProfile{}
	for rows.Next() {
		profile, err := scanKeywordProfile(rows)
		if err != nil {
			return err
		}
		profiles = append(profiles, profile)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, profile := range profiles {
		if err := s.refreshKeywordProfileForTender(ctx, profile, tender, time.Now().UTC()); err != nil {
			return err
		}
		if err := s.updateKeywordProfileStats(ctx, profile, "success", "Updated after tender change.", false); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) refreshAllKeywordProfiles(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `
		select tenant_id, user_id
		from keyword_profiles
		order by tenant_id asc, user_id asc
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type owner struct{ tenantID, userID string }
	owners := []owner{}
	for rows.Next() {
		var item owner
		if err := rows.Scan(&item.tenantID, &item.userID); err != nil {
			return err
		}
		owners = append(owners, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range owners {
		if _, err := s.RefreshKeywordMatches(ctx, item.tenantID, item.userID); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) updateKeywordProfileStats(ctx context.Context, profile models.KeywordProfile, status, message string, updateRefreshedAt bool) error {
	var count int
	if err := s.db.QueryRowContext(ctx, `
		select count(*)
		from keyword_match_records
		where profile_id = ? and tenant_id = ? and user_id = ?
	`, profile.ID, profile.TenantID, profile.UserID).Scan(&count); err != nil {
		return err
	}
	now := time.Now().UTC()
	refreshedAt := profile.LastRefreshedAt
	if updateRefreshedAt {
		refreshedAt = now
	}
	if strings.TrimSpace(status) == "" {
		status = profile.RefreshStatus
	}
	if strings.TrimSpace(message) == "" {
		message = profile.RefreshMessage
	}
	_, err := s.db.ExecContext(ctx, `
		update keyword_profiles
		set refresh_status = ?, refresh_message = ?, match_count = ?, last_refreshed_at = ?, updated_at = ?
		where id = ? and tenant_id = ? and user_id = ?
	`, status, message, count, sqliteTimeString(refreshedAt), sqliteTimeString(now), profile.ID, profile.TenantID, profile.UserID)
	return err
}

func (s *SQLiteStore) RefreshKeywordMatches(ctx context.Context, tenantID, userID string) (models.KeywordSearchSummary, error) {
	profile, err := s.ensureKeywordProfile(ctx, tenantID, userID)
	if err != nil {
		return models.KeywordSearchSummary{}, err
	}
	now := time.Now().UTC()
	_, _ = s.db.ExecContext(ctx, `
		update keyword_profiles
		set refresh_status = ?, refresh_message = ?, updated_at = ?
		where id = ? and tenant_id = ? and user_id = ?
	`, "running", "Refreshing keyword matches.", sqliteTimeString(now), profile.ID, profile.TenantID, profile.UserID)

	keywords, err := s.activeKeywords(ctx, profile)
	if err != nil {
		_ = s.updateKeywordProfileStats(ctx, profile, "failed", err.Error(), true)
		return models.KeywordSearchSummary{}, err
	}
	if _, err := s.db.ExecContext(ctx, `
		delete from keyword_match_records
		where profile_id = ? and tenant_id = ? and user_id = ?
	`, profile.ID, profile.TenantID, profile.UserID); err != nil {
		_ = s.updateKeywordProfileStats(ctx, profile, "failed", err.Error(), true)
		return models.KeywordSearchSummary{}, err
	}
	tenders, err := s.listAllTenders(ctx)
	if err != nil {
		_ = s.updateKeywordProfileStats(ctx, profile, "failed", err.Error(), true)
		return models.KeywordSearchSummary{}, err
	}
	for _, tender := range tenders {
		matches := matchedKeywordsForTender(tender, keywords)
		if len(matches) == 0 {
			continue
		}
		if err := s.upsertKeywordMatch(ctx, profile, tender.ID, matches, now); err != nil {
			_ = s.updateKeywordProfileStats(ctx, profile, "failed", err.Error(), true)
			return models.KeywordSearchSummary{}, err
		}
	}
	message := "Keyword matches refreshed."
	if len(keywords) == 0 {
		message = "No active keywords to match."
	}
	if err := s.updateKeywordProfileStats(ctx, profile, "success", message, true); err != nil {
		return models.KeywordSearchSummary{}, err
	}
	return s.KeywordSearchSummary(ctx, profile.TenantID, profile.UserID)
}

func (s *SQLiteStore) ListKeywordTenderMatches(ctx context.Context, tenantID, userID string, filter KeywordMatchFilter) ([]models.KeywordTenderMatchResult, int, error) {
	profile, err := s.ensureKeywordProfile(ctx, tenantID, userID)
	if err != nil {
		return nil, 0, err
	}
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 {
		filter.PageSize = 20
	}
	if filter.PageSize > 100 {
		filter.PageSize = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		select id, profile_id, tenant_id, user_id, tender_id, matched_keywords, match_count, refreshed_at, created_at, updated_at
		from keyword_match_records
		where profile_id = ? and tenant_id = ? and user_id = ?
	`, profile.ID, profile.TenantID, profile.UserID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	matches := []models.KeywordTenderMatch{}
	tenderIDs := []string{}
	for rows.Next() {
		match, err := scanKeywordTenderMatch(rows)
		if err != nil {
			return nil, 0, err
		}
		matches = append(matches, match)
		tenderIDs = append(tenderIDs, match.TenderID)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	tenderByID, err := s.GetTendersByIDs(ctx, tenderIDs)
	if err != nil {
		return nil, 0, err
	}
	results := []models.KeywordTenderMatchResult{}
	for _, match := range matches {
		tender, ok := tenderByID[match.TenderID]
		if !ok {
			continue
		}
		if filter.Query != "" && !(ContainsCI(tender.Title, filter.Query) || ContainsCI(tender.Summary, filter.Query) || ContainsCI(tender.Excerpt, filter.Query)) {
			continue
		}
		if filter.Source != "" && tender.SourceKey != filter.Source {
			continue
		}
		if filter.Province != "" && !strings.EqualFold(tender.Province, filter.Province) {
			continue
		}
		if filter.Status != "" && !strings.EqualFold(tender.Status, filter.Status) {
			continue
		}
		if filter.Keyword != "" && !matchContainsKeyword(match, filter.Keyword) {
			continue
		}
		results = append(results, models.KeywordTenderMatchResult{Match: match, Tender: tender})
	}
	sort.Slice(results, func(i, j int) bool {
		switch filter.Sort {
		case "updated":
			return results[i].Match.UpdatedAt.After(results[j].Match.UpdatedAt)
		case "source":
			if strings.EqualFold(results[i].Tender.SourceKey, results[j].Tender.SourceKey) {
				return results[i].Tender.ID < results[j].Tender.ID
			}
			return strings.ToLower(results[i].Tender.SourceKey) < strings.ToLower(results[j].Tender.SourceKey)
		case "matches":
			if results[i].Match.MatchCount == results[j].Match.MatchCount {
				return results[i].Tender.ClosingDate < results[j].Tender.ClosingDate
			}
			return results[i].Match.MatchCount > results[j].Match.MatchCount
		default:
			if results[i].Tender.ClosingDate == results[j].Tender.ClosingDate {
				return results[i].Tender.ID < results[j].Tender.ID
			}
			return results[i].Tender.ClosingDate < results[j].Tender.ClosingDate
		}
	})
	total := len(results)
	start := (filter.Page - 1) * filter.PageSize
	if start > total {
		return []models.KeywordTenderMatchResult{}, total, nil
	}
	end := start + filter.PageSize
	if end > total {
		end = total
	}
	return results[start:end], total, nil
}

func matchContainsKeyword(match models.KeywordTenderMatch, keyword string) bool {
	target := keywordComparable(keyword)
	if target == "" {
		return true
	}
	for _, value := range match.MatchedKeywords {
		if keywordComparable(value) == target {
			return true
		}
	}
	return false
}

func (s *SQLiteStore) KeywordSearchSummary(ctx context.Context, tenantID, userID string) (models.KeywordSearchSummary, error) {
	profile, err := s.ensureKeywordProfile(ctx, tenantID, userID)
	if err != nil {
		return models.KeywordSearchSummary{}, err
	}
	keywords, err := s.ListKeywords(ctx, profile.TenantID, profile.UserID)
	if err != nil {
		return models.KeywordSearchSummary{}, err
	}
	active := 0
	for _, keyword := range keywords {
		if keyword.Enabled {
			active++
		}
	}
	var matched int
	if err := s.db.QueryRowContext(ctx, `
		select count(*)
		from keyword_match_records
		where profile_id = ? and tenant_id = ? and user_id = ?
	`, profile.ID, profile.TenantID, profile.UserID).Scan(&matched); err != nil {
		return models.KeywordSearchSummary{}, err
	}
	return models.KeywordSearchSummary{
		Profile:            profile,
		TotalKeywordCount:  len(keywords),
		ActiveKeywordCount: active,
		MatchedTenderCount: matched,
		LastRefreshedAt:    profile.LastRefreshedAt,
		RefreshStatus:      profile.RefreshStatus,
		RefreshMessage:     profile.RefreshMessage,
	}, nil
}
