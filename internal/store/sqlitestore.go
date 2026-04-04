package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"openbid/internal/models"
)

const currentSchemaVersion = 6

type SQLiteStore struct {
	db   *sql.DB
	path string
}

func NewSQLiteStore(path string) (*SQLiteStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)", filepath.ToSlash(path))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	s := &SQLiteStore{db: db, path: path}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLiteStore) Close() error { return s.db.Close() }
func (s *SQLiteStore) Path() string { return s.path }

func (s *SQLiteStore) BackupTo(ctx context.Context, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, "pragma wal_checkpoint(full);"); err != nil {
		return err
	}
	src, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, src, 0o600)
}

func (s *SQLiteStore) ValidateRuntime(ctx context.Context) error {
	var userVersion int
	if err := s.db.QueryRowContext(ctx, "pragma user_version;").Scan(&userVersion); err != nil {
		return err
	}
	if userVersion != currentSchemaVersion {
		return fmt.Errorf("unexpected schema version: got %d want %d", userVersion, currentSchemaVersion)
	}
	for _, table := range []string{"tenders", "tenants", "sync_runs", "source_configs", "source_schedule_settings", "jobs", "source_health", "audit_entries", "workflow_events", "user_records", "membership_records", "workflow_records", "bookmark_records", "saved_search_records", "sessions", "tenant_source_assignments"} {
		var count int
		if err := s.db.QueryRowContext(ctx, "select count(*) from sqlite_master where type='table' and name=?", table).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("%s table missing", table)
		}
	}
	var storedSchemaVersion string
	if err := s.db.QueryRowContext(ctx, "select value from schema_meta where key='schema_version'").Scan(&storedSchemaVersion); err != nil {
		return err
	}
	if storedSchemaVersion != strconv.Itoa(currentSchemaVersion) {
		return fmt.Errorf("schema_meta version mismatch: got %s want %d", storedSchemaVersion, currentSchemaVersion)
	}
	return nil
}

func (s *SQLiteStore) migrate(ctx context.Context) error {
	var currentVersion int
	if err := s.db.QueryRowContext(ctx, "pragma user_version;").Scan(&currentVersion); err != nil {
		return err
	}
	if currentVersion > currentSchemaVersion {
		return fmt.Errorf("database schema version %d is newer than this binary supports (%d)", currentVersion, currentSchemaVersion)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmts := []string{
		`create table if not exists schema_meta (key text primary key, value text not null);`,
		`create table if not exists tenders (id text primary key, payload text not null);`,
		`create table if not exists users (id text primary key, payload text not null);`,
		`create table if not exists tenants (id text primary key, payload text not null);`,
		`create table if not exists memberships (id text primary key, payload text not null);`,
		`create table if not exists workflows (id text primary key, payload text not null);`,
		`create table if not exists bookmarks (id text primary key, payload text not null);`,
		`create table if not exists saved_searches (id text primary key, payload text not null);`,
		`create table if not exists sync_runs (id text primary key, payload text not null);`,
		`create table if not exists source_configs (id text primary key, payload text not null);`,
		`create table if not exists source_schedule_settings (id text primary key, payload text not null);`,
		`create table if not exists jobs (id text primary key, payload text not null);`,
		`create table if not exists source_health (id text primary key, payload text not null);`,
		`create table if not exists audit_entries (id text primary key, payload text not null);`,
		`create table if not exists workflow_events (id text primary key, payload text not null);`,
		`create index if not exists idx_users_username on users(lower(json_extract(payload, '$.Username')));`,
		`create index if not exists idx_tenders_source on tenders(coalesce(json_extract(payload, '$.SourceKey'), ''));`,
		`create index if not exists idx_tenders_status on tenders(lower(coalesce(json_extract(payload, '$.Status'), '')));`,
		`create index if not exists idx_tenders_document_status on tenders(coalesce(json_extract(payload, '$.DocumentStatus'), ''));`,
		`create index if not exists idx_tenders_published on tenders(coalesce(json_extract(payload, '$.PublishedDate'), ''));`,
		`create index if not exists idx_tenders_closing on tenders(coalesce(json_extract(payload, '$.ClosingDate'), ''));`,
	}
	for _, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if err := s.migrateRelationalTables(ctx, tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`pragma user_version = %d;`, currentSchemaVersion)); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`insert into schema_meta(key,value) values('schema_version','%d') on conflict(key) do update set value='%d';`, currentSchemaVersion, currentSchemaVersion)); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func sqliteCountRows(ctx context.Context, db *sql.DB, table string) (int, error) {
	var count int
	if err := db.QueryRowContext(ctx, "select count(*) from "+table).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func fileSizeOrZero(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func (s *SQLiteStore) RuntimeStats(ctx context.Context) (RuntimeStats, error) {
	stats := RuntimeStats{
		Path:                  s.path,
		ExpectedSchemaVersion: currentSchemaVersion,
		SizeBytes:             fileSizeOrZero(s.path),
		WALSizeBytes:          fileSizeOrZero(s.path + "-wal"),
		SHMSizeBytes:          fileSizeOrZero(s.path + "-shm"),
	}
	if err := s.db.QueryRowContext(ctx, "pragma user_version;").Scan(&stats.SchemaVersion); err != nil {
		return stats, err
	}
	if err := s.db.QueryRowContext(ctx, "pragma journal_mode;").Scan(&stats.JournalMode); err != nil {
		return stats, err
	}
	if err := s.db.QueryRowContext(ctx, "pragma quick_check(1);").Scan(&stats.QuickCheck); err != nil {
		return stats, err
	}
	counts := []struct {
		table  string
		target *int
	}{
		{"tenders", &stats.TenderCount},
		{"user_records", &stats.UserCount},
		{"tenants", &stats.TenantCount},
		{"membership_records", &stats.MembershipCount},
		{"workflow_records", &stats.WorkflowCount},
		{"bookmark_records", &stats.BookmarkCount},
		{"saved_search_records", &stats.SavedSearchCount},
		{"sync_runs", &stats.SyncRunCount},
		{"source_configs", &stats.SourceConfigCount},
		{"source_health", &stats.SourceHealthCount},
		{"tenant_source_assignments", &stats.TenantSourceCount},
		{"jobs", &stats.JobCount},
		{"audit_entries", &stats.AuditCount},
		{"workflow_events", &stats.WorkflowEventCount},
	}
	for _, item := range counts {
		count, err := sqliteCountRows(ctx, s.db, item.table)
		if err != nil {
			return stats, err
		}
		*item.target = count
	}
	return stats, nil
}

func sqliteUpsertJSON[T any](ctx context.Context, db *sql.DB, table, id string, v T) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, "insert into "+table+"(id,payload) values(?,?) on conflict(id) do update set payload=excluded.payload", id, string(b))
	return err
}

func sqliteGetJSON[T any](ctx context.Context, db *sql.DB, table, id string) (T, error) {
	var raw string
	var zero T
	err := db.QueryRowContext(ctx, "select payload from "+table+" where id = ?", id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return zero, ErrNotFound
	}
	if err != nil {
		return zero, err
	}
	if err := json.Unmarshal([]byte(raw), &zero); err != nil {
		return zero, err
	}
	return zero, nil
}

