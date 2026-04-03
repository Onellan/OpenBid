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

	"tenderhub-za/internal/models"
)

const currentSchemaVersion = 2

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
	for _, table := range []string{"tenders", "users", "tenants", "memberships", "workflows", "bookmarks", "saved_searches", "sync_runs", "source_configs", "source_schedule_settings", "jobs", "source_health", "audit_entries", "workflow_events"} {
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
		fmt.Sprintf(`pragma user_version = %d;`, currentSchemaVersion),
		fmt.Sprintf(`insert into schema_meta(key,value) values('schema_version','%d') on conflict(key) do update set value='%d';`, currentSchemaVersion, currentSchemaVersion),
	}
	for _, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			_ = tx.Rollback()
			return err
		}
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
		{"users", &stats.UserCount},
		{"tenants", &stats.TenantCount},
		{"memberships", &stats.MembershipCount},
		{"workflows", &stats.WorkflowCount},
		{"bookmarks", &stats.BookmarkCount},
		{"saved_searches", &stats.SavedSearchCount},
		{"sync_runs", &stats.SyncRunCount},
		{"source_configs", &stats.SourceConfigCount},
		{"source_health", &stats.SourceHealthCount},
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

func (s *SQLiteStore) listAllTenders(ctx context.Context) ([]models.Tender, error) {
	return sqliteListJSON[models.Tender](ctx, s.db, "tenders")
}

