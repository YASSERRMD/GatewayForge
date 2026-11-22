package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Querier abstracts *pgxpool.Pool and pgx.Tx for transaction-safe repository methods.
type Querier interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Store provides typed, tenant-scoped PostgreSQL persistence operations.
type Store struct {
	pool *pgxpool.Pool
	q    Querier
}

// New creates a Store backed by pool.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, q: pool}
}

// Pool exposes the underlying pgxpool for health checks or advanced queries.
func (s *Store) Pool() *pgxpool.Pool {
	return s.pool
}

// WithTx executes fn inside a PostgreSQL transaction, rolling back on error or panic.
func (s *Store) WithTx(ctx context.Context, fn func(txStore *Store) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	txStore := &Store{pool: s.pool, q: tx}
	if err := fn(txStore); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	committed = true
	return nil
}

func mapPGError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505": // unique_violation
			return fmt.Errorf("%w: %s", ErrConflict, pgErr.ConstraintName)
		case "23503", "23514": // foreign_key_violation, check_violation
			return fmt.Errorf("constraint violation (%s): %w", pgErr.ConstraintName, err)
		}
	}
	return err
}

// CreateOrganization creates an organization and initializes its draft revision row.
func (s *Store) CreateOrganization(ctx context.Context, slug, displayName string) (Organization, error) {
	var org Organization
	err := s.q.QueryRow(ctx, `
		INSERT INTO organizations (slug, display_name)
		VALUES ($1, $2)
		RETURNING id, slug, display_name, enabled, revision, created_at, updated_at
	`, slug, displayName).Scan(
		&org.ID, &org.Slug, &org.DisplayName, &org.Enabled, &org.Revision, &org.CreatedAt, &org.UpdatedAt,
	)
	if err != nil {
		return Organization{}, mapPGError(err)
	}
	if _, err := s.q.Exec(ctx, `
		INSERT INTO draft_revisions (org_id, revision) VALUES ($1, 1) ON CONFLICT (org_id) DO NOTHING
	`, org.ID); err != nil {
		return Organization{}, mapPGError(err)
	}
	return org, nil
}

// GetOrganization retrieves an organization by ID.
func (s *Store) GetOrganization(ctx context.Context, orgID string) (Organization, error) {
	var org Organization
	err := s.q.QueryRow(ctx, `
		SELECT id, slug, display_name, enabled, revision, created_at, updated_at
		FROM organizations
		WHERE id = $1
	`, orgID).Scan(
		&org.ID, &org.Slug, &org.DisplayName, &org.Enabled, &org.Revision, &org.CreatedAt, &org.UpdatedAt,
	)
	if err != nil {
		return Organization{}, mapPGError(err)
	}
	return org, nil
}