func sqliteListJSON[T any](ctx context.Context, db *sql.DB, table string) ([]T, error) {
	rows, err := db.QueryContext(ctx, "select payload from "+table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var v T
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func sqliteDelete(ctx context.Context, db *sql.DB, table, id string) error {
	_, err := db.ExecContext(ctx, "delete from "+table+" where id = ?", id)
	return err
}

func placeholders(count int) string {
	if count <= 0 {
		return ""
	}
	return strings.TrimRight(strings.Repeat("?,", count), ",")
}

func uniqueTrimmed(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func (s *SQLiteStore) listAllTenders(ctx context.Context) ([]models.Tender, error) {
	return sqliteListJSON[models.Tender](ctx, s.db, "tenders")
}

func (s *SQLiteStore) GetTendersByIDs(ctx context.Context, ids []string) (map[string]models.Tender, error) {
	out := map[string]models.Tender{}
	ordered := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ordered = append(ordered, id)
	}
	if len(ordered) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(ordered))
	for _, id := range ordered {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx, "select payload from tenders where id in ("+placeholders(len(args))+")", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var tender models.Tender
		if err := json.Unmarshal([]byte(raw), &tender); err != nil {
			return nil, err
		}
		out[tender.ID] = tender
	}
	return out, rows.Err()
}

func (s *SQLiteStore) ListTenders(ctx context.Context, f ListFilter) ([]models.Tender, int, error) {
	f = NormalizeFilter(f)
	items, total, err := s.listTendersSQL(ctx, f)
	if err == nil {
		return items, total, nil
	}
	return s.listTendersInMemory(ctx, f)
}

func (s *SQLiteStore) TenderFilterOptions(ctx context.Context, tenantID string) (TenderFilterOptions, error) {
	rows, err := s.db.QueryContext(ctx, `
		select
			trim(coalesce(json_extract(payload, '$.SourceKey'), '')),
			trim(coalesce(json_extract(payload, '$.Province'), '')),
			trim(coalesce(json_extract(payload, '$.Status'), '')),
			trim(coalesce(json_extract(payload, '$.Category'), '')),
			trim(coalesce(json_extract(payload, '$.Issuer'), '')),
			trim(coalesce(json_extract(payload, '$.CIDBGrading'), '')),
			trim(coalesce(json_extract(payload, '$.DocumentStatus'), ''))
		from tenders
	`)
	if err != nil {
		return TenderFilterOptions{}, err
	}
	defer rows.Close()

	sourceKeySet := map[string]struct{}{}
	provinceSet := map[string]struct{}{}
	statusSet := map[string]struct{}{}
	categorySet := map[string]struct{}{}
	issuerSet := map[string]struct{}{}
	cidbSet := map[string]struct{}{}
	documentStatusSet := map[string]struct{}{}
	for rows.Next() {
		var sourceKey, province, status, category, issuer, cidb, documentStatus string
		if err := rows.Scan(&sourceKey, &province, &status, &category, &issuer, &cidb, &documentStatus); err != nil {
			return TenderFilterOptions{}, err
		}
		addDistinctValue(sourceKeySet, sourceKey)
		addDistinctValue(provinceSet, province)
		addDistinctValue(statusSet, status)
		addDistinctValue(categorySet, category)
		addDistinctValue(issuerSet, issuer)
		addDistinctValue(cidbSet, cidb)
		addDistinctValue(documentStatusSet, documentStatus)
	}
	if err := rows.Err(); err != nil {
		return TenderFilterOptions{}, err
	}

	sourceConfigs, err := s.ListSourceConfigs(ctx)
	if err != nil {
		return TenderFilterOptions{}, err
	}
	sourceLabels := map[string]string{}
	for _, cfg := range sourceConfigs {
		key := strings.TrimSpace(cfg.Key)
		if key == "" {
			continue
		}
		label := strings.TrimSpace(cfg.Name)
		if label == "" {
			label = key
		}
		sourceLabels[key] = label
	}
	sourceKeys := sortedDistinctValues(sourceKeySet)
	sources := make([]NamedValue, 0, len(sourceKeys))
	for _, key := range sourceKeys {
		label := sourceLabels[key]
		if label == "" {
			label = key
		}
		sources = append(sources, NamedValue{Value: key, Label: label})
	}
	sort.Slice(sources, func(i, j int) bool {
		if strings.EqualFold(sources[i].Label, sources[j].Label) {
			return sources[i].Value < sources[j].Value
		}
		return strings.ToLower(sources[i].Label) < strings.ToLower(sources[j].Label)
	})

	workflowStatuses := []string{}
	if strings.TrimSpace(tenantID) != "" {
		workflowStatuses, err = s.distinctWorkflowStatuses(ctx, tenantID)
		if err != nil {
			return TenderFilterOptions{}, err
		}
	}

	return TenderFilterOptions{
		Sources:        sources,
		Provinces:      sortedDistinctValues(provinceSet),
		Statuses:       sortedDistinctValues(statusSet),
		Categories:     sortedDistinctValues(categorySet),
		Issuers:        sortedDistinctValues(issuerSet),
		CIDBGradings:   sortedDistinctValues(cidbSet),
		WorkflowStatus: workflowStatuses,
		DocumentStatus: sortedDistinctValues(documentStatusSet),
	}, nil
}

func addDistinctValue(values map[string]struct{}, value string) {
	value = strings.TrimSpace(value)
	if value != "" {
		values[value] = struct{}{}
	}
}

func sortedDistinctValues(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool {
		if strings.EqualFold(out[i], out[j]) {
			return out[i] < out[j]
		}
		return strings.ToLower(out[i]) < strings.ToLower(out[j])
	})
	return out
}

