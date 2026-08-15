// Package persistence holds GORM models and repositories for the business
// tables (migrations/*.sql). GORM AutoMigrate and Hooks are never used here
// (PRD §15.1 / R11): schema changes go through goose, and state transitions
// that matter (credit CAS, job status) go through hand-written SQL with an
// explicit RowsAffected check, not GORM callbacks.
package persistence

import "time"

// User mirrors the `users` table (migrations/00001_init.sql).
type User struct {
	ID           uint64 `gorm:"primaryKey"`
	BizID        string `gorm:"column:biz_id"`
	Email        string
	PasswordHash string    `gorm:"column:password_hash"`
	CreatedAt    time.Time `gorm:"column:created_at"`
	UpdatedAt    time.Time `gorm:"column:updated_at"`
}

func (User) TableName() string { return "users" }

// CreditAccount mirrors the `credit_accounts` table.
type CreditAccount struct {
	UserID    uint64 `gorm:"column:user_id;primaryKey"`
	Balance   int
	Held      int
	Version   int
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

func (CreditAccount) TableName() string { return "credit_accounts" }

// CreditLedger mirrors the `credit_ledger` table (migrations/00004_credits.sql).
// Read-only from the HTTP layer — every write goes through creditsvc's own
// transactional Hold/Commit/Refund/Recharge, never a raw GORM Create here.
type CreditLedger struct {
	ID           uint64 `gorm:"primaryKey"`
	UserID       uint64 `gorm:"column:user_id"`
	Direction    string
	Amount       int
	BalanceAfter int    `gorm:"column:balance_after"`
	HeldAfter    int    `gorm:"column:held_after"`
	RefType      string `gorm:"column:ref_type"`
	RefID        string `gorm:"column:ref_id"`
	IdemKey      string `gorm:"column:idem_key"`
	Remark       string
	CreatedAt    time.Time `gorm:"column:created_at"`
}

func (CreditLedger) TableName() string { return "credit_ledger" }

// Asset mirrors the `assets` table.
type Asset struct {
	ID            uint64 `gorm:"primaryKey"`
	BizID         string `gorm:"column:biz_id"`
	UserID        uint64 `gorm:"column:user_id"`
	ProjectID     *uint64
	Type          string
	Source        string
	FromTaskRunID string `gorm:"column:from_task_run_id"`
	ParentAssetID *uint64

	StorageKey    string `gorm:"column:storage_key"`
	PublicURL     string `gorm:"column:public_url"`
	ThumbKey      string `gorm:"column:thumb_key"`
	Mime          string
	Width         int
	Height        int
	DurationMs    int    `gorm:"column:duration_ms"`
	SizeBytes     int64  `gorm:"column:size_bytes"`
	ResolutionTag string `gorm:"column:resolution_tag"`

	FirstFrameAssetID *uint64 `gorm:"column:first_frame_asset_id"`
	LastFrameAssetID  *uint64 `gorm:"column:last_frame_asset_id"`

	Meta             []byte     `gorm:"column:meta;type:json"`
	ModerationStatus string     `gorm:"column:moderation_status"`
	DeletedAt        *time.Time `gorm:"column:deleted_at"`
	CreatedAt        time.Time  `gorm:"column:created_at"`
}

func (Asset) TableName() string { return "assets" }

// Job mirrors the `jobs` table.
type Job struct {
	ID        uint64 `gorm:"primaryKey"`
	BizID     string `gorm:"column:biz_id"`
	UserID    uint64 `gorm:"column:user_id"`
	ProjectID *uint64
	// RetryOfJobID/RetryOfNodeName/RetryOfLoopIndex are set only on
	// satellite retry jobs (jobsvc.RetryNode) — see migrations/00006's doc
	// for why this is the only new state a satellite run needs; every other
	// field on this row behaves exactly like an ordinary Job.
	RetryOfJobID     *uint64 `gorm:"column:retry_of_job_id"`
	RetryOfNodeName  *string `gorm:"column:retry_of_node_name"`
	RetryOfLoopIndex *int    `gorm:"column:retry_of_loop_index"`
	WorkflowName     string  `gorm:"column:workflow_name"`
	WorkflowRunID    string  `gorm:"column:workflow_run_id"`
	Title            string
	Status           string
	Spec             []byte `gorm:"column:spec;type:json"`

	NodeTotal  int `gorm:"column:node_total"`
	NodeDone   int `gorm:"column:node_done"`
	NodeFailed int `gorm:"column:node_failed"`

	CreditEstimated int `gorm:"column:credit_estimated"`
	CreditHeld      int `gorm:"column:credit_held"`
	CreditSettled   int `gorm:"column:credit_settled"`

	IdemKey    *string    `gorm:"column:idem_key"`
	ErrorCode  string     `gorm:"column:error_code"`
	ErrorMsg   string     `gorm:"column:error_msg"`
	StartedAt  *time.Time `gorm:"column:started_at"`
	FinishedAt *time.Time `gorm:"column:finished_at"`
	CreatedAt  time.Time  `gorm:"column:created_at"`
	UpdatedAt  time.Time  `gorm:"column:updated_at"`
}

func (Job) TableName() string { return "jobs" }

// JobNode mirrors the `job_nodes` projection table.
type JobNode struct {
	ID           uint64 `gorm:"primaryKey"`
	JobID        uint64 `gorm:"column:job_id"`
	TaskRunID    string `gorm:"column:task_run_id"`
	NodeName     string `gorm:"column:node_name"`
	LoopIndex    int    `gorm:"column:loop_index"`
	ParentScope  string `gorm:"column:parent_scope"`
	ExecutorType string `gorm:"column:executor_type"`
	Phase        string
	ExecCode     *int8      `gorm:"column:exec_code"`
	AssetIDs     []byte     `gorm:"column:asset_ids;type:json"`
	CreditCost   int        `gorm:"column:credit_cost"`
	ErrorMsg     string     `gorm:"column:error_msg"`
	StartedAt    *time.Time `gorm:"column:started_at"`
	FinishedAt   *time.Time `gorm:"column:finished_at"`
	UpdatedAt    time.Time  `gorm:"column:updated_at"`
}

func (JobNode) TableName() string { return "job_nodes" }

// Character mirrors the `characters` table (F3).
type Character struct {
	ID          uint64 `gorm:"primaryKey"`
	BizID       string `gorm:"column:biz_id"`
	UserID      uint64 `gorm:"column:user_id"`
	ProjectID   *uint64
	Name        string
	Description string
	RefAssetIDs []byte `gorm:"column:ref_asset_ids;type:json"` // []string of asset biz_ids
	Seed        int64
	DeletedAt   *time.Time `gorm:"column:deleted_at"`
	CreatedAt   time.Time  `gorm:"column:created_at"`
	UpdatedAt   time.Time  `gorm:"column:updated_at"`
}

func (Character) TableName() string { return "characters" }

// Preset mirrors the `presets` table (F4). POC only reads these (seeded by
// migration) — F4.5 (user-defined presets) is P2, not built.
type Preset struct {
	ID             uint64 `gorm:"primaryKey"`
	BizID          string `gorm:"column:biz_id"`
	Category       string
	Name           string
	CoverURL       string `gorm:"column:cover_url"`
	PromptFragment string `gorm:"column:prompt_fragment"`
	Priority       int
	StyleType      string    `gorm:"column:style_type"`
	CreatedAt      time.Time `gorm:"column:created_at"`
}

func (Preset) TableName() string { return "presets" }

// ProviderFile mirrors the `provider_files` table (F3.4/§9.2): caches the
// MiniMax file_id an asset was uploaded as, so the same asset is never
// re-uploaded.
type ProviderFile struct {
	ID           uint64 `gorm:"primaryKey"`
	AssetID      uint64 `gorm:"column:asset_id"`
	ProviderCode string `gorm:"column:provider_code"`
	AccountID    uint64 `gorm:"column:account_id"`
	FileID       string `gorm:"column:file_id"`
	Purpose      string
	ExpireAt     *time.Time `gorm:"column:expire_at"`
	CreatedAt    time.Time  `gorm:"column:created_at"`
}

func (ProviderFile) TableName() string { return "provider_files" }

// Project mirrors the `projects` table (migrations/00007_projects.sql).
// assets.project_id references this by numeric id; jobs/characters keep
// their own project_id columns unassigned for now — see the migration's
// own doc for the scoping rationale.
type Project struct {
	ID          uint64 `gorm:"primaryKey"`
	BizID       string `gorm:"column:biz_id"`
	UserID      uint64 `gorm:"column:user_id"`
	Name        string
	Description string
	DeletedAt   *time.Time `gorm:"column:deleted_at"`
	CreatedAt   time.Time  `gorm:"column:created_at"`
	UpdatedAt   time.Time  `gorm:"column:updated_at"`
}

func (Project) TableName() string { return "projects" }