// UpdateOrganization updates display_name/enabled using optimistic revision checking.
func (s *Store) UpdateOrganization(ctx context.Context, orgID, displayName string, enabled bool, expectedRevision int64) (Organization, error) {
	var org Organization
	err := s.q.QueryRow(ctx, `
		UPDATE organizations
		SET display_name = $2, enabled = $3, revision = revision + 1, updated_at = NOW()
		WHERE id = $1 AND revision = $4
		RETURNING id, slug, display_name, enabled, revision, created_at, updated_at
	`, orgID, displayName, enabled, expectedRevision).Scan(
		&org.ID, &org.Slug, &org.DisplayName, &org.Enabled, &org.Revision, &org.CreatedAt, &org.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		_ = s.q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organizations WHERE id = $1)`, orgID).Scan(&exists)
		if exists {
			return Organization{}, ErrConflict
		}
		return Organization{}, ErrNotFound
	}
	if err != nil {
		return Organization{}, mapPGError(err)
	}
	return org, nil
}

// CreateEnvironment creates a tenant-scoped environment.
func (s *Store) CreateEnvironment(ctx context.Context, orgID, slug, displayName string, isProd bool) (Environment, error) {
	var env Environment
	err := s.q.QueryRow(ctx, `
		INSERT INTO environments (org_id, slug, display_name, is_production)
		VALUES ($1, $2, $3, $4)
		RETURNING id, org_id, slug, display_name, is_production, created_at, updated_at
	`, orgID, slug, displayName, isProd).Scan(
		&env.ID, &env.OrgID, &env.Slug, &env.DisplayName, &env.IsProduction, &env.CreatedAt, &env.UpdatedAt,
	)
	return env, mapPGError(err)
}

// CreateService creates a tenant-scoped service inside an environment.
func (s *Store) CreateService(ctx context.Context, orgID, envID, slug, name, description string) (Service, error) {
	var svc Service
	err := s.q.QueryRow(ctx, `
		INSERT INTO services (org_id, env_id, slug, name, description)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, org_id, env_id, slug, name, description, enabled, revision, deleted_at, created_at, updated_at
	`, orgID, envID, slug, name, description).Scan(
		&svc.ID, &svc.OrgID, &svc.EnvID, &svc.Slug, &svc.Name, &svc.Description,
		&svc.Enabled, &svc.Revision, &svc.DeletedAt, &svc.CreatedAt, &svc.UpdatedAt,
	)
	return svc, mapPGError(err)
}

// GetService fetches an active service strictly within orgID.
func (s *Store) GetService(ctx context.Context, orgID, serviceID string) (Service, error) {
	var svc Service
	err := s.q.QueryRow(ctx, `
		SELECT id, org_id, env_id, slug, name, description, enabled, revision, deleted_at, created_at, updated_at
		FROM services
		WHERE org_id = $1 AND id = $2 AND deleted_at IS NULL
	`, orgID, serviceID).Scan(
		&svc.ID, &svc.OrgID, &svc.EnvID, &svc.Slug, &svc.Name, &svc.Description,
		&svc.Enabled, &svc.Revision, &svc.DeletedAt, &svc.CreatedAt, &svc.UpdatedAt,
	)
	return svc, mapPGError(err)
}

// ListServices lists active services scoped strictly to orgID.
func (s *Store) ListServices(ctx context.Context, orgID string, limit, offset int) ([]Service, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.q.Query(ctx, `
		SELECT id, org_id, env_id, slug, name, description, enabled, revision, deleted_at, created_at, updated_at
		FROM services
		WHERE org_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC, id ASC
		LIMIT $2 OFFSET $3
	`, orgID, limit, offset)
	if err != nil {
		return nil, mapPGError(err)
	}
	defer rows.Close()

	var out []Service
	for rows.Next() {
		var svc Service
		if err := rows.Scan(
			&svc.ID, &svc.OrgID, &svc.EnvID, &svc.Slug, &svc.Name, &svc.Description,
			&svc.Enabled, &svc.Revision, &svc.DeletedAt, &svc.CreatedAt, &svc.UpdatedAt,
		); err != nil {
			return nil, mapPGError(err)
		}
		out = append(out, svc)
	}
	return out, mapPGError(rows.Err())
}

// CreateUpstreamPool creates an upstream pool in an org/env.
func (s *Store) CreateUpstreamPool(ctx context.Context, p UpstreamPool) (UpstreamPool, error) {
	if p.DestinationClass == "" {
		p.DestinationClass = "internal_approved"
	}
	if p.ConnectTimeoutMS == 0 {
		p.ConnectTimeoutMS = 3000
	}
	if p.ReadTimeoutMS == 0 {
		p.ReadTimeoutMS = 15000
	}
	if p.HealthCheckPath == "" {
		p.HealthCheckPath = "/health"
	}
	if p.HealthCheckIntervalMS == 0 {
		p.HealthCheckIntervalMS = 10000
	}
	if p.HealthCheckTimeoutMS == 0 {
		p.HealthCheckTimeoutMS = 2000
	}
	var out UpstreamPool
	err := s.q.QueryRow(ctx, `
		INSERT INTO upstream_pools (
			org_id, env_id, name, destination_class,
			connect_timeout_ms, read_timeout_ms,
			health_check_path, health_check_interval_ms, health_check_timeout_ms
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING id, org_id, env_id, name, destination_class,
		          connect_timeout_ms, read_timeout_ms,
		          health_check_path, health_check_interval_ms, health_check_timeout_ms,
		          revision, created_at, updated_at
	`,
		p.OrgID, p.EnvID, p.Name, p.DestinationClass,
		p.ConnectTimeoutMS, p.ReadTimeoutMS,
		p.HealthCheckPath, p.HealthCheckIntervalMS, p.HealthCheckTimeoutMS,
	).Scan(
		&out.ID, &out.OrgID, &out.EnvID, &out.Name, &out.DestinationClass,
		&out.ConnectTimeoutMS, &out.ReadTimeoutMS,
		&out.HealthCheckPath, &out.HealthCheckIntervalMS, &out.HealthCheckTimeoutMS,
		&out.Revision, &out.CreatedAt, &out.UpdatedAt,
	)
	return out, mapPGError(err)
}

// AddUpstreamTarget attaches a weighted target URL to a pool within orgID.
func (s *Store) AddUpstreamTarget(ctx context.Context, orgID, poolID, targetURL string, weight int, enabled bool) (UpstreamTarget, error) {
	var out UpstreamTarget
	err := s.q.QueryRow(ctx, `
		INSERT INTO upstream_targets (org_id, pool_id, url, weight, enabled)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, org_id, pool_id, url, weight, enabled, created_at, updated_at
	`, orgID, poolID, targetURL, weight, enabled).Scan(
		&out.ID, &out.OrgID, &out.PoolID, &out.URL, &out.Weight, &out.Enabled, &out.CreatedAt, &out.UpdatedAt,
	)
	return out, mapPGError(err)
}

// CreateRoute creates a tenant-scoped route mapping.
func (s *Store) CreateRoute(ctx context.Context, r Route) (Route, error) {
	var out Route
	err := s.q.QueryRow(ctx, `
		INSERT INTO routes (
			org_id, env_id, service_id, upstream_pool_id,
			name, host, path_prefix, methods, strip_prefix, priority, enabled
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		RETURNING id, org_id, env_id, service_id, upstream_pool_id,
		          name, host, path_prefix, methods, strip_prefix, priority, enabled,
		          revision, created_at, updated_at
	`,
		r.OrgID, r.EnvID, r.ServiceID, r.UpstreamPoolID,
		r.Name, r.Host, r.PathPrefix, r.Methods, r.StripPrefix, r.Priority, r.Enabled,
	).Scan(
		&out.ID, &out.OrgID, &out.EnvID, &out.ServiceID, &out.UpstreamPoolID,
		&out.Name, &out.Host, &out.PathPrefix, &out.Methods, &out.StripPrefix, &out.Priority, &out.Enabled,
		&out.Revision, &out.CreatedAt, &out.UpdatedAt,
	)
	return out, mapPGError(err)
}

// AppendAuditEvent writes an immutable audit event record.
func (s *Store) AppendAuditEvent(ctx context.Context, ev AuditEvent) (AuditEvent, error) {
	var out AuditEvent
	err := s.q.QueryRow(ctx, `
		INSERT INTO audit_events (
			org_id, actor_user_id, actor_email, action, target_type, target_id,
			request_id, before_redacted, after_redacted
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING id, org_id, actor_user_id, actor_email, action, target_type, target_id,
		          request_id, before_redacted, after_redacted, created_at
	`,
		ev.OrgID, ev.ActorUserID, ev.ActorEmail, ev.Action, ev.TargetType, ev.TargetID,
		ev.RequestID, ev.BeforeRedacted, ev.AfterRedacted,
	).Scan(
		&out.ID, &out.OrgID, &out.ActorUserID, &out.ActorEmail, &out.Action, &out.TargetType, &out.TargetID,
		&out.RequestID, &out.BeforeRedacted, &out.AfterRedacted, &out.CreatedAt,
	)
	return out, mapPGError(err)
}
