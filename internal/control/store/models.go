package store

import (
	"encoding/json"
	"errors"
	"time"
)

var (
	// ErrNotFound is returned when a requested resource does not exist in the caller's tenant scope.
	ErrNotFound = errors.New("resource not found")
	// ErrConflict is returned on uniqueness violations or optimistic revision mismatches.
	ErrConflict = errors.New("resource conflict")
)

type Organization struct {
	ID          string    `json:"id"`
	Slug        string    `json:"slug"`
	DisplayName string    `json:"display_name"`
	Enabled     bool      `json:"enabled"`
	Revision    int64     `json:"revision"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	DisplayName  string    `json:"display_name"`
	PasswordHash string    `json:"-"`
	IsActive     bool      `json:"is_active"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Membership struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"org_id"`
	UserID    string    `json:"user_id"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Environment struct {
	ID           string    `json:"id"`
	OrgID        string    `json:"org_id"`
	Slug         string    `json:"slug"`
	DisplayName  string    `json:"display_name"`
	IsProduction bool      `json:"is_production"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Service struct {
	ID          string     `json:"id"`
	OrgID       string     `json:"org_id"`
	EnvID       string     `json:"env_id"`
	Slug        string     `json:"slug"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Enabled     bool       `json:"enabled"`
	Revision    int64      `json:"revision"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type UpstreamPool struct {
	ID                    string    `json:"id"`
	OrgID                 string    `json:"org_id"`
	EnvID                 string    `json:"env_id"`
	Name                  string    `json:"name"`
	DestinationClass      string    `json:"destination_class"`
	ConnectTimeoutMS      int       `json:"connect_timeout_ms"`
	ReadTimeoutMS         int       `json:"read_timeout_ms"`
	HealthCheckPath       string    `json:"health_check_path"`
	HealthCheckIntervalMS int       `json:"health_check_interval_ms"`
	HealthCheckTimeoutMS  int       `json:"health_check_timeout_ms"`
	Revision              int64     `json:"revision"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

type UpstreamTarget struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"org_id"`
	PoolID    string    `json:"pool_id"`
	URL       string    `json:"url"`
	Weight    int       `json:"weight"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Route struct {
	ID             string    `json:"id"`
	OrgID          string    `json:"org_id"`
	EnvID          string    `json:"env_id"`
	ServiceID      string    `json:"service_id"`
	UpstreamPoolID string    `json:"upstream_pool_id"`
	Name           string    `json:"name"`
	Host           string    `json:"host"`
	PathPrefix     string    `json:"path_prefix"`
	Methods        []string  `json:"methods"`
	StripPrefix    bool      `json:"strip_prefix"`
	Priority       int       `json:"priority"`
	Enabled        bool      `json:"enabled"`
	Revision       int64     `json:"revision"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type AuditEvent struct {
	ID             int64           `json:"id"`
	OrgID          *string         `json:"org_id,omitempty"`
	ActorUserID    *string         `json:"actor_user_id,omitempty"`
	ActorEmail     string          `json:"actor_email"`
	Action         string          `json:"action"`
	TargetType     string          `json:"target_type"`
	TargetID       string          `json:"target_id"`
	RequestID      string          `json:"request_id"`
	BeforeRedacted json.RawMessage `json:"before_redacted,omitempty"`
	AfterRedacted  json.RawMessage `json:"after_redacted,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
}
