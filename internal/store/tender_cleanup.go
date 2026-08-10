package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"strings"
	"time"

	"openbid/internal/models"
	"openbid/internal/tenderstate"
)

const (
	expiredTenderArchiveReason = "expired_tender_cleanup"
	staleTenderArchiveReason   = "stale_tender_cleanup"
	tenderMaximumAge           = 30 * 24 * time.Hour
)

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

func tenderExpiredAt(t models.Tender, now time.Time) bool {
	return tenderstate.IsExpired(t, now)
}

func tenderStoredBefore(t models.Tender, cutoff time.Time) bool {
	storedAt := t.CreatedAt
	if storedAt.IsZero() {
		// Older databases did not populate CreatedAt for source-provided IDs.
		// UpdatedAt is the best available conservative fallback for those rows.
		storedAt = t.UpdatedAt
	}
	return !storedAt.IsZero() && storedAt.Before(cutoff)
}

func (s *SQLiteStore) CleanupExpiredTenders(ctx context.Context, now time.Time) (models.ExpiredTenderCleanupResult, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	archiveAt := now.UTC()
	staleBefore := now.Add(-tenderMaximumAge)
	tenders, err := s.listAllTenders(ctx)
	if err != nil {
		return models.ExpiredTenderCleanupResult{}, err
	}
	expired := make([]models.Tender, 0)
	for _, tender := range tenders {
		if tenderArchived(tender) {
			continue
		}
		isExpired := tenderExpiredAt(tender, now)
		stale := tenderStoredBefore(tender, staleBefore)
		if !isExpired && !stale {
			continue
		}
		tender.ArchivedAt = archiveAt
		tender.ArchiveReason = expiredTenderArchiveReason
		if !isExpired && stale {
			tender.ArchiveReason = staleTenderArchiveReason
		}
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
		if err = upsertTenderDashboardIndex(ctx, tx, tender); err != nil {
			return models.ExpiredTenderCleanupResult{}, err
		}
		if err = upsertTenderFilterIndex(ctx, tx, tender); err != nil {
			return models.ExpiredTenderCleanupResult{}, err
		}
		result.RemovedTenderIDs = append(result.RemovedTenderIDs, tender.ID)
	}
	if err = cleanupArchivedTenderLinks(ctx, tx); err != nil {
		return models.ExpiredTenderCleanupResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return models.ExpiredTenderCleanupResult{}, err
	}
	log.Printf("expired or stale tender cleanup archived %d tenders", result.RemovedCount)
	return result, nil
}

func cleanupArchivedTenderLinks(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `
		delete from jobs
		where exists (
			select 1 from tender_filter_index ti
			where ti.tender_id = coalesce(json_extract(jobs.payload, '$.TenderID'), '')
			  and ti.archived = 1
		)
	`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		delete from keyword_match_records
		where exists (
			select 1 from tender_filter_index ti
			where ti.tender_id = keyword_match_records.tender_id
			  and ti.archived = 1
		)
	`); err != nil {
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
