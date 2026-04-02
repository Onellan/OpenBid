package models

import "time"

type Role string

const (
	RoleAdmin            Role = "admin"
	RolePortfolioManager Role = "portfolio_manager"
	RoleTenantAdmin      Role = "tenant_admin"
	RoleAnalyst          Role = "analyst"
	RoleReviewer         Role = "reviewer"
	RoleOperator         Role = "operator"
	RoleViewer           Role = "viewer"
)

type ExtractionState string

const (
	ExtractionQueued     ExtractionState = "queued"
	ExtractionProcessing ExtractionState = "processing"
	ExtractionRetry      ExtractionState = "retry"
	ExtractionCompleted  ExtractionState = "completed"
	ExtractionFailed     ExtractionState = "failed"
)

type TenderDocument struct {
	URL, FileName, MIMEType, Role, Source, LastModified string
	SizeBytes                                           int64
}

type TenderContact struct {
	Role, Name, Email, Telephone, Fax, Mobile string
}

type TenderBriefing struct {
	Label, DateTime, Venue, Address, Notes string
	Required                               bool
}

type TenderSubmission struct {
	Method, Address, DeliveryLocation, Instructions string
	ElectronicAllowed, PhysicalAllowed              bool
	TwoEnvelope                                     bool
}

type TenderEvaluation struct {
	Method                    string
	PricePoints               int
	PreferencePoints          int
	MinimumFunctionalityScore float64
	Notes                     string
}

type TenderRequirement struct {
	Category, Description string
	Required              bool
}

type TenderLocation struct {
	Site, DeliveryLocation, Street, Suburb, Town, PostalCode, Province, Coordinates string
}

type Tender struct {
	ID, SourceKey, ExternalID, Title, Issuer, Province, Category, TenderNumber, PublishedDate, ClosingDate, Status, CIDBGrading, Summary, OriginalURL, DocumentURL, Excerpt string
	EngineeringRelevant                                                                                                                                                     bool
	RelevanceScore                                                                                                                                                          float64
	TenderType                                                                                                                                                              string
	Scope                                                                                                                                                                   string
	ValidityDays                                                                                                                                                            int
	DocumentStatus                                                                                                                                                          ExtractionState
	ExtractedFacts                                                                                                                                                          map[string]string
	PageFacts                                                                                                                                                               map[string]string
	DocumentFacts                                                                                                                                                           map[string]string
	SourceMetadata                                                                                                                                                          map[string]string
	Location                                                                                                                                                                TenderLocation
	Submission                                                                                                                                                              TenderSubmission
	Evaluation                                                                                                                                                              TenderEvaluation
	Contacts                                                                                                                                                                []TenderContact
	Briefings                                                                                                                                                               []TenderBriefing
	Documents                                                                                                                                                               []TenderDocument
	Requirements                                                                                                                                                            []TenderRequirement
	CreatedAt, UpdatedAt                                                                                                                                                    time.Time
}
type User struct {
	ID, Username, DisplayName, Email, PasswordHash, PasswordSalt, MFASecret string
	IsActive, MFAEnabled                                                    bool
	FailedLogins                                                            int
	LockedUntil                                                             time.Time
	RecoveryCodes                                                           []string
	CreatedAt, UpdatedAt                                                    time.Time
}
type Tenant struct {
	ID, Name, Slug       string
	CreatedAt, UpdatedAt time.Time
}
type Membership struct {
	ID, UserID, TenantID, Responsibilities string
	Role                                   Role
	CreatedAt, UpdatedAt                   time.Time
}
type Workflow struct {
	ID, TenantID, TenderID, Status, Priority, AssignedUser, Notes string
	UpdatedAt                                                     time.Time
}
type Bookmark struct {
	ID, TenantID, UserID, TenderID, Note string
	CreatedAt, UpdatedAt                 time.Time
}
type SavedSearch struct {
	ID, TenantID, UserID, Name, Query, Filters string
	CreatedAt, UpdatedAt                       time.Time
}
type SyncRun struct {
	ID, SourceKey, Status, Message, Trigger string
	ItemCount                               int
	StartedAt, FinishedAt                   time.Time
}
type ExtractionJob struct {
	ID, TenderID, DocumentURL, LastError string
	State                                ExtractionState
	Attempts                             int
	NextAttemptAt, CreatedAt, UpdatedAt  time.Time
}
type SourceHealth struct {
	SourceKey, LastStatus, LastMessage, HealthStatus, LastTrigger          string
	LastSyncAt, LastCheckedAt, LastSuccessfulCheckAt, NextScheduledCheckAt time.Time
	LastItemCount, ConsecutiveFailures                                     int
	Running, PendingManualCheck                                            bool
}
type SourceConfig struct {
	ID, Key, Name, Type, FeedURL                   string
	Enabled, ManualChecksEnabled, AutoCheckEnabled bool
	IntervalMinutes                                int
	CreatedAt, UpdatedAt                           time.Time
}
type SourceScheduleSettings struct {
	ID                     string
	DefaultIntervalMinutes int
	Paused                 bool
	CreatedAt, UpdatedAt   time.Time
}
type Session struct {
	UserID, TenantID, CSRF string
	Expires                time.Time
}
type Dashboard struct {
	TotalTenders, EngineeringRelevant, WithDocuments, ExtractedDocuments, QueuedDocuments, OpenTenders int
	RecentTenders                                                                                      []Tender
	SyncHistory                                                                                        []SyncRun
	SourceHealth                                                                                       []SourceHealth
	LowMemoryMode, AnalyticsEnabled                                                                    bool
}

type AuditEntry struct {
	ID        string            `json:"id"`
	TenantID  string            `json:"tenant_id"`
	UserID    string            `json:"user_id"`
	Action    string            `json:"action"`
	Entity    string            `json:"entity"`
	EntityID  string            `json:"entity_id"`
	Summary   string            `json:"summary"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
}

type WorkflowEvent struct {
	ID           string    `json:"id"`
	TenantID     string    `json:"tenant_id"`
	TenderID     string    `json:"tender_id"`
	ChangedBy    string    `json:"changed_by"`
	Status       string    `json:"status"`
	Priority     string    `json:"priority"`
	AssignedUser string    `json:"assigned_user"`
	Notes        string    `json:"notes"`
	CreatedAt    time.Time `json:"created_at"`
}
