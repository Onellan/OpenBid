package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"tenderhub-za/internal/models"
)

func sqliteTimeString(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseSQLiteTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func intToBool(value int) bool {
	return value != 0
}

func encodeStringSlice(values []string) string {
	if len(values) == 0 {
		return "[]"
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

func decodeStringSlice(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(value), &out); err != nil {
		return nil
	}
	return out
}

func sqliteTableExistsTx(ctx context.Context, tx *sql.Tx, table string) (bool, error) {
	var count int
	if err := tx.QueryRowContext(ctx, "select count(*) from sqlite_master where type='table' and name=?", table).Scan(&count); err != nil {
		return false, err
	}
	return count == 1, nil
}

func relationalTableCount(ctx context.Context, tx *sql.Tx, table string) (int, error) {
	var count int
	if err := tx.QueryRowContext(ctx, "select count(*) from "+table).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func loadLegacyJSONEntities[T any](ctx context.Context, tx *sql.Tx, table string) ([]T, error) {
	exists, err := sqliteTableExistsTx(ctx, tx, table)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	rows, err := tx.QueryContext(ctx, "select payload from "+table)
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
		var item T
		if err := json.Unmarshal([]byte(raw), &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) migrateRelationalTables(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`create table if not exists user_records (
			id text primary key,
			username text not null unique,
			display_name text not null,
			email text not null unique,
			password_hash text not null,
			password_salt text not null,
			mfa_secret text not null,
			is_active integer not null,
			mfa_enabled integer not null,
			failed_logins integer not null,
			session_version integer not null,
			locked_until text not null,
			recovery_codes text not null,
			created_at text not null,
			updated_at text not null
		);`,
		`create table if not exists membership_records (
			id text primary key,
			user_id text not null,
			tenant_id text not null,
			responsibilities text not null,
			role text not null,
			created_at text not null,
			updated_at text not null,
			unique(user_id, tenant_id)
		);`,
		`create table if not exists workflow_records (
			id text primary key,
			tenant_id text not null,
			tender_id text not null,
			status text not null,
			priority text not null,
			assigned_user text not null,
			notes text not null,
			updated_at text not null,
			unique(tenant_id, tender_id)
		);`,
		`create table if not exists bookmark_records (
			id text primary key,
			tenant_id text not null,
			user_id text not null,
			tender_id text not null,
			note text not null,
			created_at text not null,
			updated_at text not null,
			unique(tenant_id, user_id, tender_id)
		);`,
		`create table if not exists saved_search_records (
			id text primary key,
			tenant_id text not null,
			user_id text not null,
			name text not null,
			query text not null,
			filters text not null,
			created_at text not null,
			updated_at text not null
		);`,
		`create table if not exists sessions (
			id text primary key,
			user_id text not null,
			tenant_id text not null,
			csrf text not null,
			session_version integer not null,
			expires_at text not null,
			created_at text not null,
			updated_at text not null
		);`,
		`create index if not exists idx_user_records_username on user_records(lower(username));`,
		`create index if not exists idx_membership_records_user on membership_records(user_id, tenant_id);`,
		`create index if not exists idx_membership_records_tenant on membership_records(tenant_id, user_id);`,
		`create index if not exists idx_workflow_records_tenant on workflow_records(tenant_id, status);`,
		`create index if not exists idx_bookmark_records_tenant_user on bookmark_records(tenant_id, user_id, updated_at);`,
		`create index if not exists idx_saved_search_records_tenant_user on saved_search_records(tenant_id, user_id, name);`,
		`create index if not exists idx_sessions_user on sessions(user_id);`,
		`create index if not exists idx_sessions_expires on sessions(expires_at);`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	if err := s.backfillUsers(ctx, tx); err != nil {
		return err
	}
	if err := s.backfillMemberships(ctx, tx); err != nil {
		return err
	}
	if err := s.backfillWorkflows(ctx, tx); err != nil {
		return err
	}
	if err := s.backfillBookmarks(ctx, tx); err != nil {
		return err
	}
	if err := s.backfillSavedSearches(ctx, tx); err != nil {
		return err
	}
	return nil
}

func (s *SQLiteStore) backfillUsers(ctx context.Context, tx *sql.Tx) error {
	count, err := relationalTableCount(ctx, tx, "user_records")
	if err != nil || count > 0 {
		return err
	}
	items, err := loadLegacyJSONEntities[models.User](ctx, tx, "users")
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.ID == "" {
			item.ID = newid()
		}
		if item.CreatedAt.IsZero() {
			item.CreatedAt = time.Now().UTC()
		}
		if item.UpdatedAt.IsZero() {
			item.UpdatedAt = item.CreatedAt
		}
		if _, err := tx.ExecContext(ctx, `
			insert into user_records(
				id, username, display_name, email, password_hash, password_salt, mfa_secret, is_active, mfa_enabled,
				failed_logins, session_version, locked_until, recovery_codes, created_at, updated_at
			) values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		`,
			item.ID, item.Username, item.DisplayName, item.Email, item.PasswordHash, item.PasswordSalt, item.MFASecret,
			boolToInt(item.IsActive), boolToInt(item.MFAEnabled), item.FailedLogins, item.SessionVersion,
			sqliteTimeString(item.LockedUntil), encodeStringSlice(item.RecoveryCodes),
			sqliteTimeString(item.CreatedAt), sqliteTimeString(item.UpdatedAt),
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) backfillMemberships(ctx context.Context, tx *sql.Tx) error {
	count, err := relationalTableCount(ctx, tx, "membership_records")
	if err != nil || count > 0 {
		return err
	}
	items, err := loadLegacyJSONEntities[models.Membership](ctx, tx, "memberships")
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.ID == "" {
			item.ID = newid()
		}
		if item.CreatedAt.IsZero() {
			item.CreatedAt = time.Now().UTC()
		}
		if item.UpdatedAt.IsZero() {
			item.UpdatedAt = item.CreatedAt
		}
		if _, err := tx.ExecContext(ctx, `
			insert into membership_records(id, user_id, tenant_id, responsibilities, role, created_at, updated_at)
			values(?,?,?,?,?,?,?)
		`, item.ID, item.UserID, item.TenantID, item.Responsibilities, string(item.Role), sqliteTimeString(item.CreatedAt), sqliteTimeString(item.UpdatedAt)); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) backfillWorkflows(ctx context.Context, tx *sql.Tx) error {
	count, err := relationalTableCount(ctx, tx, "workflow_records")
	if err != nil || count > 0 {
		return err
	}
	items, err := loadLegacyJSONEntities[models.Workflow](ctx, tx, "workflows")
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.ID == "" {
			item.ID = newid()
		}
		if item.UpdatedAt.IsZero() {
			item.UpdatedAt = time.Now().UTC()
		}
		if _, err := tx.ExecContext(ctx, `
			insert into workflow_records(id, tenant_id, tender_id, status, priority, assigned_user, notes, updated_at)
			values(?,?,?,?,?,?,?,?)
		`, item.ID, item.TenantID, item.TenderID, item.Status, item.Priority, item.AssignedUser, item.Notes, sqliteTimeString(item.UpdatedAt)); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) backfillBookmarks(ctx context.Context, tx *sql.Tx) error {
	count, err := relationalTableCount(ctx, tx, "bookmark_records")
	if err != nil || count > 0 {
		return err
	}
	items, err := loadLegacyJSONEntities[models.Bookmark](ctx, tx, "bookmarks")
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.ID == "" {
			item.ID = newid()
		}
		if item.CreatedAt.IsZero() {
			item.CreatedAt = time.Now().UTC()
		}
		if item.UpdatedAt.IsZero() {
			item.UpdatedAt = item.CreatedAt
		}
		if _, err := tx.ExecContext(ctx, `
			insert into bookmark_records(id, tenant_id, user_id, tender_id, note, created_at, updated_at)
			values(?,?,?,?,?,?,?)
		`, item.ID, item.TenantID, item.UserID, item.TenderID, item.Note, sqliteTimeString(item.CreatedAt), sqliteTimeString(item.UpdatedAt)); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) backfillSavedSearches(ctx context.Context, tx *sql.Tx) error {
	count, err := relationalTableCount(ctx, tx, "saved_search_records")
	if err != nil || count > 0 {
		return err
	}
	items, err := loadLegacyJSONEntities[models.SavedSearch](ctx, tx, "saved_searches")
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.ID == "" {
			item.ID = newid()
		}
		if item.CreatedAt.IsZero() {
			item.CreatedAt = time.Now().UTC()
		}
		if item.UpdatedAt.IsZero() {
			item.UpdatedAt = item.CreatedAt
		}
		if _, err := tx.ExecContext(ctx, `
			insert into saved_search_records(id, tenant_id, user_id, name, query, filters, created_at, updated_at)
			values(?,?,?,?,?,?,?,?)
		`, item.ID, item.TenantID, item.UserID, item.Name, item.Query, item.Filters, sqliteTimeString(item.CreatedAt), sqliteTimeString(item.UpdatedAt)); err != nil {
			return err
		}
	}
	return nil
}

func scanUser(scanner interface {
	Scan(dest ...any) error
}) (models.User, error) {
	var (
		user          models.User
		isActive      int
		mfaEnabled    int
		lockedUntil   string
		recoveryCodes string
		createdAt     string
		updatedAt     string
	)
	err := scanner.Scan(
		&user.ID, &user.Username, &user.DisplayName, &user.Email, &user.PasswordHash, &user.PasswordSalt, &user.MFASecret,
		&isActive, &mfaEnabled, &user.FailedLogins, &user.SessionVersion, &lockedUntil, &recoveryCodes, &createdAt, &updatedAt,
	)
	if err != nil {
		return models.User{}, err
	}
	user.IsActive = intToBool(isActive)
	user.MFAEnabled = intToBool(mfaEnabled)
	user.LockedUntil = parseSQLiteTime(lockedUntil)
	user.RecoveryCodes = decodeStringSlice(recoveryCodes)
	user.CreatedAt = parseSQLiteTime(createdAt)
	user.UpdatedAt = parseSQLiteTime(updatedAt)
	return user, nil
}

func scanMembership(scanner interface {
	Scan(dest ...any) error
}) (models.Membership, error) {
	var (
		membership models.Membership
		role       string
		createdAt  string
		updatedAt  string
	)
	err := scanner.Scan(&membership.ID, &membership.UserID, &membership.TenantID, &membership.Responsibilities, &role, &createdAt, &updatedAt)
	if err != nil {
		return models.Membership{}, err
	}
	membership.Role = models.Role(role)
	membership.CreatedAt = parseSQLiteTime(createdAt)
	membership.UpdatedAt = parseSQLiteTime(updatedAt)
	return membership, nil
}

func scanWorkflow(scanner interface {
	Scan(dest ...any) error
}) (models.Workflow, error) {
	var (
		workflow  models.Workflow
		updatedAt string
	)
	err := scanner.Scan(&workflow.ID, &workflow.TenantID, &workflow.TenderID, &workflow.Status, &workflow.Priority, &workflow.AssignedUser, &workflow.Notes, &updatedAt)
	if err != nil {
		return models.Workflow{}, err
	}
	workflow.UpdatedAt = parseSQLiteTime(updatedAt)
	return workflow, nil
}

func scanBookmark(scanner interface {
	Scan(dest ...any) error
}) (models.Bookmark, error) {
	var (
		bookmark  models.Bookmark
		createdAt string
		updatedAt string
	)
	err := scanner.Scan(&bookmark.ID, &bookmark.TenantID, &bookmark.UserID, &bookmark.TenderID, &bookmark.Note, &createdAt, &updatedAt)
	if err != nil {
		return models.Bookmark{}, err
	}
	bookmark.CreatedAt = parseSQLiteTime(createdAt)
	bookmark.UpdatedAt = parseSQLiteTime(updatedAt)
	return bookmark, nil
}

func scanSavedSearch(scanner interface {
	Scan(dest ...any) error
}) (models.SavedSearch, error) {
	var (
		search    models.SavedSearch
		createdAt string
		updatedAt string
	)
	err := scanner.Scan(&search.ID, &search.TenantID, &search.UserID, &search.Name, &search.Query, &search.Filters, &createdAt, &updatedAt)
	if err != nil {
		return models.SavedSearch{}, err
	}
	search.CreatedAt = parseSQLiteTime(createdAt)
	search.UpdatedAt = parseSQLiteTime(updatedAt)
	return search, nil
}

func scanSession(scanner interface {
	Scan(dest ...any) error
}) (models.Session, error) {
	var (
		session   models.Session
		expiresAt string
		createdAt string
		updatedAt string
	)
	err := scanner.Scan(&session.ID, &session.UserID, &session.TenantID, &session.CSRF, &session.SessionVersion, &expiresAt, &createdAt, &updatedAt)
	if err != nil {
		return models.Session{}, err
	}
	session.Expires = parseSQLiteTime(expiresAt)
	session.CreatedAt = parseSQLiteTime(createdAt)
	session.UpdatedAt = parseSQLiteTime(updatedAt)
	return session, nil
}

func (s *SQLiteStore) GetSession(ctx context.Context, id string) (models.Session, error) {
	row := s.db.QueryRowContext(ctx, `
		select id, user_id, tenant_id, csrf, session_version, expires_at, created_at, updated_at
		from sessions where id = ?
	`, strings.TrimSpace(id))
	session, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return models.Session{}, ErrNotFound
	}
	if err != nil {
		return models.Session{}, err
	}
	if !session.Expires.IsZero() && time.Now().After(session.Expires) {
		_ = s.DeleteSession(ctx, session.ID)
		return models.Session{}, ErrNotFound
	}
	return session, nil
}

func (s *SQLiteStore) UpsertSession(ctx context.Context, session models.Session) error {
	now := time.Now().UTC()
	if session.ID == "" {
		session.ID = newid()
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	}
	if session.Expires.IsZero() {
		session.Expires = now
	}
	session.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
		insert into sessions(id, user_id, tenant_id, csrf, session_version, expires_at, created_at, updated_at)
		values(?,?,?,?,?,?,?,?)
		on conflict(id) do update set
			user_id=excluded.user_id,
			tenant_id=excluded.tenant_id,
			csrf=excluded.csrf,
			session_version=excluded.session_version,
			expires_at=excluded.expires_at,
			updated_at=excluded.updated_at
	`, session.ID, session.UserID, session.TenantID, session.CSRF, session.SessionVersion, sqliteTimeString(session.Expires), sqliteTimeString(session.CreatedAt), sqliteTimeString(session.UpdatedAt))
	return err
}

func (s *SQLiteStore) DeleteSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "delete from sessions where id = ?", strings.TrimSpace(id))
	return err
}

func (s *SQLiteStore) DeleteSessionsForUser(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, "delete from sessions where user_id = ?", strings.TrimSpace(userID))
	return err
}

func (s *SQLiteStore) bookmarkByNaturalKey(ctx context.Context, tenantID, userID, tenderID string) (models.Bookmark, error) {
	row := s.db.QueryRowContext(ctx, `
		select id, tenant_id, user_id, tender_id, note, created_at, updated_at
		from bookmark_records
		where tenant_id = ? and user_id = ? and tender_id = ?
	`, strings.TrimSpace(tenantID), strings.TrimSpace(userID), strings.TrimSpace(tenderID))
	bookmark, err := scanBookmark(row)
	if errors.Is(err, sql.ErrNoRows) {
		return models.Bookmark{}, ErrNotFound
	}
	return bookmark, err
}