func (s *SQLiteStore) distinctWorkflowStatuses(ctx context.Context, tenantID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		select distinct trim(status)
		from workflow_records
		where tenant_id = ? and trim(status) <> ''
		order by lower(trim(status)) asc
	`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []string{}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		values = append(values, strings.TrimSpace(value))
	}
	return values, rows.Err()
}

func (s *SQLiteStore) listTendersInMemory(ctx context.Context, f ListFilter) ([]models.Tender, int, error) {
	items, err := s.listAllTenders(ctx)
	if err != nil {
		return nil, 0, err
	}

	bookmarked := map[string]bool{}
	if f.TenantID != "" && f.UserID != "" {
		bms, _ := s.ListBookmarks(ctx, f.TenantID, f.UserID)
		for _, b := range bms {
			bookmarked[b.TenderID] = true
		}
	}
	workflowByTender := map[string]models.Workflow{}
	if f.TenantID != "" {
		wfs, _ := s.ListWorkflows(ctx, f.TenantID)
		for _, wf := range wfs {
			workflowByTender[wf.TenderID] = wf
		}
	}

	out := []models.Tender{}
	for _, t := range items {
		if f.Query != "" && !(ContainsCI(t.Title, f.Query) || ContainsCI(t.Issuer, f.Query) || ContainsCI(t.Summary, f.Query) || ContainsCI(t.TenderNumber, f.Query)) {
			continue
		}
		if f.Source != "" && t.SourceKey != f.Source {
			continue
		}
		if f.Province != "" && !strings.EqualFold(t.Province, f.Province) {
			continue
		}
		if f.Category != "" && !strings.EqualFold(t.Category, f.Category) {
			continue
		}
		if f.Issuer != "" && !ContainsCI(t.Issuer, f.Issuer) {
			continue
		}
		if f.Status != "" && !strings.EqualFold(t.Status, f.Status) {
			continue
		}
		if f.CIDB != "" && !ContainsCI(t.CIDBGrading, f.CIDB) {
			continue
		}
		if f.DocumentStatus != "" && string(t.DocumentStatus) != f.DocumentStatus {
			continue
		}
		if f.WorkflowStatus != "" {
			wf, ok := workflowByTender[t.ID]
			if !ok || !strings.EqualFold(wf.Status, f.WorkflowStatus) {
				continue
			}
		}
		if f.BookmarkedOnly && !bookmarked[t.ID] {
			continue
		}
		if f.HasDocuments && t.DocumentURL == "" {
			continue
		}
		out = append(out, t)
	}

	sort.Slice(out, func(i, j int) bool {
		switch f.Sort {
		case "source":
			return strings.ToLower(out[i].SourceKey) < strings.ToLower(out[j].SourceKey)
		case "province":
			return strings.ToLower(out[i].Province) < strings.ToLower(out[j].Province)
		case "status":
			return strings.ToLower(out[i].Status) < strings.ToLower(out[j].Status)
		case "category":
			return strings.ToLower(out[i].Category) < strings.ToLower(out[j].Category)
		case "issuer":
			return strings.ToLower(out[i].Issuer) < strings.ToLower(out[j].Issuer)
		case "published_date":
			return out[i].PublishedDate > out[j].PublishedDate
		case "relevance":
			return out[i].RelevanceScore > out[j].RelevanceScore
		case "cidb":
			return strings.ToLower(out[i].CIDBGrading) < strings.ToLower(out[j].CIDBGrading)
		case "workflow_status":
			return strings.ToLower(workflowByTender[out[i].ID].Status) < strings.ToLower(workflowByTender[out[j].ID].Status)
		case "document_status":
			return strings.ToLower(string(out[i].DocumentStatus)) < strings.ToLower(string(out[j].DocumentStatus))
		default:
			return out[i].ClosingDate < out[j].ClosingDate
		}
	})
	total := len(out)
	start := (f.Page - 1) * f.PageSize
	if start > total {
		return []models.Tender{}, total, nil
	}
	end := start + f.PageSize
	if end > total {
		end = total
	}
	return out[start:end], total, nil
}

func tenderOrderClause(f ListFilter) string {
	switch f.Sort {
	case "source":
		return "lower(coalesce(json_extract(t.payload, '$.SourceKey'), '')) asc, t.id asc"
	case "province":
		return "lower(coalesce(json_extract(t.payload, '$.Province'), '')) asc, t.id asc"
	case "status":
		return "lower(coalesce(json_extract(t.payload, '$.Status'), '')) asc, t.id asc"
	case "category":
		return "lower(coalesce(json_extract(t.payload, '$.Category'), '')) asc, t.id asc"
	case "issuer":
		return "lower(coalesce(json_extract(t.payload, '$.Issuer'), '')) asc, t.id asc"
	case "published_date":
		return "coalesce(json_extract(t.payload, '$.PublishedDate'), '') desc, t.id asc"
	case "relevance":
		return "coalesce(json_extract(t.payload, '$.RelevanceScore'), 0) desc, t.id asc"
	case "cidb":
		return "lower(coalesce(json_extract(t.payload, '$.CIDBGrading'), '')) asc, t.id asc"
	case "workflow_status":
		if strings.TrimSpace(f.TenantID) == "" {
			return "coalesce(json_extract(t.payload, '$.ClosingDate'), '') asc, t.id asc"
		}
		return "lower(coalesce(w.status, '')) asc, t.id asc"
	case "document_status":
		return "lower(coalesce(json_extract(t.payload, '$.DocumentStatus'), '')) asc, t.id asc"
	default:
		return "coalesce(json_extract(t.payload, '$.ClosingDate'), '') asc, t.id asc"
	}
}

func buildTenderSQLFilter(f ListFilter) (string, []any, []string) {
	clauses := []string{"1=1"}
	joins := []string{}
	joinArgs := []any{}
	whereArgs := []any{}
	if f.Query != "" {
		term := "%" + strings.ToLower(f.Query) + "%"
		clauses = append(clauses, "(lower(coalesce(json_extract(t.payload, '$.Title'), '')) like ? or lower(coalesce(json_extract(t.payload, '$.Issuer'), '')) like ? or lower(coalesce(json_extract(t.payload, '$.Summary'), '')) like ? or lower(coalesce(json_extract(t.payload, '$.TenderNumber'), '')) like ?)")
		whereArgs = append(whereArgs, term, term, term, term)
	}
	if f.Source != "" {
		clauses = append(clauses, "coalesce(json_extract(t.payload, '$.SourceKey'), '') = ?")
		whereArgs = append(whereArgs, f.Source)
	}
	if f.Province != "" {
		clauses = append(clauses, "lower(coalesce(json_extract(t.payload, '$.Province'), '')) = ?")
		whereArgs = append(whereArgs, strings.ToLower(f.Province))
	}
	if f.Category != "" {
		clauses = append(clauses, "lower(coalesce(json_extract(t.payload, '$.Category'), '')) = ?")
		whereArgs = append(whereArgs, strings.ToLower(f.Category))
	}
	if f.Issuer != "" {
		clauses = append(clauses, "lower(coalesce(json_extract(t.payload, '$.Issuer'), '')) like ?")
		whereArgs = append(whereArgs, "%"+strings.ToLower(f.Issuer)+"%")
	}
	if f.Status != "" {
		clauses = append(clauses, "lower(coalesce(json_extract(t.payload, '$.Status'), '')) = ?")
		whereArgs = append(whereArgs, strings.ToLower(f.Status))
	}
	if f.CIDB != "" {
		clauses = append(clauses, "lower(coalesce(json_extract(t.payload, '$.CIDBGrading'), '')) like ?")
		whereArgs = append(whereArgs, "%"+strings.ToLower(f.CIDB)+"%")
	}
	if f.DocumentStatus != "" {
		clauses = append(clauses, "coalesce(json_extract(t.payload, '$.DocumentStatus'), '') = ?")
		whereArgs = append(whereArgs, f.DocumentStatus)
	}
	if f.HasDocuments {
		clauses = append(clauses, "coalesce(json_extract(t.payload, '$.DocumentURL'), '') <> ''")
	}
	if f.WorkflowStatus != "" {
		if strings.TrimSpace(f.TenantID) == "" {
			clauses = append(clauses, "1=0")
		} else {
			joins = append(joins, "join workflow_records w on w.tenant_id = ? and w.tender_id = t.id")
			joinArgs = append(joinArgs, f.TenantID)
			clauses = append(clauses, "lower(w.status) = ?")
			whereArgs = append(whereArgs, strings.ToLower(f.WorkflowStatus))
		}
	}
	if f.BookmarkedOnly {
		if strings.TrimSpace(f.TenantID) == "" || strings.TrimSpace(f.UserID) == "" {
			clauses = append(clauses, "1=0")
		} else {
			joins = append(joins, "join bookmark_records b on b.tenant_id = ? and b.user_id = ? and b.tender_id = t.id")
			joinArgs = append(joinArgs, f.TenantID, f.UserID)
		}
	}
	if f.Sort == "workflow_status" && f.WorkflowStatus == "" && strings.TrimSpace(f.TenantID) != "" {
		joins = append(joins, "left join workflow_records w on w.tenant_id = ? and w.tender_id = t.id")
		joinArgs = append(joinArgs, f.TenantID)
	}
	return strings.Join(clauses, " and "), append(joinArgs, whereArgs...), joins
}

func (s *SQLiteStore) listTendersSQL(ctx context.Context, f ListFilter) ([]models.Tender, int, error) {
	whereClause, args, joins := buildTenderSQLFilter(f)
	fromClause := " from tenders t "
	if len(joins) > 0 {
		fromClause += strings.Join(joins, " ") + " "
	}
	countQuery := "select count(*)" + fromClause + "where " + whereClause
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	offset := (f.Page - 1) * f.PageSize
	if offset >= total {
		return []models.Tender{}, total, nil
	}
	queryArgs := append(append([]any{}, args...), f.PageSize, offset)
	rows, err := s.db.QueryContext(ctx, "select t.payload"+fromClause+"where "+whereClause+" order by "+tenderOrderClause(f)+" limit ? offset ?", queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]models.Tender, 0, f.PageSize)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, 0, err
		}
		var tender models.Tender
		if err := json.Unmarshal([]byte(raw), &tender); err != nil {
			return nil, 0, err
		}
		out = append(out, tender)
	}
	return out, total, rows.Err()
}

func (s *SQLiteStore) GetTender(ctx context.Context, id string) (models.Tender, error) {
	return sqliteGetJSON[models.Tender](ctx, s.db, "tenders", id)
}
func (s *SQLiteStore) UpsertTender(ctx context.Context, v models.Tender) error {
	now := time.Now().UTC()
	if v.ID == "" {
		v.ID = newid()
		v.CreatedAt = now
	}
	v.UpdatedAt = now
	if v.ExtractedFacts == nil {
		v.ExtractedFacts = map[string]string{}
	}
	return sqliteUpsertJSON(ctx, s.db, "tenders", v.ID, v)
}
func (s *SQLiteStore) ListUsers(ctx context.Context) ([]models.User, error) {
	rows, err := s.db.QueryContext(ctx, `
		select id, username, display_name, email, platform_role, password_hash, password_salt, mfa_secret, is_active, mfa_enabled,
		       failed_logins, session_version, locked_until, recovery_codes, created_at, updated_at
		from user_records
		order by username asc
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.User{}
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, user)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
func (s *SQLiteStore) ListUsersByIDs(ctx context.Context, userIDs []string) ([]models.User, error) {
	ids := uniqueTrimmed(userIDs)
	if len(ids) == 0 {
		return nil, nil
	}
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx, `
		select id, username, display_name, email, platform_role, password_hash, password_salt, mfa_secret, is_active, mfa_enabled,
		       failed_logins, session_version, locked_until, recovery_codes, created_at, updated_at
		from user_records
		where id in (`+placeholders(len(ids))+`)
		order by username asc
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.User{}
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, user)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
func (s *SQLiteStore) GetUserByUsername(ctx context.Context, username string) (models.User, error) {
	username = strings.TrimSpace(strings.ToLower(username))
	if username == "" {
		return models.User{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `
		select id, username, display_name, email, platform_role, password_hash, password_salt, mfa_secret, is_active, mfa_enabled,
		       failed_logins, session_version, locked_until, recovery_codes, created_at, updated_at
		from user_records
		where lower(username) = ?
		limit 1
	`, username)
	user, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return models.User{}, ErrNotFound
	}
	return user, err
}
func (s *SQLiteStore) GetUserByEmail(ctx context.Context, email string) (models.User, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		return models.User{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `
		select id, username, display_name, email, platform_role, password_hash, password_salt, mfa_secret, is_active, mfa_enabled,
		       failed_logins, session_version, locked_until, recovery_codes, created_at, updated_at
		from user_records
		where lower(email) = ?
		limit 1
	`, email)
	user, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return models.User{}, ErrNotFound
	}
	return user, err
}
func (s *SQLiteStore) GetUser(ctx context.Context, id string) (models.User, error) {
	row := s.db.QueryRowContext(ctx, `
		select id, username, display_name, email, platform_role, password_hash, password_salt, mfa_secret, is_active, mfa_enabled,
		       failed_logins, session_version, locked_until, recovery_codes, created_at, updated_at
		from user_records
		where id = ?
	`, id)
	user, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return models.User{}, ErrNotFound
	}
	return user, err
}
func (s *SQLiteStore) UpsertUser(ctx context.Context, v models.User) error {
	now := time.Now().UTC()
	if v.ID == "" {
		v.ID = newid()
		v.CreatedAt = now
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = now
	}
	v.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
		insert into user_records(
			id, username, display_name, email, platform_role, password_hash, password_salt, mfa_secret, is_active, mfa_enabled,
			failed_logins, session_version, locked_until, recovery_codes, created_at, updated_at
		) values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		on conflict(id) do update set
			username=excluded.username,
			display_name=excluded.display_name,
			email=excluded.email,
			platform_role=excluded.platform_role,
			password_hash=excluded.password_hash,
			password_salt=excluded.password_salt,
			mfa_secret=excluded.mfa_secret,
			is_active=excluded.is_active,
			mfa_enabled=excluded.mfa_enabled,
			failed_logins=excluded.failed_logins,
			session_version=excluded.session_version,
			locked_until=excluded.locked_until,
			recovery_codes=excluded.recovery_codes,
			updated_at=excluded.updated_at
	`,
		v.ID, v.Username, v.DisplayName, v.Email, string(v.PlatformRole), v.PasswordHash, v.PasswordSalt, v.MFASecret, boolToInt(v.IsActive), boolToInt(v.MFAEnabled),
		v.FailedLogins, v.SessionVersion, sqliteTimeString(v.LockedUntil), encodeStringSlice(v.RecoveryCodes), sqliteTimeString(v.CreatedAt), sqliteTimeString(v.UpdatedAt),
	)
	return err
}
func (s *SQLiteStore) ListTenants(ctx context.Context) ([]models.Tenant, error) {
	out, err := sqliteListJSON[models.Tenant](ctx, s.db, "tenants")
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (s *SQLiteStore) GetTenant(ctx context.Context, id string) (models.Tenant, error) {
	return sqliteGetJSON[models.Tenant](ctx, s.db, "tenants", id)
}
func (s *SQLiteStore) UpsertTenant(ctx context.Context, v models.Tenant) error {
	now := time.Now().UTC()
	if v.ID == "" {
		v.ID = newid()
		v.CreatedAt = now
	}
	v.UpdatedAt = now
	return sqliteUpsertJSON(ctx, s.db, "tenants", v.ID, v)
}
func (s *SQLiteStore) ListMemberships(ctx context.Context, userID string) ([]models.Membership, error) {
	rows, err := s.db.QueryContext(ctx, `
		select id, user_id, tenant_id, responsibilities, role, created_at, updated_at
		from membership_records
		where user_id = ?
		order by tenant_id asc
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Membership{}
	for rows.Next() {
		membership, err := scanMembership(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, membership)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
func (s *SQLiteStore) ListMembershipsByTenant(ctx context.Context, tenantID string) ([]models.Membership, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		select id, user_id, tenant_id, responsibilities, role, created_at, updated_at
		from membership_records
		where tenant_id = ?
		order by user_id asc
	`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Membership{}
	for rows.Next() {
		membership, err := scanMembership(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, membership)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
func (s *SQLiteStore) ListAllMemberships(ctx context.Context) ([]models.Membership, error) {
	rows, err := s.db.QueryContext(ctx, `
		select id, user_id, tenant_id, responsibilities, role, created_at, updated_at
		from membership_records
		order by user_id asc, tenant_id asc
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Membership{}
	for rows.Next() {
		membership, err := scanMembership(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, membership)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
func (s *SQLiteStore) GetMembership(ctx context.Context, userID, tenantID string) (models.Membership, error) {
	row := s.db.QueryRowContext(ctx, `
		select id, user_id, tenant_id, responsibilities, role, created_at, updated_at
		from membership_records
		where user_id = ? and tenant_id = ?
	`, userID, tenantID)
	membership, err := scanMembership(row)
	if errors.Is(err, sql.ErrNoRows) {
		return models.Membership{}, ErrNotFound
	}
	return membership, err
}
func (s *SQLiteStore) UpsertMembership(ctx context.Context, v models.Membership) error {
	now := time.Now().UTC()
	if v.ID == "" {
		existing, err := s.GetMembership(ctx, v.UserID, v.TenantID)
		if err == nil {
			v.ID = existing.ID
			v.CreatedAt = existing.CreatedAt
		}
	}
	if v.ID == "" {
		v.ID = newid()
		v.CreatedAt = now
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = now
	}
	v.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
		insert into membership_records(id, user_id, tenant_id, responsibilities, role, created_at, updated_at)
		values(?,?,?,?,?,?,?)
		on conflict(user_id, tenant_id) do update set
			responsibilities=excluded.responsibilities,
			role=excluded.role,
			updated_at=excluded.updated_at
	`, v.ID, v.UserID, v.TenantID, v.Responsibilities, string(v.Role), sqliteTimeString(v.CreatedAt), sqliteTimeString(v.UpdatedAt))
	return err
}
func (s *SQLiteStore) DeleteMembership(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "delete from membership_records where id = ?", id)
	return err
}
func (s *SQLiteStore) GetWorkflow(ctx context.Context, tenantID, tenderID string) (models.Workflow, error) {
	row := s.db.QueryRowContext(ctx, `
		select id, tenant_id, tender_id, status, priority, assigned_user, notes, updated_at
		from workflow_records
		where tenant_id = ? and tender_id = ?
	`, tenantID, tenderID)
	workflow, err := scanWorkflow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return models.Workflow{}, ErrNotFound
	}
	return workflow, err
}
func (s *SQLiteStore) ListWorkflows(ctx context.Context, tenantID string) ([]models.Workflow, error) {
	query := `
		select id, tenant_id, tender_id, status, priority, assigned_user, notes, updated_at
		from workflow_records
	`
	args := []any{}
	if tenantID != "" {
		query += " where tenant_id = ?"
		args = append(args, tenantID)
	}
	query += " order by updated_at desc, id asc"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Workflow{}
	for rows.Next() {
		workflow, err := scanWorkflow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, workflow)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *SQLiteStore) GetWorkflowsByTenderIDs(ctx context.Context, tenantID string, tenderIDs []string) (map[string]models.Workflow, error) {
	out := map[string]models.Workflow{}
	if strings.TrimSpace(tenantID) == "" {
		return out, nil
	}
	ids := make([]string, 0, len(tenderIDs))
	seen := map[string]bool{}
	for _, tenderID := range tenderIDs {
		tenderID = strings.TrimSpace(tenderID)
		if tenderID == "" || seen[tenderID] {
			continue
		}
		seen[tenderID] = true
		ids = append(ids, tenderID)
	}
	if len(ids) == 0 {
		return out, nil
	}
	args := []any{tenantID}
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx, `
		select id, tenant_id, tender_id, status, priority, assigned_user, notes, updated_at
		from workflow_records
		where tenant_id = ? and tender_id in (`+placeholders(len(ids))+`)
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		workflow, err := scanWorkflow(rows)
		if err != nil {
			return nil, err
		}
		out[workflow.TenderID] = workflow
	}
	return out, rows.Err()
}

func (s *SQLiteStore) UpsertWorkflow(ctx context.Context, v models.Workflow) error {
	if v.ID == "" {
		existing, err := s.GetWorkflow(ctx, v.TenantID, v.TenderID)
		if err == nil {
			v.ID = existing.ID
		} else {
			v.ID = newid()
		}
	}
	v.UpdatedAt = time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		insert into workflow_records(id, tenant_id, tender_id, status, priority, assigned_user, notes, updated_at)
		values(?,?,?,?,?,?,?,?)
		on conflict(tenant_id, tender_id) do update set
			status=excluded.status,
			priority=excluded.priority,
			assigned_user=excluded.assigned_user,
			notes=excluded.notes,
			updated_at=excluded.updated_at
	`, v.ID, v.TenantID, v.TenderID, v.Status, v.Priority, v.AssignedUser, v.Notes, sqliteTimeString(v.UpdatedAt))
	return err
}
func (s *SQLiteStore) ListBookmarks(ctx context.Context, tenantID, userID string) ([]models.Bookmark, error) {
	rows, err := s.db.QueryContext(ctx, `
		select id, tenant_id, user_id, tender_id, note, created_at, updated_at
		from bookmark_records
		where tenant_id = ? and user_id = ?
		order by updated_at desc, id asc
	`, tenantID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Bookmark{}
	for rows.Next() {
		bookmark, err := scanBookmark(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, bookmark)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *SQLiteStore) GetBookmarksByTenderIDs(ctx context.Context, tenantID, userID string, tenderIDs []string) (map[string]models.Bookmark, error) {
	out := map[string]models.Bookmark{}
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(userID) == "" {
		return out, nil
	}
	ids := make([]string, 0, len(tenderIDs))
	seen := map[string]bool{}
	for _, tenderID := range tenderIDs {
		tenderID = strings.TrimSpace(tenderID)
		if tenderID == "" || seen[tenderID] {
			continue
		}
		seen[tenderID] = true
		ids = append(ids, tenderID)
	}
	if len(ids) == 0 {
		return out, nil
	}
	args := []any{tenantID, userID}
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx, `
		select id, tenant_id, user_id, tender_id, note, created_at, updated_at
		from bookmark_records
		where tenant_id = ? and user_id = ? and tender_id in (`+placeholders(len(ids))+`)
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		bookmark, err := scanBookmark(rows)
		if err != nil {
			return nil, err
		}
		out[bookmark.TenderID] = bookmark
	}
	return out, rows.Err()
}

func (s *SQLiteStore) CountBookmarks(ctx context.Context, tenantID, userID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		select count(*)
		from bookmark_records
		where tenant_id = ? and user_id = ?
	`, tenantID, userID).Scan(&count)
	return count, err
}

func (s *SQLiteStore) UpsertBookmark(ctx context.Context, v models.Bookmark) error {
	now := time.Now().UTC()
	existing, err := s.bookmarkByNaturalKey(ctx, v.TenantID, v.UserID, v.TenderID)
	if err == nil {
		v.ID = existing.ID
		v.CreatedAt = existing.CreatedAt
	}
	if v.ID == "" {
		v.ID = newid()
		v.CreatedAt = now
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = now
	}
	v.UpdatedAt = now
	_, err = s.db.ExecContext(ctx, `
		insert into bookmark_records(id, tenant_id, user_id, tender_id, note, created_at, updated_at)
		values(?,?,?,?,?,?,?)
		on conflict(tenant_id, user_id, tender_id) do update set
			note=excluded.note,
			updated_at=excluded.updated_at
	`, v.ID, v.TenantID, v.UserID, v.TenderID, v.Note, sqliteTimeString(v.CreatedAt), sqliteTimeString(v.UpdatedAt))
	return err
}

func (s *SQLiteStore) ToggleBookmark(ctx context.Context, v models.Bookmark) error {
	existing, err := s.bookmarkByNaturalKey(ctx, v.TenantID, v.UserID, v.TenderID)
	if err == nil {
		_, err = s.db.ExecContext(ctx, "delete from bookmark_records where id = ?", existing.ID)
		return err
	}
	v.ID = newid()
	v.CreatedAt = time.Now().UTC()
	v.UpdatedAt = v.CreatedAt
	_, err = s.db.ExecContext(ctx, `
		insert into bookmark_records(id, tenant_id, user_id, tender_id, note, created_at, updated_at)
		values(?,?,?,?,?,?,?)
	`, v.ID, v.TenantID, v.UserID, v.TenderID, v.Note, sqliteTimeString(v.CreatedAt), sqliteTimeString(v.UpdatedAt))
	return err
}

func (s *SQLiteStore) DeleteBookmark(ctx context.Context, tenantID, userID, tenderID string) error {
	_, err := s.db.ExecContext(ctx, `
		delete from bookmark_records
		where tenant_id = ? and user_id = ? and tender_id = ?
	`, tenantID, userID, tenderID)
	return err
}
func (s *SQLiteStore) ListSavedSearches(ctx context.Context, tenantID, userID string) ([]models.SavedSearch, error) {
	rows, err := s.db.QueryContext(ctx, `
		select id, tenant_id, user_id, name, query, filters, created_at, updated_at
		from saved_search_records
		where tenant_id = ? and user_id = ?
		order by name asc, updated_at desc
	`, tenantID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.SavedSearch{}
	for rows.Next() {
		search, err := scanSavedSearch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, search)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *SQLiteStore) CountSavedSearches(ctx context.Context, tenantID, userID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		select count(*)
		from saved_search_records
		where tenant_id = ? and user_id = ?
	`, tenantID, userID).Scan(&count)
	return count, err
}

func (s *SQLiteStore) UpsertSavedSearch(ctx context.Context, v models.SavedSearch) error {
	now := time.Now().UTC()
	if v.ID == "" {
		v.ID = newid()
		v.CreatedAt = now
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = now
	}
	v.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
		insert into saved_search_records(id, tenant_id, user_id, name, query, filters, created_at, updated_at)
		values(?,?,?,?,?,?,?,?)
		on conflict(id) do update set
			tenant_id=excluded.tenant_id,
			user_id=excluded.user_id,
			name=excluded.name,
			query=excluded.query,
			filters=excluded.filters,
			updated_at=excluded.updated_at
	`, v.ID, v.TenantID, v.UserID, v.Name, v.Query, v.Filters, sqliteTimeString(v.CreatedAt), sqliteTimeString(v.UpdatedAt))
	return err
}
func (s *SQLiteStore) DeleteSavedSearch(ctx context.Context, tenantID, userID, id string) error {
	_, err := s.db.ExecContext(ctx, `
		delete from saved_search_records
		where id = ? and tenant_id = ? and user_id = ?
	`, id, tenantID, userID)
	return err
}
func (s *SQLiteStore) ListSyncRuns(ctx context.Context) ([]models.SyncRun, error) {
	out, err := sqliteListJSON[models.SyncRun](ctx, s.db, "sync_runs")
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out, nil
}

func (s *SQLiteStore) LatestSyncRun(ctx context.Context) (models.SyncRun, error) {
	rows, err := s.db.QueryContext(ctx, `
		select payload
		from sync_runs
		order by coalesce(json_extract(payload, '$.StartedAt'), '') desc, id desc
		limit 1
	`)
	if err != nil {
		return models.SyncRun{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return models.SyncRun{}, ErrNotFound
	}
	var raw string
	if err := rows.Scan(&raw); err != nil {
		return models.SyncRun{}, err
	}
	var run models.SyncRun
	if err := json.Unmarshal([]byte(raw), &run); err != nil {
		return models.SyncRun{}, err
	}
	return run, rows.Err()
}

func (s *SQLiteStore) AddSyncRun(ctx context.Context, v models.SyncRun) error {
	if v.ID == "" {
		v.ID = newid()
	}
	return sqliteUpsertJSON(ctx, s.db, "sync_runs", v.ID, v)
}
func (s *SQLiteStore) ListSourceConfigs(ctx context.Context) ([]models.SourceConfig, error) {
	out, err := sqliteListJSON[models.SourceConfig](ctx, s.db, "source_configs")
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].Key < out[j].Key
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}
func (s *SQLiteStore) GetSourceConfig(ctx context.Context, key string) (models.SourceConfig, error) {
	items, err := s.ListSourceConfigs(ctx)
	if err != nil {
		return models.SourceConfig{}, err
	}
	key = strings.TrimSpace(key)
	for _, item := range items {
		if item.Key == key || item.ID == key {
			return item, nil
		}
	}
	return models.SourceConfig{}, ErrNotFound
}
func (s *SQLiteStore) UpsertSourceConfig(ctx context.Context, v models.SourceConfig) error {
	now := time.Now().UTC()
	if v.ID == "" {
		v.ID = newid()
		v.CreatedAt = now
	}
	v.UpdatedAt = now
	return sqliteUpsertJSON(ctx, s.db, "source_configs", v.ID, v)
}
func (s *SQLiteStore) DeleteSourceConfig(ctx context.Context, id string) error {
	return sqliteDelete(ctx, s.db, "source_configs", id)
}
func (s *SQLiteStore) ListSourceHealth(ctx context.Context) ([]models.SourceHealth, error) {
	out, err := sqliteListJSON[models.SourceHealth](ctx, s.db, "source_health")
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SourceKey < out[j].SourceKey })
	return out, nil
}
func (s *SQLiteStore) GetSourceHealth(ctx context.Context, sourceKey string) (models.SourceHealth, error) {
	return sqliteGetJSON[models.SourceHealth](ctx, s.db, "source_health", sourceKey)
}
func (s *SQLiteStore) UpsertSourceHealth(ctx context.Context, v models.SourceHealth) error {
	key := v.SourceKey
	if key == "" {
		key = newid()
	}
	return sqliteUpsertJSON(ctx, s.db, "source_health", key, v)
}
func (s *SQLiteStore) DeleteSourceHealth(ctx context.Context, sourceKey string) error {
	return sqliteDelete(ctx, s.db, "source_health", sourceKey)
}
func (s *SQLiteStore) ListSourceAssignments(ctx context.Context, tenantID string) ([]models.TenantSourceAssignment, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		select id, tenant_id, source_key, created_at, updated_at
		from tenant_source_assignments
		where tenant_id = ?
		order by source_key asc
	`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.TenantSourceAssignment{}
	for rows.Next() {
		assignment, err := scanTenantSourceAssignment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, assignment)
	}
	return out, rows.Err()
}
func (s *SQLiteStore) UpsertSourceAssignment(ctx context.Context, v models.TenantSourceAssignment) error {
	now := time.Now().UTC()
	v.TenantID = strings.TrimSpace(v.TenantID)
	v.SourceKey = strings.TrimSpace(v.SourceKey)
	if v.TenantID == "" || v.SourceKey == "" {
		return fmt.Errorf("tenant source assignment requires tenant_id and source_key")
	}
	var existingID string
	var createdAt string
	err := s.db.QueryRowContext(ctx, `
		select id, created_at
		from tenant_source_assignments
		where tenant_id = ? and source_key = ?
		limit 1
	`, v.TenantID, v.SourceKey).Scan(&existingID, &createdAt)
	switch {
	case err == nil:
		v.ID = existingID
		v.CreatedAt = parseSQLiteTime(createdAt)
	case errors.Is(err, sql.ErrNoRows):
		if v.ID == "" {
			v.ID = newid()
		}
		if v.CreatedAt.IsZero() {
			v.CreatedAt = now
		}
	default:
		return err
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = now
	}
	v.UpdatedAt = now
	_, err = s.db.ExecContext(ctx, `
		insert into tenant_source_assignments(id, tenant_id, source_key, created_at, updated_at)
		values(?,?,?,?,?)
		on conflict(tenant_id, source_key) do update set
			updated_at=excluded.updated_at
	`, v.ID, v.TenantID, v.SourceKey, sqliteTimeString(v.CreatedAt), sqliteTimeString(v.UpdatedAt))
	return err
}
func (s *SQLiteStore) GetSourceScheduleSettings(ctx context.Context) (models.SourceScheduleSettings, error) {
	return sqliteGetJSON[models.SourceScheduleSettings](ctx, s.db, "source_schedule_settings", "global")
}
func (s *SQLiteStore) UpsertSourceScheduleSettings(ctx context.Context, v models.SourceScheduleSettings) error {
	now := time.Now().UTC()
	if v.ID == "" {
		v.ID = "global"
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = now
	}
	v.UpdatedAt = now
	return sqliteUpsertJSON(ctx, s.db, "source_schedule_settings", v.ID, v)
}
func (s *SQLiteStore) ListJobs(ctx context.Context) ([]models.ExtractionJob, error) {
	out, err := sqliteListJSON[models.ExtractionJob](ctx, s.db, "jobs")
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *SQLiteStore) ListValidJobs(ctx context.Context) ([]models.ExtractionJob, error) {
	rows, err := s.db.QueryContext(ctx, `
		select j.payload
		from jobs j
		where coalesce(json_extract(j.payload, '$.TenderID'), '') <> ''
		  and exists (
			select 1
			from tenders t
			where t.id = coalesce(json_extract(j.payload, '$.TenderID'), '')
		  )
		order by coalesce(json_extract(j.payload, '$.CreatedAt'), '') desc, j.id desc
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.ExtractionJob{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var job models.ExtractionJob
		if err := json.Unmarshal([]byte(raw), &job); err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) PruneInvalidJobs(ctx context.Context) (int, error) {
	result, err := s.db.ExecContext(ctx, `
		delete from jobs
		where coalesce(json_extract(payload, '$.TenderID'), '') = ''
		   or not exists (
			select 1
			from tenders t
			where t.id = coalesce(json_extract(jobs.payload, '$.TenderID'), '')
		   )
	`)
	if err != nil {
		return 0, err
	}
	removed, err := result.RowsAffected()
	return int(removed), err
}

func (s *SQLiteStore) JobStateCounts(ctx context.Context) (JobStateCounts, error) {
	rows, err := s.db.QueryContext(ctx, `
		select coalesce(json_extract(j.payload, '$.State'), '') as state, count(*)
		from jobs j
		where coalesce(json_extract(j.payload, '$.TenderID'), '') <> ''
		  and exists (
			select 1
			from tenders t
			where t.id = coalesce(json_extract(j.payload, '$.TenderID'), '')
		  )
		group by state
	`)
	if err != nil {
		return JobStateCounts{}, err
	}
	defer rows.Close()
	counts := JobStateCounts{}
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			return JobStateCounts{}, err
		}
		switch models.ExtractionState(state) {
		case models.ExtractionQueued:
			counts.Queued = count
		case models.ExtractionProcessing:
			counts.Processing = count
		case models.ExtractionRetry:
			counts.Retry = count
		case models.ExtractionFailed:
			counts.Failed = count
		case models.ExtractionCompleted:
			counts.Completed = count
		}
	}
	return counts, rows.Err()
}

