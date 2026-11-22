package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/YASSERRMD/GatewayForge/internal/control/store"
	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

// PolicyRecord is a persisted policy definition.
type PolicyRecord struct {
	ID            string
	Name          string
	PolicyType    string
	SchemaVersion int
	Config        json.RawMessage
	Enabled       bool
}

// AttachmentRecord links a policy to org, service, or route scope.
type AttachmentRecord struct {
	PolicyID  string
	ScopeType string // org, service, route
	ServiceID string
	RouteID   string
	Priority  int
}

// ConsumerRecord holds a consumer and its non-revoked hashed credentials.
type ConsumerRecord struct {
	ID     string
	Name   string
	Status string
	Keys   []snapshot.APIKey
}

// DraftInput contains all raw tenant resources required to compile a deterministic snapshot.
type DraftInput struct {
	OrgID         string
	DraftRevision int64
	Services      map[string]store.Service
	Pools         []store.UpstreamPool
	TargetsByPool map[string][]store.UpstreamTarget
	Routes        []store.Route
	Policies      map[string]PolicyRecord
	Attachments   []AttachmentRecord
	Consumers     []ConsumerRecord
}

// Compile produces a canonically sorted, checksummed immutable snapshot.Snapshot.
func Compile(in DraftInput, version int, publishedAt time.Time) (snapshot.Snapshot, error) {
	if in.OrgID == "" {
		return snapshot.Snapshot{}, fmt.Errorf("org_id is required")
	}

	// 1. Canonical sorted pools & targets.
	pools := make([]snapshot.UpstreamPool, 0, len(in.Pools))
	for _, p := range in.Pools {
		rawTargets := in.TargetsByPool[p.ID]
		targets := make([]snapshot.UpstreamTarget, 0, len(rawTargets))
		for _, t := range rawTargets {
			targets = append(targets, snapshot.UpstreamTarget{
				ID:      t.ID,
				URL:     strings.TrimSpace(t.URL),
				Weight:  t.Weight,
				Enabled: t.Enabled,
			})
		}
		sort.Slice(targets, func(i, j int) bool { return targets[i].ID < targets[j].ID })
		pools = append(pools, snapshot.UpstreamPool{
			ID:                    p.ID,
			Name:                  p.Name,
			DestinationClass:      p.DestinationClass,
			ConnectTimeoutMS:      p.ConnectTimeoutMS,
			ReadTimeoutMS:         p.ReadTimeoutMS,
			HealthCheckPath:       p.HealthCheckPath,
			HealthCheckIntervalMS: p.HealthCheckIntervalMS,
			HealthCheckTimeoutMS:  p.HealthCheckTimeoutMS,
			Targets:               targets,
		})
	}
	sort.Slice(pools, func(i, j int) bool { return pools[i].ID < pools[j].ID })

	// 2. Compile routes with resolved org/service/route policy attachments.
	routes := make([]snapshot.Route, 0, len(in.Routes))
	for _, r := range in.Routes {
		svc := in.Services[r.ServiceID]
		methods := append([]string(nil), r.Methods...)
		for i := range methods {
			methods[i] = strings.ToUpper(strings.TrimSpace(methods[i]))
		}
		sort.Strings(methods)

		attached := resolvePoliciesForRoute(r, in.Policies, in.Attachments)

		routes = append(routes, snapshot.Route{
			ID:             r.ID,
			OrgID:          in.OrgID,
			ServiceID:      r.ServiceID,
			ServiceSlug:    svc.Slug,
			UpstreamPoolID: r.UpstreamPoolID,
			Name:           r.Name,
			Host:           strings.ToLower(strings.TrimSpace(r.Host)),
			PathPrefix:     strings.TrimSpace(r.PathPrefix),
			Methods:        methods,
			StripPrefix:    r.StripPrefix,
			Priority:       r.Priority,
			Enabled:        r.Enabled && (svc.ID == "" || (svc.Enabled && svc.DeletedAt == nil)),
			Policies:       attached,
		})
	}
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Priority != routes[j].Priority {
			return routes[i].Priority > routes[j].Priority
		}
		if len(routes[i].PathPrefix) != len(routes[j].PathPrefix) {
			return len(routes[i].PathPrefix) > len(routes[j].PathPrefix)
		}
		return routes[i].ID < routes[j].ID
	})

	// 3. Compile consumers and hashed keys deterministically.
	consumers := make([]snapshot.Consumer, 0, len(in.Consumers))
	for _, c := range in.Consumers {
		keys := make([]snapshot.APIKey, 0, len(c.Keys))
		for _, k := range c.Keys {
			scopes := append([]string(nil), k.Scopes...)
			sort.Strings(scopes)
			kc := k
			kc.Scopes = scopes
			keys = append(keys, kc)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i].ID < keys[j].ID })
		consumers = append(consumers, snapshot.Consumer{
			ID:     c.ID,
			Name:   c.Name,
			Status: c.Status,
			Keys:   keys,
		})
	}
	sort.Slice(consumers, func(i, j int) bool { return consumers[i].ID < consumers[j].ID })

	snap := snapshot.Snapshot{
		SchemaVersion: snapshot.SchemaVersion,
		OrgID:         in.OrgID,
		Version:       version,
		PublishedAt:   publishedAt.UTC().Truncate(time.Second),
		Pools:         pools,
		Routes:        routes,
		Consumers:     consumers,
	}
	sum, err := snap.ComputeChecksum()
	if err != nil {
		return snapshot.Snapshot{}, err
	}
	snap.Checksum = sum
	return snap, nil
}

