package models

import "time"

type PlatformRole string
type TenantRole string

const (
	PlatformRoleNone       PlatformRole = ""
	PlatformRoleSuperAdmin PlatformRole = "platform_super_admin"
	PlatformRoleAdmin      PlatformRole = "platform_admin"

	TenantRoleOwner     TenantRole = "tenant_owner"
	TenantRoleAdmin     TenantRole = "tenant_admin"
	TenantRoleSuperUser TenantRole = "super_user"
	TenantRoleUser      TenantRole = "user"
	TenantRoleViewer    TenantRole = "viewer"
)

type ExtractionState string

const (
	ExtractionQueued     ExtractionState = "queued"
	ExtractionProcessing ExtractionState = "processing"
	ExtractionRetry      ExtractionState = "retry"
	ExtractionCompleted  ExtractionState = "completed"
	ExtractionFailed     ExtractionState = "failed"
	ExtractionSkipped    ExtractionState = "skipped"
)

const (
	JobTypeExtraction           = "extraction"
	JobTypeExpiredTenderCleanup = "expired_tender_cleanup"

	ExpiredTenderCleanupJobID   = "maintenance-expired-tender-cleanup"
	ExpiredTenderCleanupJobName = "Remove Expired Tenders"
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
	ArchiveReason                                                                                                                                                           string
	ExtractionSkippedReason, ExtractionSkippedSource                                                                                                                        string
	GroupTags                                                                                                                                                               []string
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
	CreatedAt, UpdatedAt, ArchivedAt, ExtractionSkippedAt                                                                                                                   time.Time
}
type User struct {
	ID, Username, DisplayName, Email, PasswordHash, PasswordSalt, MFASecret string
	PlatformRole                                                            PlatformRole
	IsActive, MFAEnabled                                                    bool
	FailedLogins                                                            int
	SessionVersion                                                          int
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
	Role                                   TenantRole
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

type SmartMatchMode string

const (
	SmartMatchModeAny SmartMatchMode = "ANY"
	SmartMatchModeAll SmartMatchMode = "ALL"
)

type ExtractionMode string

const (
	ExtractionModeNoFilter             ExtractionMode = "no_filter"
	ExtractionModeSmartKeywordCriteria ExtractionMode = "smart_keyword_extraction"
)

type SmartExtractionSettings struct {
	TenantID, RefreshStatus, RefreshMessage    string
	ExtractionMode                             ExtractionMode
	Enabled, AlertsEnabled, EmailAlertsEnabled bool
	LastReprocessedAt, CreatedAt, UpdatedAt    time.Time
}

type SmartKeywordGroup struct {
	ID, TenantID, Name, TagName, Description string
	MatchMode                                SmartMatchMode
	ExcludeTerms                             []string
	MinMatchCount, Priority                  int
	Enabled                                  bool
	CreatedAt, UpdatedAt                     time.Time
}

type SmartKeyword struct {
	ID, TenantID, GroupID, Value, NormalizedValue string
	Enabled                                       bool
	CreatedAt, UpdatedAt                          time.Time
}

type SmartGroupEvaluation struct {
	GroupID, GroupName, TagName string
	MatchMode                   SmartMatchMode
	MatchedKeywords             []string
	ExcludeMatches              []string
	MinMatchCount, Priority     int
	Accepted                    bool
	Reason                      string
}

type SmartKeywordEvaluation struct {
	Enabled, Accepted                                      bool
	ActiveKeywordCount                                     int
	MatchedKeywords, StandaloneMatches, GroupTags, Reasons []string
	GroupMatches                                           []SmartGroupEvaluation
}

type SmartTenderPreview struct {
	Tender     Tender
	Evaluation SmartKeywordEvaluation
}

type SmartReprocessResult struct {
	TenantID                      string
	Processed, Accepted, Excluded int
	UpdatedAt                     time.Time
}

type SmartViewFilters struct {
	Query, Source, Issuer, Category, Status, DateFrom, DateTo, MatchedStatus string
	GroupTags                                                                []string
	MinPriority                                                              int
}

type NotificationChannel struct {
	ID, Type, Destination string
	Enabled               bool
	Settings              map[string]string
}

type SavedSmartView struct {
	ID, TenantID, UserID, Name, FiltersJSON string
	Pinned                                  bool
	AlertsEnabled, AlertPaused              bool
	AlertFrequency                          string
	AlertChannels                           []NotificationChannel
	CreatedAt, UpdatedAt                    time.Time
}

type SmartAlertDelivery struct {
	ID, TenantID, ViewID, TenderID, ChannelType, Destination, Frequency string
	Status, Error, DedupKey, Message                                    string
	CreatedAt, SentAt                                                   time.Time
}

type EmailSettings struct {
	ID               string
	Enabled          bool
	SMTPHost         string
	SMTPPort         int
	SMTPSecurityMode string
	SMTPAuthRequired bool
	SMTPUsername     string
	SMTPPassword     string
	SMTPFromEmail    string
	SMTPFromName     string
	SMTPReplyTo      string
	TimeoutSeconds   int
	TestRecipient    string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type EmailMessage struct {
	To        []string
	CC        []string
	BCC       []string
	Subject   string
	TextBody  string
	HTMLBody  string
	FromEmail string
	FromName  string
	ReplyTo   string
}

type EmailSendResult struct {
	AcceptedRecipients int
	Message            string
}
type KeywordProfile struct {
	ID, TenantID, UserID, Name, RefreshStatus, RefreshMessage string
	MatchCount                                                int
	LastRefreshedAt, CreatedAt, UpdatedAt                     time.Time
}
type Keyword struct {
	ID, ProfileID, TenantID, UserID, Value string
	Enabled                                bool
	CreatedAt, UpdatedAt                   time.Time
}
type KeywordTenderMatch struct {
	ID, ProfileID, TenantID, UserID, TenderID string
	MatchedKeywords                           []string
	MatchCount                                int
	RefreshedAt, CreatedAt, UpdatedAt         time.Time
}
type KeywordTenderMatchResult struct {
	Match  KeywordTenderMatch
	Tender Tender
}
type KeywordSearchSummary struct {
	Profile            KeywordProfile
	TotalKeywordCount  int
	ActiveKeywordCount int
	MatchedTenderCount int
	LastRefreshedAt    time.Time
	RefreshStatus      string
	RefreshMessage     string
}
type ExpiredTenderCleanupResult struct {
	RemovedCount     int
	RemovedTenderIDs []string
	RunAt            time.Time
}
type SyncRun struct {
	ID, SourceKey, Status, Message, Trigger string
	ItemCount                               int
	StartedAt, FinishedAt                   time.Time
}
type ExtractionJob struct {
	ID, TenderID, DocumentURL, LastError, SkipReason, SkipSource string
	JobType, JobName, TenantID, UserID, ResultSummary            string
	State                                                        ExtractionState
	Attempts                                                     int
	NextAttemptAt, CreatedAt, UpdatedAt, SkippedAt               time.Time
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
type TenantSourceAssignment struct {
	ID, TenantID, SourceKey string
	CreatedAt, UpdatedAt    time.Time
}
type Session struct {
	ID, UserID, TenantID, CSRF    string
	SessionVersion                int
	Expires, CreatedAt, UpdatedAt time.Time
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