func (s *SQLiteStore) JobAlertSnapshot(ctx context.Context) (JobAlertSnapshot, error) {
	var snapshot JobAlertSnapshot
	var oldestPending sql.NullString
	err := s.db.QueryRowContext(ctx, `
		select
			coalesce(sum(case when coalesce(json_extract(j.payload, '$.State'), '') = ? then 1 else 0 end), 0),
			coalesce(sum(case when coalesce(json_extract(j.payload, '$.State'), '') = ? then 1 else 0 end), 0),
			coalesce(sum(case when coalesce(json_extract(j.payload, '$.State'), '') = ? then 1 else 0 end), 0),
			coalesce(sum(case when coalesce(json_extract(j.payload, '$.State'), '') = ? then 1 else 0 end), 0),
			coalesce(sum(case when coalesce(json_extract(j.payload, '$.State'), '') = ? then 1 else 0 end), 0),
			min(case
				when coalesce(json_extract(j.payload, '$.State'), '') in (?, ?, ?)
				then coalesce(json_extract(j.payload, '$.CreatedAt'), '')
				else null
			end)
		from jobs j
		where coalesce(json_extract(j.payload, '$.TenderID'), '') <> ''
		  and exists (
			select 1
			from tenders t
			where t.id = coalesce(json_extract(j.payload, '$.TenderID'), '')
		  )
	`,
		string(models.ExtractionQueued),
		string(models.ExtractionProcessing),
		string(models.ExtractionRetry),
		string(models.ExtractionFailed),
		string(models.ExtractionCompleted),
		string(models.ExtractionQueued),
		string(models.ExtractionRetry),
		string(models.ExtractionProcessing),
	).Scan(
		&snapshot.Queued,
		&snapshot.Processing,
		&snapshot.Retry,
		&snapshot.Failed,
		&snapshot.Completed,
		&oldestPending,
	)
	if err != nil {
		return JobAlertSnapshot{}, err
	}
	if oldestPending.Valid && strings.TrimSpace(oldestPending.String) != "" {
		snapshot.OldestPendingAt, err = time.Parse(time.RFC3339Nano, oldestPending.String)
		if err != nil {
			return JobAlertSnapshot{}, err
		}
	}
	return snapshot, nil
}