func resolvePoliciesForRoute(r store.Route, policies map[string]PolicyRecord, attachments []AttachmentRecord) []snapshot.AttachedPolicy {
	out := make([]snapshot.AttachedPolicy, 0)
	for _, att := range attachments {
		pol, ok := policies[att.PolicyID]
		if !ok || !pol.Enabled {
			continue
		}
		applies := false
		switch att.ScopeType {
		case "org":
			applies = true
		case "service":
			applies = att.ServiceID == r.ServiceID
		case "route":
			applies = att.RouteID == r.ID
		}
		if !applies {
			continue
		}
		out = append(out, snapshot.AttachedPolicy{
			ID:            pol.ID,
			Name:          pol.Name,
			PolicyType:    pol.PolicyType,
			SchemaVersion: pol.SchemaVersion,
			ScopeType:     att.ScopeType,
			Priority:      att.Priority,
			Config:        pol.Config,
		})
	}
	scopeRank := func(s string) int {
		switch s {
		case "route":
			return 3
		case "service":
			return 2
		default:
			return 1
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if scopeRank(out[i].ScopeType) != scopeRank(out[j].ScopeType) {
			return scopeRank(out[i].ScopeType) > scopeRank(out[j].ScopeType)
		}
		if out[i].Priority != out[j].Priority {
			return out[i].Priority > out[j].Priority
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// LoadDraftFromDB reads all persisted tenant draft entities from PostgreSQL.
func LoadDraftFromDB(ctx context.Context, pool *pgxpool.Pool, orgID string) (DraftInput, error) {
	in := DraftInput{
		OrgID:         orgID,
		DraftRevision: 1,
		Services:      make(map[string]store.Service),
		TargetsByPool: make(map[string][]store.UpstreamTarget),
		Policies:      make(map[string]PolicyRecord),
	}

	_ = pool.QueryRow(ctx, `SELECT revision FROM draft_revisions WHERE org_id = $1`, orgID).Scan(&in.DraftRevision)

	// Services
	sRows, err := pool.Query(ctx, `
		SELECT id, org_id, env_id, slug, name, description, enabled, revision, deleted_at, created_at, updated_at
		FROM services WHERE org_id = $1 AND deleted_at IS NULL
	`, orgID)
	if err != nil {
		return in, fmt.Errorf("load services: %w", err)
	}
	for sRows.Next() {
		var s store.Service
		if err := sRows.Scan(&s.ID, &s.OrgID, &s.EnvID, &s.Slug, &s.Name, &s.Description, &s.Enabled, &s.Revision, &s.DeletedAt, &s.CreatedAt, &s.UpdatedAt); err != nil {
			sRows.Close()
			return in, err
		}
		in.Services[s.ID] = s
	}
	sRows.Close()

	// Upstream pools
	pRows, err := pool.Query(ctx, `
		SELECT id, org_id, env_id, name, destination_class,
		       connect_timeout_ms, read_timeout_ms,
		       health_check_path, health_check_interval_ms, health_check_timeout_ms,
		       revision, created_at, updated_at
		FROM upstream_pools WHERE org_id = $1
	`, orgID)
	if err != nil {
		return in, fmt.Errorf("load pools: %w", err)
	}
	for pRows.Next() {
		var p store.UpstreamPool
		if err := pRows.Scan(
			&p.ID, &p.OrgID, &p.EnvID, &p.Name, &p.DestinationClass,
			&p.ConnectTimeoutMS, &p.ReadTimeoutMS,
			&p.HealthCheckPath, &p.HealthCheckIntervalMS, &p.HealthCheckTimeoutMS,
			&p.Revision, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			pRows.Close()
			return in, err
		}
		in.Pools = append(in.Pools, p)
	}
	pRows.Close()

	// Upstream targets
	tRows, err := pool.Query(ctx, `
		SELECT id, org_id, pool_id, url, weight, enabled, created_at, updated_at
		FROM upstream_targets WHERE org_id = $1
	`, orgID)
	if err != nil {
		return in, fmt.Errorf("load targets: %w", err)
	}
	for tRows.Next() {
		var t store.UpstreamTarget
		if err := tRows.Scan(&t.ID, &t.OrgID, &t.PoolID, &t.URL, &t.Weight, &t.Enabled, &t.CreatedAt, &t.UpdatedAt); err != nil {
			tRows.Close()
			return in, err
		}
		in.TargetsByPool[t.PoolID] = append(in.TargetsByPool[t.PoolID], t)
	}
	tRows.Close()

	// Routes
	rRows, err := pool.Query(ctx, `
		SELECT id, org_id, env_id, service_id, upstream_pool_id,
		       name, host, path_prefix, methods, strip_prefix, priority, enabled,
		       revision, created_at, updated_at
		FROM routes WHERE org_id = $1
	`, orgID)
	if err != nil {
		return in, fmt.Errorf("load routes: %w", err)
	}
	for rRows.Next() {
		var r store.Route
		if err := rRows.Scan(
			&r.ID, &r.OrgID, &r.EnvID, &r.ServiceID, &r.UpstreamPoolID,
			&r.Name, &r.Host, &r.PathPrefix, &r.Methods, &r.StripPrefix, &r.Priority, &r.Enabled,
			&r.Revision, &r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			rRows.Close()
			return in, err
		}
		in.Routes = append(in.Routes, r)
	}
	rRows.Close()

	// Policies & attachments
	polRows, err := pool.Query(ctx, `
		SELECT id, name, policy_type, schema_version, config, enabled
		FROM policies WHERE org_id = $1
	`, orgID)
	if err != nil {
		return in, fmt.Errorf("load policies: %w", err)
	}
	for polRows.Next() {
		var p PolicyRecord
		if err := polRows.Scan(&p.ID, &p.Name, &p.PolicyType, &p.SchemaVersion, &p.Config, &p.Enabled); err != nil {
			polRows.Close()
			return in, err
		}
		in.Policies[p.ID] = p
	}
	polRows.Close()

	attRows, err := pool.Query(ctx, `
		SELECT policy_id, scope_type, COALESCE(service_id::TEXT, ''), COALESCE(route_id::TEXT, ''), priority
		FROM policy_attachments WHERE org_id = $1
	`, orgID)
	if err != nil {
		return in, fmt.Errorf("load policy attachments: %w", err)
	}
	for attRows.Next() {
		var a AttachmentRecord
		if err := attRows.Scan(&a.PolicyID, &a.ScopeType, &a.ServiceID, &a.RouteID, &a.Priority); err != nil {
			attRows.Close()
			return in, err
		}
		in.Attachments = append(in.Attachments, a)
	}
	attRows.Close()

	// Consumers and active (non-revoked) hashed API keys
	cRows, err := pool.Query(ctx, `
		SELECT id, name, status FROM consumers WHERE org_id = $1
	`, orgID)
	if err != nil {
		return in, fmt.Errorf("load consumers: %w", err)
	}
	consumerIdx := make(map[string]int)
	for cRows.Next() {
		var c ConsumerRecord
		if err := cRows.Scan(&c.ID, &c.Name, &c.Status); err != nil {
			cRows.Close()
			return in, err
		}
		consumerIdx[c.ID] = len(in.Consumers)
		in.Consumers = append(in.Consumers, c)
	}
	cRows.Close()

	kRows, err := pool.Query(ctx, `
		SELECT id, consumer_id, key_prefix, key_hash, scopes, expires_at
		FROM api_keys
		WHERE org_id = $1 AND revoked_at IS NULL
	`, orgID)
	if err != nil {
		return in, fmt.Errorf("load api_keys: %w", err)
	}
	for kRows.Next() {
		var k snapshot.APIKey
		var cid string
		if err := kRows.Scan(&k.ID, &cid, &k.KeyPrefix, &k.KeyHash, &k.Scopes, &k.ExpiresAt); err != nil {
			kRows.Close()
			return in, err
		}
		if idx, ok := consumerIdx[cid]; ok {
			in.Consumers[idx].Keys = append(in.Consumers[idx].Keys, k)
		}
	}
	kRows.Close()

	return in, nil
}
