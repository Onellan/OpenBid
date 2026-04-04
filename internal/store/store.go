package store

import (
	"context"
	"errors"
	"openbid/internal/models"
	"strings"
	"time"
)

var ErrNotFound = errors.New("not found")

type ListFilter struct {
	Query, Source, Province, Category, Issuer, Status, CIDB, WorkflowStatus, DocumentStatus, Sort, View, TenantID, UserID string
	BookmarkedOnly, HasDocuments                                                                                          bool
	Page, PageSize                                                                                                        int
}

type RuntimeStats struct {
	Path                  string
	SizeBytes             int64
	WALSizeBytes          int64
	SHMSizeBytes          int64
	SchemaVersion         int
	ExpectedSchemaVersion int
	JournalMode           string
	QuickCheck            string
	TenderCount           int
	UserCount             int
	TenantCount           int
	MembershipCount       int
	WorkflowCount         int
	BookmarkCount         int
	SavedSearchCount      int
	SyncRunCount          int
	SourceConfigCount     int
	SourceHealthCount     int
	TenantSourceCount     int
	JobCount              int
	AuditCount            int
	WorkflowEventCount    int
}

type JobStateCounts struct {
	Queued     int
	Processing int
	Retry      int
	Failed     int
	Completed  int
}

type JobAlertSnapshot struct {
	Queued          int
	Processing      int
	Retry           int
	Failed          int
	Completed       int
	OldestPendingAt time.Time
}

type NamedValue struct {
	Value string
	Label string
}

type TenderFilterOptions struct {
	Sources        []NamedValue
	Provinces      []string
	Statuses       []string
	Categories     []string
	Issuers        []string
	CIDBGradings   []string
	WorkflowStatus []string
	DocumentStatus []string
}

func NormalizeFilter(f ListFilter) ListFilter {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 {
		f.PageSize = 20
	}
	if f.PageSize > 100 {
		f.PageSize = 100
	}
	if f.Sort == "" {
		f.Sort = "closing_date"
	}
	if f.View == "" {
		f.View = "table"
	}
	return f
}

func ContainsCI(s, q string) bool { return strings.Contains(strings.ToLower(s), strings.ToLower(q)) }

type Store interface {
	ListTenders(context.Context, ListFilter) ([]models.Tender, int, error)
	TenderFilterOptions(context.Context, string) (TenderFilterOptions, error)
	GetTender(context.Context, string) (models.Tender, error)
	UpsertTender(context.Context, models.Tender) error

	ListUsers(context.Context) ([]models.User, error)
	ListUsersByIDs(context.Context, []string) ([]models.User, error)
	GetUserByUsername(context.Context, string) (models.User, error)
	GetUserByEmail(context.Context, string) (models.User, error)
	GetUser(context.Context, string) (models.User, error)
	UpsertUser(context.Context, models.User) error
	DeleteSessionsForUser(context.Context, string) error

	ListTenants(context.Context) ([]models.Tenant, error)
	GetTenant(context.Context, string) (models.Tenant, error)
	UpsertTenant(context.Context, models.Tenant) error

	ListMemberships(context.Context, string) ([]models.Membership, error)
	ListMembershipsByTenant(context.Context, string) ([]models.Membership, error)
	ListAllMemberships(context.Context) ([]models.Membership, error)
	GetMembership(context.Context, string, string) (models.Membership, error)
	UpsertMembership(context.Context, models.Membership) error
	DeleteMembership(context.Context, string) error

	GetWorkflow(context.Context, string, string) (models.Workflow, error)
	ListWorkflows(context.Context, string) ([]models.Workflow, error)
	GetWorkflowsByTenderIDs(context.Context, string, []string) (map[string]models.Workflow, error)
	UpsertWorkflow(context.Context, models.Workflow) error

	ListBookmarks(context.Context, string, string) ([]models.Bookmark, error)
	GetBookmarksByTenderIDs(context.Context, string, string, []string) (map[string]models.Bookmark, error)
	CountBookmarks(context.Context, string, string) (int, error)
	UpsertBookmark(context.Context, models.Bookmark) error
	ToggleBookmark(context.Context, models.Bookmark) error
	DeleteBookmark(context.Context, string, string, string) error

	ListSavedSearches(context.Context, string, string) ([]models.SavedSearch, error)
	CountSavedSearches(context.Context, string, string) (int, error)
	UpsertSavedSearch(context.Context, models.SavedSearch) error
	DeleteSavedSearch(context.Context, string, string, string) error

	ListSyncRuns(context.Context) ([]models.SyncRun, error)
	LatestSyncRun(context.Context) (models.SyncRun, error)
	AddSyncRun(context.Context, models.SyncRun) error
	ListSourceConfigs(context.Context) ([]models.SourceConfig, error)
	GetSourceConfig(context.Context, string) (models.SourceConfig, error)
	UpsertSourceConfig(context.Context, models.SourceConfig) error
	DeleteSourceConfig(context.Context, string) error
	ListSourceHealth(context.Context) ([]models.SourceHealth, error)
	GetSourceHealth(context.Context, string) (models.SourceHealth, error)
	UpsertSourceHealth(context.Context, models.SourceHealth) error
	DeleteSourceHealth(context.Context, string) error
	ListSourceAssignments(context.Context, string) ([]models.TenantSourceAssignment, error)
	UpsertSourceAssignment(context.Context, models.TenantSourceAssignment) error
	GetSourceScheduleSettings(context.Context) (models.SourceScheduleSettings, error)
	UpsertSourceScheduleSettings(context.Context, models.SourceScheduleSettings) error

	ListJobs(context.Context) ([]models.ExtractionJob, error)
	ListValidJobs(context.Context) ([]models.ExtractionJob, error)
	PruneInvalidJobs(context.Context) (int, error)
	JobStateCounts(context.Context) (JobStateCounts, error)
	JobAlertSnapshot(context.Context) (JobAlertSnapshot, error)
	QueueJob(context.Context, models.ExtractionJob) error
	UpdateJob(context.Context, models.ExtractionJob) error
	DeleteJob(context.Context, string) error

	ListAuditEntries(context.Context, string) ([]models.AuditEntry, error)
	ListAuditEntriesPage(context.Context, string, int, int) ([]models.AuditEntry, int, error)
	ListSecurityAuditEntriesPage(context.Context, string, int, int) ([]models.AuditEntry, int, error)
	AddAuditEntry(context.Context, models.AuditEntry) error
	ListWorkflowEvents(context.Context, string, string) ([]models.WorkflowEvent, error)
	AddWorkflowEvent(context.Context, models.WorkflowEvent) error

	GetSession(context.Context, string) (models.Session, error)
	UpsertSession(context.Context, models.Session) error
	DeleteSession(context.Context, string) error

	GetTendersByIDs(context.Context, []string) (map[string]models.Tender, error)
	Dashboard(context.Context, string, bool, bool) (models.Dashboard, error)
}