func (s *SQLiteStore) QueueJob(ctx context.Context, v models.ExtractionJob) error {
	var existingID string
	err := s.db.QueryRowContext(ctx, `
		select id
		from jobs
		where coalesce(json_extract(payload, '$.TenderID'), '') = ?
		  and coalesce(json_extract(payload, '$.DocumentURL'), '') = ?
		  and coalesce(json_extract(payload, '$.State'), '') <> ?
		limit 1
	`, v.TenderID, v.DocumentURL, string(models.ExtractionCompleted)).Scan(&existingID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if v.ID == "" {
		v.ID = newid()
	}
	if v.State == "" {
		v.State = models.ExtractionQueued
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now().UTC()
	}
	v.UpdatedAt = time.Now().UTC()
	if v.NextAttemptAt.IsZero() {
		v.NextAttemptAt = time.Now().UTC()
	}
	return sqliteUpsertJSON(ctx, s.db, "jobs", v.ID, v)
}
func (s *SQLiteStore) UpdateJob(ctx context.Context, v models.ExtractionJob) error {
	v.UpdatedAt = time.Now().UTC()
	return sqliteUpsertJSON(ctx, s.db, "jobs", v.ID, v)
}
func (s *SQLiteStore) DeleteJob(ctx context.Context, id string) error {
	return sqliteDelete(ctx, s.db, "jobs", id)
}
func (s *SQLiteStore) Dashboard(ctx context.Context, tenantID string, lowMemory, analytics bool) (models.Dashboard, error) {
	d := models.Dashboard{LowMemoryMode: lowMemory, AnalyticsEnabled: analytics}
	if err := s.db.QueryRowContext(ctx, `
		select
			count(*),
			coalesce(sum(case when coalesce(json_extract(payload, '$.EngineeringRelevant'), 0) = 1 then 1 else 0 end), 0),
			coalesce(sum(case when coalesce(json_extract(payload, '$.DocumentURL'), '') <> '' then 1 else 0 end), 0),
			coalesce(sum(case when coalesce(json_extract(payload, '$.DocumentStatus'), '') = ? then 1 else 0 end), 0),
			coalesce(sum(case when coalesce(json_extract(payload, '$.DocumentStatus'), '') in (?, ?) then 1 else 0 end), 0),
			coalesce(sum(case when lower(coalesce(json_extract(payload, '$.Status'), '')) = 'open' then 1 else 0 end), 0)
		from tenders
	`, string(models.ExtractionCompleted), string(models.ExtractionQueued), string(models.ExtractionRetry)).Scan(
		&d.TotalTenders,
		&d.EngineeringRelevant,
		&d.WithDocuments,
		&d.ExtractedDocuments,
		&d.QueuedDocuments,
		&d.OpenTenders,
	); err != nil {
		return models.Dashboard{}, err
	}

	rows, err := s.db.QueryContext(ctx, `
		select payload
		from tenders
		order by coalesce(json_extract(payload, '$.PublishedDate'), '') desc, id asc
		limit 8
	`)
	if err != nil {
		return models.Dashboard{}, err
	}
	defer rows.Close()
	d.RecentTenders = make([]models.Tender, 0, 8)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return models.Dashboard{}, err
		}
		var tender models.Tender
		if err := json.Unmarshal([]byte(raw), &tender); err != nil {
			return models.Dashboard{}, err
		}
		d.RecentTenders = append(d.RecentTenders, tender)
	}
	if err := rows.Err(); err != nil {
		return models.Dashboard{}, err
	}
	return d, nil
}

