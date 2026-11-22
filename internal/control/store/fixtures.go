package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// FixtureSummary reports the deterministic fixture identifiers created or reused by SeedDemoFixtures.
type FixtureSummary struct {
	PrimaryOrgID   string
	SecondaryOrgID string
	DevEnvID       string
	EchoServiceID  string
	EchoPoolID     string
	EchoRouteID    string
}

// SeedDemoFixtures idempotently populates two isolated organizations and a mock echo service/route
// for local development and integration testing. Contains zero production credentials.
func (s *Store) SeedDemoFixtures(ctx context.Context) (FixtureSummary, error) {
	var summary FixtureSummary
	err := s.WithTx(ctx, func(tx *Store) error {
		org1, err := tx.ensureOrg(ctx, "acme-demo", "Acme Demo Corp")
		if err != nil {
			return err
		}
		org2, err := tx.ensureOrg(ctx, "globex-demo", "Globex Sandbox")
		if err != nil {
			return err
		}
		summary.PrimaryOrgID = org1.ID
		summary.SecondaryOrgID = org2.ID

		env1, err := tx.ensureEnv(ctx, org1.ID, "dev", "Development", false)
		if err != nil {
			return err
		}
		if _, err := tx.ensureEnv(ctx, org2.ID, "dev", "Development", false); err != nil {
			return err
		}
		summary.DevEnvID = env1.ID

		svc, err := tx.ensureService(ctx, org1.ID, env1.ID, "echo-service", "Echo Service", "Deterministic local mock echo service")
		if err != nil {
			return err
		}
		summary.EchoServiceID = svc.ID

		pool, err := tx.ensurePool(ctx, org1.ID, env1.ID, "mock-echo-pool", "http://mock-upstream:8089")
		if err != nil {
			return err
		}
		summary.EchoPoolID = pool.ID

		route, err := tx.ensureRoute(ctx, Route{
			OrgID:          org1.ID,
			EnvID:          env1.ID,
			ServiceID:      svc.ID,
			UpstreamPoolID: pool.ID,
			Name:           "echo-default",
			Host:           "localhost",
			PathPrefix:     "/echo",
			Methods:        []string{"GET", "POST"},
			StripPrefix:    false,
			Priority:       100,
			Enabled:        true,
		})
		if err != nil {
			return err
		}
		summary.EchoRouteID = route.ID

		payload, _ := json.Marshal(map[string]string{
			"org_slug":   org1.Slug,
			"service_id": svc.ID,
			"route_id":   route.ID,
		})
		orgIDCopy := org1.ID
		if _, err := tx.AppendAuditEvent(ctx, AuditEvent{
			OrgID:         &orgIDCopy,
			ActorEmail:    "system-seed@gatewayforge.local",
			Action:        "fixtures.seed",
			TargetType:    "organization",
			TargetID:      org1.ID,
			RequestID:     "seed-demo-fixtures",
			AfterRedacted: payload,
		}); err != nil {
			return err
		}
		return nil
	})
	return summary, err
}

func (s *Store) ensureOrg(ctx context.Context, slug, displayName string) (Organization, error) {
	var org Organization
	err := s.q.QueryRow(ctx, `
		SELECT id, slug, display_name, enabled, revision, created_at, updated_at
		FROM organizations WHERE slug = $1
	`, slug).Scan(&org.ID, &org.Slug, &org.DisplayName, &org.Enabled, &org.Revision, &org.CreatedAt, &org.UpdatedAt)
	if err == nil {
		return org, nil
	}
	if !errors.Is(mapPGError(err), ErrNotFound) {
		return Organization{}, mapPGError(err)
	}
	return s.CreateOrganization(ctx, slug, displayName)
}

