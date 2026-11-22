package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// FixtureSummary reports the deterministic fixture identifiers created or reused by SeedDemoFixtures.
type FixtureSummary struct {
	PrimaryOrgID      string
	SecondaryOrgID    string
	DevEnvID          string
	EchoServiceID     string
	CatalogServiceID  string
	EchoPoolID        string
	EchoRouteID       string
	CatalogRouteID    string
	DemoConsumerID    string
	RateLimitPolicyID string
	TimeoutPolicyID   string
	CORSPolicyID      string
}

// SeedDemoFixtures idempotently populates two isolated organizations, mock services, routes,
// a demo consumer, and realistic policies for local development and integration testing.
// Contains zero production credentials or plaintext API keys.
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

		catalogSvc, err := tx.ensureService(ctx, org1.ID, env1.ID, "catalog-service", "Demo Catalog API", "Rate-limited demo catalog API backed by mock upstream")
		if err != nil {
			return err
		}
		summary.CatalogServiceID = catalogSvc.ID

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

		catalogRoute, err := tx.ensureRoute(ctx, Route{
			OrgID:          org1.ID,
			EnvID:          env1.ID,
			ServiceID:      catalogSvc.ID,
			UpstreamPoolID: pool.ID,
			Name:           "catalog-public",
			Host:           "localhost",
			PathPrefix:     "/demo/catalog",
			Methods:        []string{"GET"},
			StripPrefix:    false,
			Priority:       100,
			Enabled:        true,
		})
		if err != nil {
			return err
		}
		summary.CatalogRouteID = catalogRoute.ID

		consumerID, err := tx.ensureConsumer(ctx, org1.ID, "demo-partner-app", "Synthetic local demo partner consumer (issue keys via Studio or API)", "partner-demo@example.invalid")
		if err != nil {
			return err
		}
		summary.DemoConsumerID = consumerID

		rlPolID, err := tx.ensurePolicy(ctx, org1.ID, "demo-catalog-ratelimit", "rate_limit", map[string]any{
			"algorithm":           "token_bucket",
			"key_by":              "ip",
			"requests_per_window": 120,
			"window_seconds":      60,
			"burst":               120,
			"fail_mode":           "fail_open",
			"security_class":      "standard",
		})
		if err != nil {
			return err
		}
		summary.RateLimitPolicyID = rlPolID
		if err := tx.ensurePolicyAttachment(ctx, org1.ID, rlPolID, "route", nil, &catalogRoute.ID, 100); err != nil {
			return err
		}

		toPolID, err := tx.ensurePolicy(ctx, org1.ID, "demo-default-timeout", "timeout", map[string]any{
			"connect_timeout_ms": 1500,
			"total_timeout_ms":   5000,
		})
		if err != nil {
			return err
		}
		summary.TimeoutPolicyID = toPolID
		if err := tx.ensurePolicyAttachment(ctx, org1.ID, toPolID, "org", nil, nil, 100); err != nil {
			return err
		}

		corsPolID, err := tx.ensurePolicy(ctx, org1.ID, "demo-studio-cors", "cors", map[string]any{
			"allowed_origins":   []string{"http://localhost:3000"},
			"allowed_methods":   []string{"GET", "POST", "OPTIONS"},
			"allowed_headers":   []string{"Content-Type", "X-API-Key", "X-Request-Id"},
			"allow_credentials": false,
			"max_age_seconds":   300,
		})
		if err != nil {
			return err
		}
		summary.CORSPolicyID = corsPolID
		if err := tx.ensurePolicyAttachment(ctx, org1.ID, corsPolID, "org", nil, nil, 100); err != nil {
			return err
		}

		if _, err := tx.q.Exec(ctx, `
			INSERT INTO draft_revisions (org_id, revision) VALUES ($1, 1)
			ON CONFLICT (org_id) DO NOTHING
		`, org1.ID); err != nil {
			return fmt.Errorf("ensure draft_revisions: %w", err)
		}

		payload, _ := json.Marshal(map[string]string{
			"org_slug":           org1.Slug,
			"service_id":         svc.ID,
			"catalog_service_id": catalogSvc.ID,
			"route_id":           route.ID,
			"catalog_route_id":   catalogRoute.ID,
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

func (s *Store) ensureConsumer(ctx context.Context, orgID, name, desc, ownerEmail string) (string, error) {
	var id string
	err := s.q.QueryRow(ctx, `
		SELECT id FROM consumers WHERE org_id = $1 AND name = $2
	`, orgID, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(mapPGError(err), ErrNotFound) {
		return "", mapPGError(err)
	}
	err = s.q.QueryRow(ctx, `
		INSERT INTO consumers (org_id, name, description, owner_email, status)
		VALUES ($1, $2, $3, $4, 'active')
		RETURNING id
	`, orgID, name, desc, ownerEmail).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("seed consumer: %w", mapPGError(err))
	}
	return id, nil
}

func (s *Store) ensurePolicy(ctx context.Context, orgID, name, policyType string, cfg map[string]any) (string, error) {
	var id string
	err := s.q.QueryRow(ctx, `
		SELECT id FROM policies WHERE org_id = $1 AND name = $2
	`, orgID, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(mapPGError(err), ErrNotFound) {
		return "", mapPGError(err)
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshal policy %s: %w", name, err)
	}
	err = s.q.QueryRow(ctx, `
		INSERT INTO policies (org_id, name, policy_type, schema_version, config, enabled)
		VALUES ($1, $2, $3, 1, $4, TRUE)
		RETURNING id
	`, orgID, name, policyType, raw).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("seed policy %s: %w", name, mapPGError(err))
	}
	return id, nil
}

func (s *Store) ensurePolicyAttachment(ctx context.Context, orgID, policyID, scopeType string, serviceID, routeID *string, priority int) error {
	_, err := s.q.Exec(ctx, `
		INSERT INTO policy_attachments (org_id, policy_id, scope_type, service_id, route_id, priority)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT DO NOTHING
	`, orgID, policyID, scopeType, serviceID, routeID, priority)
	if err != nil {
		return fmt.Errorf("seed policy attachment: %w", mapPGError(err))
	}
	return nil
}
