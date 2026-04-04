package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"tenderhub-za/internal/models"

	_ "modernc.org/sqlite"
)

func TestSQLiteSessionCRUDAndUserRevocation(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	session := models.Session{
		ID:             "session-1",
		UserID:         "user-1",
		TenantID:       "tenant-1",
		CSRF:           "csrf-1",
		SessionVersion: 2,
		Expires:        time.Now().Add(time.Hour),
	}
	if err := s.UpsertSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.GetSession(ctx, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.UserID != "user-1" || loaded.SessionVersion != 2 {
		t.Fatalf("unexpected loaded session: %#v", loaded)
	}

	if err := s.UpsertSession(ctx, models.Session{ID: "session-2", UserID: "user-1", TenantID: "tenant-2", CSRF: "csrf-2", Expires: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSessionsForUser(ctx, "user-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSession(ctx, "session-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected revoked session to disappear, got %v", err)
	}
	if _, err := s.GetSession(ctx, "session-2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected revoked session to disappear, got %v", err)
	}
}

func TestSQLiteGetSessionPrunesExpiredRows(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	if err := s.UpsertSession(ctx, models.Session{ID: "expired", UserID: "user-1", TenantID: "tenant-1", CSRF: "csrf", Expires: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSession(ctx, "expired"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected expired session to be treated as missing, got %v", err)
	}
	rows, err := s.ListJobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("unexpected job side effects while checking sessions: %#v", rows)
	}
}

func TestSQLiteToggleBookmarkAndSavedSearchDelete(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	bookmark := models.Bookmark{TenantID: "tenant-1", UserID: "user-1", TenderID: "tender-1", Note: "follow"}
	if err := s.ToggleBookmark(ctx, bookmark); err != nil {
		t.Fatal(err)
	}
	bookmarks, err := s.ListBookmarks(ctx, "tenant-1", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(bookmarks) != 1 {
		t.Fatalf("expected bookmark to be created, got %#v", bookmarks)
	}
	if err := s.ToggleBookmark(ctx, bookmark); err != nil {
		t.Fatal(err)
	}
	bookmarks, err = s.ListBookmarks(ctx, "tenant-1", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(bookmarks) != 0 {
		t.Fatalf("expected second toggle to remove bookmark, got %#v", bookmarks)
	}

	search := models.SavedSearch{ID: "search-1", TenantID: "tenant-1", UserID: "user-1", Name: "Metro", Query: "q=metro"}
	if err := s.UpsertSavedSearch(ctx, search); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSavedSearch(ctx, "tenant-1", "user-1", "search-1"); err != nil {
		t.Fatal(err)
	}
	searches, err := s.ListSavedSearches(ctx, "tenant-1", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(searches) != 0 {
		t.Fatalf("expected saved search deletion, got %#v", searches)
	}
}

func TestSQLiteMigratesLegacyJSONEntitiesIntoRelationalTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	statements := []string{
		`create table schema_meta (key text primary key, value text not null);`,
		`create table users (id text primary key, payload text not null);`,
		`create table memberships (id text primary key, payload text not null);`,
		`create table workflows (id text primary key, payload text not null);`,
		`create table bookmarks (id text primary key, payload text not null);`,
		`create table saved_searches (id text primary key, payload text not null);`,
		`pragma user_version = 3;`,
		`insert into schema_meta(key,value) values('schema_version','3');`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	mustInsertJSON := func(table, id string, value any) {
		t.Helper()
		payload, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, "insert into "+table+"(id,payload) values(?,?)", id, string(payload)); err != nil {
			t.Fatal(err)
		}
	}
	mustInsertJSON("users", "u1", models.User{ID: "u1", Username: "legacy", DisplayName: "Legacy User", Email: "legacy@example.com", IsActive: true})
	mustInsertJSON("memberships", "m1", models.Membership{ID: "m1", UserID: "u1", TenantID: "tenant-1", Role: models.RoleAnalyst})
	mustInsertJSON("workflows", "w1", models.Workflow{ID: "w1", TenantID: "tenant-1", TenderID: "tender-1", Status: "reviewing"})
	mustInsertJSON("bookmarks", "b1", models.Bookmark{ID: "b1", TenantID: "tenant-1", UserID: "u1", TenderID: "tender-1", Note: "legacy"})
	mustInsertJSON("saved_searches", "s1", models.SavedSearch{ID: "s1", TenantID: "tenant-1", UserID: "u1", Name: "Legacy Search", Query: "q=legacy"})
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	users, err := store.ListUsers(ctx)
	if err != nil || len(users) != 1 || users[0].Username != "legacy" {
		t.Fatalf("expected migrated user record, err=%v users=%#v", err, users)
	}
	memberships, err := store.ListMemberships(ctx, "u1")
	if err != nil || len(memberships) != 1 {
		t.Fatalf("expected migrated membership, err=%v memberships=%#v", err, memberships)
	}
	workflows, err := store.ListWorkflows(ctx, "tenant-1")
	if err != nil || len(workflows) != 1 {
		t.Fatalf("expected migrated workflow, err=%v workflows=%#v", err, workflows)
	}
	bookmarks, err := store.ListBookmarks(ctx, "tenant-1", "u1")
	if err != nil || len(bookmarks) != 1 {
		t.Fatalf("expected migrated bookmark, err=%v bookmarks=%#v", err, bookmarks)
	}
	searches, err := store.ListSavedSearches(ctx, "tenant-1", "u1")
	if err != nil || len(searches) != 1 {
		t.Fatalf("expected migrated saved search, err=%v searches=%#v", err, searches)
	}
}
