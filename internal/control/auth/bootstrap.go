package auth

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/YASSERRMD/GatewayForge/internal/control/rbac"
	"github.com/YASSERRMD/GatewayForge/internal/shared/config"
)

var (
	// ErrBootstrapDisabled is returned when bootstrap is invoked in production mode.
	ErrBootstrapDisabled = errors.New("first-admin bootstrap is disabled in this environment")
	// ErrAlreadyBootstrapped is returned when initial admin provisioning has already completed.
	ErrAlreadyBootstrapped = errors.New("system is already bootstrapped")
	// ErrInvalidBootstrapInput indicates invalid email, password, or org slug.
	ErrInvalidBootstrapInput = errors.New("invalid bootstrap input")

	slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}[a-z0-9]$`)
)

// PasswordHasher abstracts password hashing and constant-time verification.
type PasswordHasher interface {
	Hash(password string) (string, error)
	Verify(password, encodedHash string) (bool, error)
}

// BootstrapInput describes the operator-supplied initial administrator and organization.
type BootstrapInput struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
	OrgSlug     string `json:"org_slug"`
	OrgName     string `json:"org_name"`
}

// BootstrapResult returns the created initial user and organization identifiers (never secrets).
type BootstrapResult struct {
	UserID  string `json:"user_id"`
	Email   string `json:"email"`
	OrgID   string `json:"org_id"`
	OrgSlug string `json:"org_slug"`
	EnvID   string `json:"env_id"`
}

// BootstrapService manages one-time dev/test initial admin provisioning without default credentials.
type BootstrapService struct {
	pool   *pgxpool.Pool
	env    config.Environment
	hasher PasswordHasher
}

// NewBootstrapService constructs a BootstrapService.
func NewBootstrapService(pool *pgxpool.Pool, env config.Environment, hasher PasswordHasher) *BootstrapService {
	return &BootstrapService{pool: pool, env: env, hasher: hasher}
}

// IsBootstrapped reports whether initial admin bootstrap has completed.
func (b *BootstrapService) IsBootstrapped(ctx context.Context) (bool, error) {
	var exists bool
	err := b.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM system_bootstrap)`).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check bootstrap status: %w", err)
	}
	return exists, nil
}

// Bootstrap provisions the first organization, owner user, and default dev environment atomically.
// Concurrent calls are serialized via PostgreSQL primary-key uniqueness on system_bootstrap(singleton).
func (b *BootstrapService) Bootstrap(ctx context.Context, in BootstrapInput) (BootstrapResult, error) {
	if b.env == config.EnvProduction {
		return BootstrapResult{}, ErrBootstrapDisabled
	}
	if b.hasher == nil {
		return BootstrapResult{}, errors.New("password hasher is required")
	}

	email := strings.ToLower(strings.TrimSpace(in.Email))
	displayName := strings.TrimSpace(in.DisplayName)
	orgSlug := strings.ToLower(strings.TrimSpace(in.OrgSlug))
	orgName := strings.TrimSpace(in.OrgName)

	if !strings.Contains(email, "@") || len(email) < 5 || len(email) > 254 {
		return BootstrapResult{}, fmt.Errorf("%w: valid email is required", ErrInvalidBootstrapInput)
	}
	if len(displayName) < 1 || len(displayName) > 120 {
		return BootstrapResult{}, fmt.Errorf("%w: display_name must be 1..120 characters", ErrInvalidBootstrapInput)
	}
	if len(in.Password) < 12 || len(in.Password) > 128 {
		return BootstrapResult{}, fmt.Errorf("%w: password must be 12..128 characters", ErrInvalidBootstrapInput)
	}
	if !slugPattern.MatchString(orgSlug) {
		return BootstrapResult{}, fmt.Errorf("%w: org_slug must be 3..64 lowercase alphanumeric/hyphen characters", ErrInvalidBootstrapInput)
	}
	if len(orgName) < 1 || len(orgName) > 120 {
		return BootstrapResult{}, fmt.Errorf("%w: org_name must be 1..120 characters", ErrInvalidBootstrapInput)
	}

	pwHash, err := b.hasher.Hash(in.Password)
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("hash bootstrap password: %w", err)
	}

	tx, err := b.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("begin bootstrap tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	var already bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM system_bootstrap)`).Scan(&already); err != nil {
		return BootstrapResult{}, fmt.Errorf("check bootstrap lock: %w", err)
	}
	if already {
		return BootstrapResult{}, ErrAlreadyBootstrapped
	}

	var orgID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO organizations (slug, display_name) VALUES ($1, $2) RETURNING id
	`, orgSlug, orgName).Scan(&orgID); err != nil {
		return BootstrapResult{}, mapBootstrapPGError(err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO draft_revisions (org_id, revision) VALUES ($1, 1)
	`, orgID); err != nil {
		return BootstrapResult{}, mapBootstrapPGError(err)
	}

	var envID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO environments (org_id, slug, display_name, is_production)
		VALUES ($1, 'dev', 'Development', FALSE) RETURNING id
	`, orgID).Scan(&envID); err != nil {
		return BootstrapResult{}, mapBootstrapPGError(err)
	}

	var userID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO users (email, display_name, password_hash)
		VALUES ($1, $2, $3) RETURNING id
	`, email, displayName, pwHash).Scan(&userID); err != nil {
		return BootstrapResult{}, mapBootstrapPGError(err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO memberships (org_id, user_id, role) VALUES ($1, $2, $3)
	`, orgID, userID, string(rbac.RoleOwner)); err != nil {
		return BootstrapResult{}, mapBootstrapPGError(err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO system_bootstrap (singleton, initial_user_id, initial_org_id)
		VALUES (TRUE, $1, $2)
	`, userID, orgID); err != nil {
		return BootstrapResult{}, mapBootstrapPGError(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return BootstrapResult{}, mapBootstrapPGError(err)
	}
	committed = true

	return BootstrapResult{
		UserID:  userID,
		Email:   email,
		OrgID:   orgID,
		OrgSlug: orgSlug,
		EnvID:   envID,
	}, nil
}

func mapBootstrapPGError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		// 23505 unique_violation (singleton PK or email/slug collision under race)
		// 40001 serialization_failure (concurrent serializable bootstrap race)
		if pgErr.Code == "23505" || pgErr.Code == "40001" {
			return ErrAlreadyBootstrapped
		}
	}
	return err
}