func (s *Store) ensureEnv(ctx context.Context, orgID, slug, displayName string, isProd bool) (Environment, error) {
	var env Environment
	err := s.q.QueryRow(ctx, `
		SELECT id, org_id, slug, display_name, is_production, created_at, updated_at
		FROM environments WHERE org_id = $1 AND slug = $2
	`, orgID, slug).Scan(&env.ID, &env.OrgID, &env.Slug, &env.DisplayName, &env.IsProduction, &env.CreatedAt, &env.UpdatedAt)
	if err == nil {
		return env, nil
	}
	if !errors.Is(mapPGError(err), ErrNotFound) {
		return Environment{}, mapPGError(err)
	}
	return s.CreateEnvironment(ctx, orgID, slug, displayName, isProd)
}

func (s *Store) ensureService(ctx context.Context, orgID, envID, slug, name, desc string) (Service, error) {
	var svc Service
	err := s.q.QueryRow(ctx, `
		SELECT id, org_id, env_id, slug, name, description, enabled, revision, deleted_at, created_at, updated_at
		FROM services WHERE org_id = $1 AND env_id = $2 AND slug = $3 AND deleted_at IS NULL
	`, orgID, envID, slug).Scan(
		&svc.ID, &svc.OrgID, &svc.EnvID, &svc.Slug, &svc.Name, &svc.Description,
		&svc.Enabled, &svc.Revision, &svc.DeletedAt, &svc.CreatedAt, &svc.UpdatedAt,
	)
	if err == nil {
		return svc, nil
	}
	if !errors.Is(mapPGError(err), ErrNotFound) {
		return Service{}, mapPGError(err)
	}
	return s.CreateService(ctx, orgID, envID, slug, name, desc)
}

func (s *Store) ensurePool(ctx context.Context, orgID, envID, name, targetURL string) (UpstreamPool, error) {
	var p UpstreamPool
	err := s.q.QueryRow(ctx, `
		SELECT id, org_id, env_id, name, destination_class,
		       connect_timeout_ms, read_timeout_ms,
		       health_check_path, health_check_interval_ms, health_check_timeout_ms,
		       revision, created_at, updated_at
		FROM upstream_pools WHERE org_id = $1 AND env_id = $2 AND name = $3
	`, orgID, envID, name).Scan(
		&p.ID, &p.OrgID, &p.EnvID, &p.Name, &p.DestinationClass,
		&p.ConnectTimeoutMS, &p.ReadTimeoutMS,
		&p.HealthCheckPath, &p.HealthCheckIntervalMS, &p.HealthCheckTimeoutMS,
		&p.Revision, &p.CreatedAt, &p.UpdatedAt,
	)
	if err == nil {
		return p, nil
	}
	if !errors.Is(mapPGError(err), ErrNotFound) {
		return UpstreamPool{}, mapPGError(err)
	}
	created, err := s.CreateUpstreamPool(ctx, UpstreamPool{
		OrgID:            orgID,
		EnvID:            envID,
		Name:             name,
		DestinationClass: "mock_local",
	})
	if err != nil {
		return UpstreamPool{}, err
	}
	if _, err := s.AddUpstreamTarget(ctx, orgID, created.ID, targetURL, 100, true); err != nil {
		return UpstreamPool{}, fmt.Errorf("seed target: %w", err)
	}
	return created, nil
}

func (s *Store) ensureRoute(ctx context.Context, r Route) (Route, error) {
	var out Route
	err := s.q.QueryRow(ctx, `
		SELECT id, org_id, env_id, service_id, upstream_pool_id,
		       name, host, path_prefix, methods, strip_prefix, priority, enabled,
		       revision, created_at, updated_at
		FROM routes WHERE org_id = $1 AND env_id = $2 AND name = $3
	`, r.OrgID, r.EnvID, r.Name).Scan(
		&out.ID, &out.OrgID, &out.EnvID, &out.ServiceID, &out.UpstreamPoolID,
		&out.Name, &out.Host, &out.PathPrefix, &out.Methods, &out.StripPrefix, &out.Priority, &out.Enabled,
		&out.Revision, &out.CreatedAt, &out.UpdatedAt,
	)
	if err == nil {
		return out, nil
	}
	if !errors.Is(mapPGError(err), ErrNotFound) {
		return Route{}, mapPGError(err)
	}
	return s.CreateRoute(ctx, r)
}