func (s *SQLiteStore) ListTenders(ctx context.Context, f ListFilter) ([]models.Tender, int, error) {
	f = NormalizeFilter(f)
	if f.WorkflowStatus != "" || f.BookmarkedOnly {
		return s.listTendersInMemory(ctx, f)
	}
	items, total, err := s.listTendersSQL(ctx, f)
	if err == nil {
		return items, total, nil
	}
	return s.listTendersInMemory(ctx, f)
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
		case "published_date":
			return out[i].PublishedDate > out[j].PublishedDate
		case "relevance":
			return out[i].RelevanceScore > out[j].RelevanceScore
		case "cidb":
			return out[i].CIDBGrading > out[j].CIDBGrading
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

func tenderOrderClause(sortKey string) string {
	switch sortKey {
	case "published_date":
		return "coalesce(json_extract(payload, '$.PublishedDate'), '') desc, id asc"
	case "relevance":
		return "coalesce(json_extract(payload, '$.RelevanceScore'), 0) desc, id asc"
	case "cidb":
		return "coalesce(json_extract(payload, '$.CIDBGrading'), '') desc, id asc"
	default:
		return "coalesce(json_extract(payload, '$.ClosingDate'), '') asc, id asc"
	}
}

func buildTenderSQLFilter(f ListFilter) (string, []any) {
	clauses := []string{"1=1"}
	args := []any{}
	if f.Query != "" {
		term := "%" + strings.ToLower(f.Query) + "%"
		clauses = append(clauses, "(lower(coalesce(json_extract(payload, '$.Title'), '')) like ? or lower(coalesce(json_extract(payload, '$.Issuer'), '')) like ? or lower(coalesce(json_extract(payload, '$.Summary'), '')) like ? or lower(coalesce(json_extract(payload, '$.TenderNumber'), '')) like ?)")
		args = append(args, term, term, term, term)
	}
	if f.Source != "" {
		clauses = append(clauses, "coalesce(json_extract(payload, '$.SourceKey'), '') = ?")
		args = append(args, f.Source)
	}
	if f.Province != "" {
		clauses = append(clauses, "lower(coalesce(json_extract(payload, '$.Province'), '')) = ?")
		args = append(args, strings.ToLower(f.Province))
	}
	if f.Category != "" {
		clauses = append(clauses, "lower(coalesce(json_extract(payload, '$.Category'), '')) = ?")
		args = append(args, strings.ToLower(f.Category))
	}
	if f.Issuer != "" {
		clauses = append(clauses, "lower(coalesce(json_extract(payload, '$.Issuer'), '')) like ?")
		args = append(args, "%"+strings.ToLower(f.Issuer)+"%")
	}
	if f.Status != "" {
		clauses = append(clauses, "lower(coalesce(json_extract(payload, '$.Status'), '')) = ?")
		args = append(args, strings.ToLower(f.Status))
	}
	if f.CIDB != "" {
		clauses = append(clauses, "lower(coalesce(json_extract(payload, '$.CIDBGrading'), '')) like ?")
		args = append(args, "%"+strings.ToLower(f.CIDB)+"%")
	}
	if f.DocumentStatus != "" {
		clauses = append(clauses, "coalesce(json_extract(payload, '$.DocumentStatus'), '') = ?")
		args = append(args, f.DocumentStatus)
	}
	if f.HasDocuments {
		clauses = append(clauses, "coalesce(json_extract(payload, '$.DocumentURL'), '') <> ''")
	}
	return strings.Join(clauses, " and "), args
}

func (s *SQLiteStore) listTendersSQL(ctx context.Context, f ListFilter) ([]models.Tender, int, error) {
	whereClause, args := buildTenderSQLFilter(f)
	countQuery := "select count(*) from tenders where " + whereClause
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	offset := (f.Page - 1) * f.PageSize
	if offset >= total {
		return []models.Tender{}, total, nil
	}
	queryArgs := append(append([]any{}, args...), f.PageSize, offset)
	rows, err := s.db.QueryContext(ctx, "select payload from tenders where "+whereClause+" order by "+tenderOrderClause(f.Sort)+" limit ? offset ?", queryArgs...)
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
	out, err := sqliteListJSON[models.User](ctx, s.db, "users")
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	return out, nil
}
func (s *SQLiteStore) GetUserByUsername(ctx context.Context, username string) (models.User, error) {
	items, err := s.ListUsers(ctx)
	if err != nil {
		return models.User{}, err
	}
	for _, u := range items {
		if u.Username == username {
			return u, nil
		}
	}
	return models.User{}, ErrNotFound
}
func (s *SQLiteStore) GetUser(ctx context.Context, id string) (models.User, error) {
	return sqliteGetJSON[models.User](ctx, s.db, "users", id)
}
func (s *SQLiteStore) UpsertUser(ctx context.Context, v models.User) error {
	now := time.Now().UTC()
	if v.ID == "" {
		v.ID = newid()
		v.CreatedAt = now
	}
	v.UpdatedAt = now
	return sqliteUpsertJSON(ctx, s.db, "users", v.ID, v)
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
	items, err := sqliteListJSON[models.Membership](ctx, s.db, "memberships")
	if err != nil {
		return nil, err
	}
	out := []models.Membership{}
	for _, m := range items {
		if m.UserID == userID {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TenantID < out[j].TenantID })
	return out, nil
}
func (s *SQLiteStore) ListAllMemberships(ctx context.Context) ([]models.Membership, error) {
	out, err := sqliteListJSON[models.Membership](ctx, s.db, "memberships")
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UserID == out[j].UserID {
			return out[i].TenantID < out[j].TenantID
		}
		return out[i].UserID < out[j].UserID
	})
	return out, nil
}
func (s *SQLiteStore) GetMembership(ctx context.Context, userID, tenantID string) (models.Membership, error) {
	items, err := s.ListAllMemberships(ctx)
	if err != nil {
		return models.Membership{}, err
	}
	for _, m := range items {
		if m.UserID == userID && m.TenantID == tenantID {
			return m, nil
		}
	}
	return models.Membership{}, ErrNotFound
}
func (s *SQLiteStore) UpsertMembership(ctx context.Context, v models.Membership) error {
	now := time.Now().UTC()
	if v.ID == "" {
		items, _ := s.ListAllMemberships(ctx)
		for _, m := range items {
			if m.UserID == v.UserID && m.TenantID == v.TenantID {
				v.ID = m.ID
				v.CreatedAt = m.CreatedAt
				break
			}
		}
	}
	if v.ID == "" {
		v.ID = newid()
		v.CreatedAt = now
	}
	v.UpdatedAt = now
	return sqliteUpsertJSON(ctx, s.db, "memberships", v.ID, v)
}
func (s *SQLiteStore) DeleteMembership(ctx context.Context, id string) error {
	return sqliteDelete(ctx, s.db, "memberships", id)
}
func (s *SQLiteStore) GetWorkflow(ctx context.Context, tenantID, tenderID string) (models.Workflow, error) {
	items, err := s.ListWorkflows(ctx, tenantID)
	if err != nil {
		return models.Workflow{}, err
	}
	for _, wf := range items {
		if wf.TenderID == tenderID {
			return wf, nil
		}
	}
	return models.Workflow{}, ErrNotFound
}
func (s *SQLiteStore) ListWorkflows(ctx context.Context, tenantID string) ([]models.Workflow, error) {
	items, err := sqliteListJSON[models.Workflow](ctx, s.db, "workflows")
	if err != nil {
		return nil, err
	}
	out := []models.Workflow{}
	for _, wf := range items {
		if tenantID == "" || wf.TenantID == tenantID {
			out = append(out, wf)
		}
	}
	return out, nil
}
func (s *SQLiteStore) UpsertWorkflow(ctx context.Context, v models.Workflow) error {
	if v.ID == "" {
		items, _ := s.ListWorkflows(ctx, v.TenantID)
		for _, wf := range items {
			if wf.TenantID == v.TenantID && wf.TenderID == v.TenderID {
				v.ID = wf.ID
				break
			}
		}
		if v.ID == "" {
			v.ID = newid()
		}
	}
	v.UpdatedAt = time.Now().UTC()
	return sqliteUpsertJSON(ctx, s.db, "workflows", v.ID, v)
}
func (s *SQLiteStore) ListBookmarks(ctx context.Context, tenantID, userID string) ([]models.Bookmark, error) {
	items, err := sqliteListJSON[models.Bookmark](ctx, s.db, "bookmarks")
	if err != nil {
		return nil, err
	}
	out := []models.Bookmark{}
	for _, b := range items {
		if b.TenantID == tenantID && b.UserID == userID {
			out = append(out, b)
		}
	}
	return out, nil
}