func (s *SQLiteStore) ListAuditEntries(ctx context.Context, tenantID string) ([]models.AuditEntry, error) {
	out, err := sqliteListJSON[models.AuditEntry](ctx, s.db, "audit_entries")
	if err != nil {
		return nil, err
	}
	filtered := []models.AuditEntry{}
	for _, x := range out {
		if tenantID == "" || x.TenantID == tenantID {
			filtered = append(filtered, x)
		}
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].CreatedAt.After(filtered[j].CreatedAt) })
	return filtered, nil
}

func (s *SQLiteStore) ListAuditEntriesPage(ctx context.Context, tenantID string, page, pageSize int) ([]models.AuditEntry, int, error) {
	return s.listAuditEntriesPageWhere(ctx, tenantID, page, pageSize, "")
}

func (s *SQLiteStore) ListSecurityAuditEntriesPage(ctx context.Context, tenantID string, page, pageSize int) ([]models.AuditEntry, int, error) {
	return s.listAuditEntriesPageWhere(ctx, tenantID, page, pageSize, `
		(
			coalesce(json_extract(payload, '$.metadata.category'), '') = 'security'
			or coalesce(json_extract(payload, '$.entity'), '') in ('auth', 'user_security', 'user_password', 'membership')
			or (
				coalesce(json_extract(payload, '$.entity'), '') in ('user', 'tenant')
				and coalesce(json_extract(payload, '$.action'), '') in ('create', 'update', 'delete', 'switch')
			)
		)
	`)
}

