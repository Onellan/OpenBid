package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"strings"
	"time"

	"openbid/internal/models"
)

const expiredTenderArchiveReason = "expired_tender_cleanup"

func activeTenderSQLClause(alias string) string {
	prefix := ""
	if strings.TrimSpace(alias) != "" {
		prefix = alias + "."
	}
	return "(coalesce(json_extract(" + prefix + "payload, '$.ArchivedAt'), '') in ('', '0001-01-01T00:00:00Z'))"
}

func tenderArchived(t models.Tender) bool {
	return !t.ArchivedAt.IsZero()
}

func parseTenderClosingTime(value string, now time.Time) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	location := now.Location()
	formats := []struct {
		layout   string
		dateOnly bool
	}{
		{time.RFC3339Nano, false},
		{time.RFC3339, false},
		{"2006-01-02 15:04:05", false},
		{"2006-01-02 15:04", false},
		{"2006/01/02 15:04:05", false},
		{"2006/01/02 15:04", false},
		{"02/01/2006 15:04:05", false},
		{"02/01/2006 15:04", false},
		{"2006-01-02", true},
		{"2006/01/02", true},
		{"02/01/2006", true},
		{"02 Jan 2006", true},
		{"02 January 2006", true},
	}
	for _, format := range formats {
		parsed, err := time.ParseInLocation(format.layout, value, location)
		if err != nil {
			continue
		}
		if format.dateOnly {
			parsed = time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 23, 59, 59, 0, location)
		}
		return parsed, true
	}
	return time.Time{}, false
}

func tenderExpiredAt(t models.Tender, now time.Time) bool {
	closingAt, ok := parseTenderClosingTime(t.ClosingDate, now)
	if !ok {
		return false
	}
	return !closingAt.After(now)
}

func (s *SQLiteStore) CleanupExpiredTenders(ctx context.Context, now time.Time) (models.ExpiredTenderCleanupResult, error) {
	if now.IsZero() {
		now = time.Now()
	}
	archiveAt := now.UTC()
	tenders, err := s.listAllTenders(ctx)
	if err != nil {
		return models.ExpiredTenderCleanupResult{}, err
	}
	expired := make([]models.Tender, 0)
	for _, tender := range tenders {
		if tenderArchived(tender) || !tenderExpiredAt(tender, now) {
			continue
		}
		tender.ArchivedAt = archiveAt
		tender.ArchiveReason = expiredTenderArchiveReason
		tender.UpdatedAt = archiveAt
		expired = append(expired, tender)
	}
	result := models.ExpiredTenderCleanupResult{
		RemovedCount:     len(expired),
		RemovedTenderIDs: make([]string, 0, len(expired)),
		RunAt:            archiveAt,
	}
	if len(expired) == 0 {
		return result, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return models.ExpiredTenderCleanupResult{}, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	for _, tender := range expired {
		if err = sqliteUpsertJSONExec(ctx, tx, "tenders", tender.ID, tender); err != nil {
			return models.ExpiredTenderCleanupResult{}, err
		}
		result.RemovedTenderIDs = append(result.RemovedTenderIDs, tender.ID)
		if err = cleanupVolatileTenderLinks(ctx, tx, tender.ID); err != nil {
			return models.ExpiredTenderCleanupResult{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return models.ExpiredTenderCleanupResult{}, err
	}
	log.Printf("expired tender cleanup archived %d tenders: %s", result.RemovedCount, strings.Join(result.RemovedTenderIDs, ","))
	return result, nil
}

func cleanupVolatileTenderLinks(ctx context.Context, tx *sql.Tx, tenderID string) error {
	if _, err := tx.ExecContext(ctx, "delete from jobs where coalesce(json_extract(payload, '$.TenderID'), '') = ?", tenderID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "delete from keyword_match_records where tender_id = ?", tenderID); err != nil {
		return err
	}
	return nil
}

func decodeTenderPayload(raw string) (models.Tender, error) {
	var tender models.Tender
	if err := json.Unmarshal([]byte(raw), &tender); err != nil {
		return models.Tender{}, err
	}
	return tender, nil
}