func (s *SQLiteStore) UpsertBookmark(ctx context.Context, v models.Bookmark) error {
	items, err := sqliteListJSON[models.Bookmark](ctx, s.db, "bookmarks")
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, b := range items {
		if b.TenantID == v.TenantID && b.UserID == v.UserID && b.TenderID == v.TenderID {
			b.Note = v.Note
			b.UpdatedAt = now
			return sqliteUpsertJSON(ctx, s.db, "bookmarks", b.ID, b)
		}
	}
	v.ID = newid()
	v.CreatedAt = now
	v.UpdatedAt = now
	return sqliteUpsertJSON(ctx, s.db, "bookmarks", v.ID, v)
}

func (s *SQLiteStore) ToggleBookmark(ctx context.Context, v models.Bookmark) error {
	items, err := sqliteListJSON[models.Bookmark](ctx, s.db, "bookmarks")
	if err != nil {
		return err
	}
	for _, b := range items {
		if b.TenantID == v.TenantID && b.UserID == v.UserID && b.TenderID == v.TenderID {
			return sqliteDelete(ctx, s.db, "bookmarks", b.ID)
		}
	}
	v.ID = newid()
	v.CreatedAt = time.Now().UTC()
	v.UpdatedAt = v.CreatedAt
	return sqliteUpsertJSON(ctx, s.db, "bookmarks", v.ID, v)
}

func (s *SQLiteStore) DeleteBookmark(ctx context.Context, tenantID, userID, tenderID string) error {
	items, err := sqliteListJSON[models.Bookmark](ctx, s.db, "bookmarks")
	if err != nil {
		return err
	}
	for _, b := range items {
		if b.TenantID == tenantID && b.UserID == userID && b.TenderID == tenderID {
			return sqliteDelete(ctx, s.db, "bookmarks", b.ID)
		}
	}
	return nil
}
func (s *SQLiteStore) ListSavedSearches(ctx context.Context, tenantID, userID string) ([]models.SavedSearch, error) {
	items, err := sqliteListJSON[models.SavedSearch](ctx, s.db, "saved_searches")
	if err != nil {
		return nil, err
	}
	out := []models.SavedSearch{}
	for _, v := range items {
		if v.TenantID == tenantID && v.UserID == userID {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (s *SQLiteStore) UpsertSavedSearch(ctx context.Context, v models.SavedSearch) error {
	now := time.Now().UTC()
	if v.ID == "" {
		v.ID = newid()
		v.CreatedAt = now
	}
	v.UpdatedAt = now
	return sqliteUpsertJSON(ctx, s.db, "saved_searches", v.ID, v)
}
func (s *SQLiteStore) DeleteSavedSearch(ctx context.Context, tenantID, userID, id string) error {
	v, err := sqliteGetJSON[models.SavedSearch](ctx, s.db, "saved_searches", id)
	if err == nil && v.TenantID == tenantID && v.UserID == userID {
		return sqliteDelete(ctx, s.db, "saved_searches", id)
	}
	if errors.Is(err, ErrNotFound) {
		return nil
	}
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
func (s *SQLiteStore) QueueJob(ctx context.Context, v models.ExtractionJob) error {
	items, err := s.ListJobs(ctx)
	if err != nil {
		return err
	}
	for _, j := range items {
		if j.TenderID == v.TenderID && j.DocumentURL == v.DocumentURL && j.State != models.ExtractionCompleted {
			return nil
		}
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
	items, err := s.listAllTenders(ctx)
	if err != nil {
		return models.Dashboard{}, err
	}
	d := models.Dashboard{LowMemoryMode: lowMemory, AnalyticsEnabled: analytics}
	for _, t := range items {
		d.TotalTenders++
		if t.EngineeringRelevant {
			d.EngineeringRelevant++
		}
		if t.DocumentURL != "" {
			d.WithDocuments++
		}
		if t.DocumentStatus == models.ExtractionCompleted {
			d.ExtractedDocuments++
		}
		if t.DocumentStatus == models.ExtractionQueued || t.DocumentStatus == models.ExtractionRetry {
			d.QueuedDocuments++
		}
		if strings.ToLower(t.Status) == "open" {
			d.OpenTenders++
		}
		d.RecentTenders = append(d.RecentTenders, t)
	}
	sort.Slice(d.RecentTenders, func(i, j int) bool { return d.RecentTenders[i].PublishedDate > d.RecentTenders[j].PublishedDate })
	if len(d.RecentTenders) > 8 {
		d.RecentTenders = d.RecentTenders[:8]
	}
	d.SyncHistory, _ = s.ListSyncRuns(ctx)
	d.SourceHealth, _ = s.ListSourceHealth(ctx)
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