func (s *SQLiteStore) listAuditEntriesPageWhere(ctx context.Context, tenantID string, page, pageSize int, extraWhere string) ([]models.AuditEntry, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	args := []any{}
	whereParts := []string{"1=1"}
	if strings.TrimSpace(tenantID) != "" {
		whereParts = append(whereParts, "coalesce(json_extract(payload, '$.tenant_id'), '') = ?")
		args = append(args, tenantID)
	}
	if extraWhere = strings.TrimSpace(extraWhere); extraWhere != "" {
		whereParts = append(whereParts, extraWhere)
	}
	where := strings.Join(whereParts, " and ")
	var total int
	if err := s.db.QueryRowContext(ctx, "select count(*) from audit_entries where "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	if offset >= total {
		return []models.AuditEntry{}, total, nil
	}
	queryArgs := append(append([]any{}, args...), pageSize, offset)
	rows, err := s.db.QueryContext(ctx, `
		select payload
		from audit_entries
		where `+where+`
		order by coalesce(json_extract(payload, '$.created_at'), '') desc, id desc
		limit ? offset ?
	`, queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]models.AuditEntry, 0, pageSize)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, 0, err
		}
		var item models.AuditEntry
		if err := json.Unmarshal([]byte(raw), &item); err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func (s *SQLiteStore) AddAuditEntry(ctx context.Context, v models.AuditEntry) error {
	if v.ID == "" {
		v.ID = newid()
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now().UTC()
	}
	if v.Metadata == nil {
		v.Metadata = map[string]string{}
	}
	return sqliteUpsertJSON(ctx, s.db, "audit_entries", v.ID, v)
}

func (s *SQLiteStore) ListWorkflowEvents(ctx context.Context, tenantID, tenderID string) ([]models.WorkflowEvent, error) {
	out, err := sqliteListJSON[models.WorkflowEvent](ctx, s.db, "workflow_events")
	if err != nil {
		return nil, err
	}
	filtered := []models.WorkflowEvent{}
	for _, x := range out {
		if (tenantID == "" || x.TenantID == tenantID) && (tenderID == "" || x.TenderID == tenderID) {
			filtered = append(filtered, x)
		}
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].CreatedAt.After(filtered[j].CreatedAt) })
	return filtered, nil
}

func (s *SQLiteStore) AddWorkflowEvent(ctx context.Context, v models.WorkflowEvent) error {
	if v.ID == "" {
		v.ID = newid()
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now().UTC()
	}
	return sqliteUpsertJSON(ctx, s.db, "workflow_events", v.ID, v)
}
