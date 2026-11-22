package api

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/YASSERRMD/GatewayForge/internal/control/auth"
	"github.com/YASSERRMD/GatewayForge/internal/control/rbac"
	"github.com/YASSERRMD/GatewayForge/internal/control/store"
	"github.com/YASSERRMD/GatewayForge/internal/shared/netsec"
)

// UpstreamHandler serves tenant-scoped upstream pool and target CRUD endpoints.
type UpstreamHandler struct {
	Store        *store.Store
	Sessions     *auth.SessionManager
	Audit        *AuditRecorder
	TargetPolicy netsec.TargetPolicy
}

// MountUpstreamRoutes registers /orgs/{orgId}/upstreams routes on v1.
func (h *UpstreamHandler) MountUpstreamRoutes(v1 chi.Router) {
	v1.With(RequireAuth(h.Sessions), RequireOrgContext(h.Sessions), AuditMutationsMiddleware(h.Audit)).Route("/orgs/{orgId}/upstreams", func(r chi.Router) {
		r.With(RequirePermission(rbac.PermConfigRead)).Get("/", h.handleListPools)
		r.With(RequirePermission(rbac.PermConfigWrite)).Post("/", h.handleCreatePool)
		r.With(RequirePermission(rbac.PermConfigRead)).Get("/health", h.handleUpstreamHealthDiagnostics)
		r.With(RequirePermission(rbac.PermConfigRead)).Get("/{poolId}", h.handleGetPool)
		r.With(RequirePermission(rbac.PermConfigWrite)).Patch("/{poolId}", h.handleUpdatePool)
		r.With(RequirePermission(rbac.PermConfigWrite)).Delete("/{poolId}", h.handleDeletePool)
		r.With(RequirePermission(rbac.PermConfigWrite)).Post("/{poolId}/targets", h.handleAddTarget)
		r.With(RequirePermission(rbac.PermConfigWrite)).Delete("/{poolId}/targets/{targetId}", h.handleDeleteTarget)
	})
}

type poolWithTargets struct {
	store.UpstreamPool
	Targets []store.UpstreamTarget `json:"targets"`
}

func (h *UpstreamHandler) handleListPools(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	limit, offset := parsePagination(r)

	var total int
	if err := h.Store.Pool().QueryRow(r.Context(), `SELECT count(*) FROM upstream_pools WHERE org_id = $1`, orgCtx.OrgID).Scan(&total); err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not count upstream pools.")
		return
	}

	rows, err := h.Store.Pool().Query(r.Context(), `
		SELECT id, org_id, env_id, name, destination_class,
		       connect_timeout_ms, read_timeout_ms,
		       health_check_path, health_check_interval_ms, health_check_timeout_ms,
		       revision, created_at, updated_at
		FROM upstream_pools
		WHERE org_id = $1
		ORDER BY created_at DESC, id ASC
		LIMIT $2 OFFSET $3
	`, orgCtx.OrgID, limit, offset)
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not list upstream pools.")
		return
	}
	defer rows.Close()

	pools := make([]poolWithTargets, 0)
	for rows.Next() {
		var p poolWithTargets
		if err := rows.Scan(
			&p.ID, &p.OrgID, &p.EnvID, &p.Name, &p.DestinationClass,
			&p.ConnectTimeoutMS, &p.ReadTimeoutMS,
			&p.HealthCheckPath, &p.HealthCheckIntervalMS, &p.HealthCheckTimeoutMS,
			&p.Revision, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not scan upstream pool.")
			return
		}
		p.Targets = make([]store.UpstreamTarget, 0)
		pools = append(pools, p)
	}
	rows.Close()

	for i := range pools {
		targets, err := h.loadPoolTargets(r, orgCtx.OrgID, pools[i].ID)
		if err != nil {
			WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not load pool targets.")
			return
		}
		pools[i].Targets = targets
	}

	WriteJSON(w, http.StatusOK, map[string]any{
		"items":  pools,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

func (h *UpstreamHandler) loadPoolTargets(r *http.Request, orgID, poolID string) ([]store.UpstreamTarget, error) {
	rows, err := h.Store.Pool().Query(r.Context(), `
		SELECT id, org_id, pool_id, url, weight, enabled, created_at, updated_at
		FROM upstream_targets
		WHERE org_id = $1 AND pool_id = $2
		ORDER BY created_at ASC, id ASC
	`, orgID, poolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]store.UpstreamTarget, 0)
	for rows.Next() {
		var t store.UpstreamTarget
		if err := rows.Scan(&t.ID, &t.OrgID, &t.PoolID, &t.URL, &t.Weight, &t.Enabled, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

type targetInput struct {
	URL     string `json:"url"`
	Weight  int    `json:"weight"`
	Enabled *bool  `json:"enabled"`
}

type createPoolRequest struct {
	EnvID                 string        `json:"env_id"`
	Name                  string        `json:"name"`
	DestinationClass      string        `json:"destination_class"`
	ConnectTimeoutMS      int           `json:"connect_timeout_ms"`
	ReadTimeoutMS         int           `json:"read_timeout_ms"`
	HealthCheckPath       string        `json:"health_check_path"`
	HealthCheckIntervalMS int           `json:"health_check_interval_ms"`
	HealthCheckTimeoutMS  int           `json:"health_check_timeout_ms"`
	Targets               []targetInput `json:"targets"`
}

func (h *UpstreamHandler) handleCreatePool(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	principal, _ := PrincipalFromContext(r.Context())

	var in createPoolRequest
	if !DecodeStrictJSON(w, r, 128<<10, &in) {
		return
	}

	name := strings.TrimSpace(in.Name)
	var errs []FieldError
	if len(name) < 1 || len(name) > 120 {
		errs = append(errs, FieldError{Field: "name", Message: "must be 1..120 characters"})
	}
	destClass := strings.TrimSpace(in.DestinationClass)
	if destClass == "" {
		destClass = "internal_approved"
	}
	switch destClass {
	case "public", "internal_approved", "mock_local":
	default:
		errs = append(errs, FieldError{Field: "destination_class", Message: "must be public, internal_approved, or mock_local"})
	}
	if destClass == "mock_local" && !h.TargetPolicy.AllowMockUpstreams {
		errs = append(errs, FieldError{Field: "destination_class", Message: "mock_local destinations are disabled in this environment"})
	}

	if in.ConnectTimeoutMS != 0 && (in.ConnectTimeoutMS < 100 || in.ConnectTimeoutMS > 60000) {
		errs = append(errs, FieldError{Field: "connect_timeout_ms", Message: "must be between 100 and 60000"})
	}
	if in.ReadTimeoutMS != 0 && (in.ReadTimeoutMS < 100 || in.ReadTimeoutMS > 120000) {
		errs = append(errs, FieldError{Field: "read_timeout_ms", Message: "must be between 100 and 120000"})
	}

	for _, t := range in.Targets {
		if _, err := netsec.ValidateTargetURL(t.URL, h.TargetPolicy); err != nil {
			errs = append(errs, FieldError{Field: "targets.url", Message: err.Error()})
		}
		wt := t.Weight
		if wt == 0 {
			wt = 100
		}
		if wt < 1 || wt > 1000 {
			errs = append(errs, FieldError{Field: "targets.weight", Message: "weight must be between 1 and 1000"})
		}
	}

	envID := strings.TrimSpace(in.EnvID)
	if envID == "" {
		if err := h.Store.Pool().QueryRow(r.Context(), `
			SELECT id FROM environments WHERE org_id = $1 ORDER BY (slug = 'dev') DESC, created_at ASC LIMIT 1
		`, orgCtx.OrgID).Scan(&envID); err != nil {
			errs = append(errs, FieldError{Field: "env_id", Message: "valid env_id is required"})
		}
	} else if !uuidPattern.MatchString(envID) {
		errs = append(errs, FieldError{Field: "env_id", Message: "must be a valid UUID"})
	}
	if len(errs) > 0 {
		WriteProblem(w, r, http.StatusUnprocessableEntity, "validation_error", "Validation Failed", "Invalid upstream pool fields.", errs...)
		return
	}

	var result poolWithTargets
	err := h.Store.WithTx(r.Context(), func(tx *store.Store) error {
		created, txErr := tx.CreateUpstreamPool(r.Context(), store.UpstreamPool{
			OrgID:                 orgCtx.OrgID,
			EnvID:                 envID,
			Name:                  name,
			DestinationClass:      destClass,
			ConnectTimeoutMS:      in.ConnectTimeoutMS,
			ReadTimeoutMS:         in.ReadTimeoutMS,
			HealthCheckPath:       in.HealthCheckPath,
			HealthCheckIntervalMS: in.HealthCheckIntervalMS,
			HealthCheckTimeoutMS:  in.HealthCheckTimeoutMS,
		})
		if txErr != nil {
			return txErr
		}
		result.UpstreamPool = created
		result.Targets = make([]store.UpstreamTarget, 0, len(in.Targets))
		for _, t := range in.Targets {
			wt := t.Weight
			if wt == 0 {
				wt = 100
			}
			en := true
			if t.Enabled != nil {
				en = *t.Enabled
			}
			added, addErr := tx.AddUpstreamTarget(r.Context(), orgCtx.OrgID, created.ID, strings.TrimSpace(t.URL), wt, en)
			if addErr != nil {
				return addErr
			}
			result.Targets = append(result.Targets, added)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			WriteProblem(w, r, http.StatusConflict, "upstream_conflict", "Conflict", "An upstream pool with this name or duplicate target already exists.")
			return
		}
		WriteProblem(w, r, http.StatusUnprocessableEntity, "invalid_reference", "Invalid Upstream Configuration", err.Error())
		return
	}
	_ = bumpDraftRevision(r.Context(), h.Store, orgCtx.OrgID, principal.UserID)

	w.Header().Set("ETag", formatRevisionETag(result.Revision))
	WriteJSON(w, http.StatusCreated, result)
}

func (h *UpstreamHandler) handleGetPool(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	poolID := chi.URLParam(r, "poolId")
	if !uuidPattern.MatchString(poolID) {
		WriteProblem(w, r, http.StatusBadRequest, "invalid_pool_id", "Invalid Pool ID", "Pool ID must be a valid UUID.")
		return
	}
	var p poolWithTargets
	err := h.Store.Pool().QueryRow(r.Context(), `
		SELECT id, org_id, env_id, name, destination_class,
		       connect_timeout_ms, read_timeout_ms,
		       health_check_path, health_check_interval_ms, health_check_timeout_ms,
		       revision, created_at, updated_at
		FROM upstream_pools
		WHERE org_id = $1 AND id = $2
	`, orgCtx.OrgID, poolID).Scan(
		&p.ID, &p.OrgID, &p.EnvID, &p.Name, &p.DestinationClass,
		&p.ConnectTimeoutMS, &p.ReadTimeoutMS,
		&p.HealthCheckPath, &p.HealthCheckIntervalMS, &p.HealthCheckTimeoutMS,
		&p.Revision, &p.CreatedAt, &p.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		WriteProblem(w, r, http.StatusNotFound, "not_found", "Not Found", "Upstream pool not found.")
		return
	}
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not retrieve upstream pool.")
		return
	}
	targets, err := h.loadPoolTargets(r, orgCtx.OrgID, p.ID)
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not load targets.")
		return
	}
	p.Targets = targets
	w.Header().Set("ETag", formatRevisionETag(p.Revision))
	WriteJSON(w, http.StatusOK, p)
}

type updatePoolRequest struct {
	Name             *string `json:"name"`
	ConnectTimeoutMS *int    `json:"connect_timeout_ms"`
	ReadTimeoutMS    *int    `json:"read_timeout_ms"`
	HealthCheckPath  *string `json:"health_check_path"`
	Revision         int64   `json:"revision"`
}

func (h *UpstreamHandler) handleUpdatePool(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	principal, _ := PrincipalFromContext(r.Context())
	poolID := chi.URLParam(r, "poolId")
	if !uuidPattern.MatchString(poolID) {
		WriteProblem(w, r, http.StatusBadRequest, "invalid_pool_id", "Invalid Pool ID", "Pool ID must be a valid UUID.")
		return
	}
	var in updatePoolRequest
	if !DecodeStrictJSON(w, r, 64<<10, &in) {
		return
	}
	expectedRev := in.Revision
	if ifMatch := strings.TrimSpace(r.Header.Get("If-Match")); ifMatch != "" {
		if parsed, ok := parseRevisionETag(ifMatch); ok {
			expectedRev = parsed
		}
	}
	if expectedRev < 1 {
		WriteProblem(w, r, http.StatusUnprocessableEntity, "validation_error", "Validation Failed", "revision or If-Match is required.")
		return
	}

	var cur store.UpstreamPool
	err := h.Store.Pool().QueryRow(r.Context(), `
		SELECT id, org_id, env_id, name, destination_class, connect_timeout_ms, read_timeout_ms,
		       health_check_path, health_check_interval_ms, health_check_timeout_ms, revision, created_at, updated_at
		FROM upstream_pools WHERE org_id = $1 AND id = $2
	`, orgCtx.OrgID, poolID).Scan(
		&cur.ID, &cur.OrgID, &cur.EnvID, &cur.Name, &cur.DestinationClass, &cur.ConnectTimeoutMS, &cur.ReadTimeoutMS,
		&cur.HealthCheckPath, &cur.HealthCheckIntervalMS, &cur.HealthCheckTimeoutMS, &cur.Revision, &cur.CreatedAt, &cur.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		WriteProblem(w, r, http.StatusNotFound, "not_found", "Not Found", "Upstream pool not found.")
		return
	}
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not load pool.")
		return
	}
	if in.Name != nil {
		cur.Name = strings.TrimSpace(*in.Name)
	}
	if in.ConnectTimeoutMS != nil {
		cur.ConnectTimeoutMS = *in.ConnectTimeoutMS
	}
	if in.ReadTimeoutMS != nil {
		cur.ReadTimeoutMS = *in.ReadTimeoutMS
	}
	if in.HealthCheckPath != nil {
		cur.HealthCheckPath = strings.TrimSpace(*in.HealthCheckPath)
	}

	var updated store.UpstreamPool
	err = h.Store.Pool().QueryRow(r.Context(), `
		UPDATE upstream_pools
		SET name = $3, connect_timeout_ms = $4, read_timeout_ms = $5, health_check_path = $6,
		    revision = revision + 1, updated_at = NOW()
		WHERE org_id = $1 AND id = $2 AND revision = $7
		RETURNING id, org_id, env_id, name, destination_class, connect_timeout_ms, read_timeout_ms,
		          health_check_path, health_check_interval_ms, health_check_timeout_ms, revision, created_at, updated_at
	`, orgCtx.OrgID, poolID, cur.Name, cur.ConnectTimeoutMS, cur.ReadTimeoutMS, cur.HealthCheckPath, expectedRev).Scan(
		&updated.ID, &updated.OrgID, &updated.EnvID, &updated.Name, &updated.DestinationClass, &updated.ConnectTimeoutMS, &updated.ReadTimeoutMS,
		&updated.HealthCheckPath, &updated.HealthCheckIntervalMS, &updated.HealthCheckTimeoutMS, &updated.Revision, &updated.CreatedAt, &updated.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		WriteProblem(w, r, http.StatusConflict, "revision_conflict", "Conflict", "Upstream pool was modified concurrently.")
		return
	}
	if err != nil {
		WriteProblem(w, r, http.StatusUnprocessableEntity, "validation_error", "Validation Failed", "Invalid timeout or pool field.")
		return
	}
	_ = bumpDraftRevision(r.Context(), h.Store, orgCtx.OrgID, principal.UserID)
	w.Header().Set("ETag", formatRevisionETag(updated.Revision))
	WriteJSON(w, http.StatusOK, updated)
}

func (h *UpstreamHandler) handleDeletePool(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	principal, _ := PrincipalFromContext(r.Context())
	poolID := chi.URLParam(r, "poolId")
	if !uuidPattern.MatchString(poolID) {
		WriteProblem(w, r, http.StatusBadRequest, "invalid_pool_id", "Invalid Pool ID", "Pool ID must be a valid UUID.")
		return
	}
	tag, err := h.Store.Pool().Exec(r.Context(), `DELETE FROM upstream_pools WHERE org_id = $1 AND id = $2`, orgCtx.OrgID, poolID)
	if err != nil {
		WriteProblem(w, r, http.StatusConflict, "pool_in_use", "Conflict", "Cannot delete upstream pool while routes still reference it.")
		return
	}
	if tag.RowsAffected() == 0 {
		WriteProblem(w, r, http.StatusNotFound, "not_found", "Not Found", "Upstream pool not found.")
		return
	}
	_ = bumpDraftRevision(r.Context(), h.Store, orgCtx.OrgID, principal.UserID)
	w.WriteHeader(http.StatusNoContent)
}

func (h *UpstreamHandler) handleAddTarget(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	principal, _ := PrincipalFromContext(r.Context())
	poolID := chi.URLParam(r, "poolId")
	if !uuidPattern.MatchString(poolID) {
		WriteProblem(w, r, http.StatusBadRequest, "invalid_pool_id", "Invalid Pool ID", "Pool ID must be a valid UUID.")
		return
	}
	var in targetInput
	if !DecodeStrictJSON(w, r, 32<<10, &in) {
		return
	}
	if _, err := netsec.ValidateTargetURL(in.URL, h.TargetPolicy); err != nil {
		WriteProblem(w, r, http.StatusUnprocessableEntity, "disallowed_target", "Disallowed Upstream Target", err.Error())
		return
	}
	wt := in.Weight
	if wt == 0 {
		wt = 100
	}
	if wt < 1 || wt > 1000 {
		WriteProblem(w, r, http.StatusUnprocessableEntity, "validation_error", "Validation Failed", "weight must be between 1 and 1000.")
		return
	}
	en := true
	if in.Enabled != nil {
		en = *in.Enabled
	}

	added, err := h.Store.AddUpstreamTarget(r.Context(), orgCtx.OrgID, poolID, strings.TrimSpace(in.URL), wt, en)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			WriteProblem(w, r, http.StatusConflict, "target_exists", "Conflict", "Target URL already exists in this pool.")
			return
		}
		WriteProblem(w, r, http.StatusNotFound, "not_found", "Not Found", "Upstream pool not found in this organization.")
		return
	}
	_ = bumpDraftRevision(r.Context(), h.Store, orgCtx.OrgID, principal.UserID)
	WriteJSON(w, http.StatusCreated, added)
}

func (h *UpstreamHandler) handleDeleteTarget(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	principal, _ := PrincipalFromContext(r.Context())
	poolID := chi.URLParam(r, "poolId")
	targetID := chi.URLParam(r, "targetId")
	if !uuidPattern.MatchString(poolID) || !uuidPattern.MatchString(targetID) {
		WriteProblem(w, r, http.StatusBadRequest, "invalid_id", "Invalid ID", "poolId and targetId must be valid UUIDs.")
		return
	}
	tag, err := h.Store.Pool().Exec(r.Context(), `
		DELETE FROM upstream_targets WHERE org_id = $1 AND pool_id = $2 AND id = $3
	`, orgCtx.OrgID, poolID, targetID)
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not delete target.")
		return
	}
	if tag.RowsAffected() == 0 {
		WriteProblem(w, r, http.StatusNotFound, "not_found", "Not Found", "Target not found.")
		return
	}
	_ = bumpDraftRevision(r.Context(), h.Store, orgCtx.OrgID, principal.UserID)
	w.WriteHeader(http.StatusNoContent)
}

type safeTargetDiag struct {
	TargetID string `json:"target_id"`
	Enabled  bool   `json:"enabled"`
	Weight   int    `json:"weight"`
	Scheme   string `json:"scheme,omitempty"`
	Host     string `json:"host,omitempty"`
}

type safePoolHealthDiag struct {
	PoolID           string           `json:"pool_id"`
	PoolName         string           `json:"pool_name"`
	DestinationClass string           `json:"destination_class"`
	Status           string           `json:"status"`
	EnabledTargets   int              `json:"enabled_targets"`
	DisabledTargets  int              `json:"disabled_targets"`
	TotalTargets     int              `json:"total_targets"`
	Targets          []safeTargetDiag `json:"targets,omitempty"`
}

// handleUpstreamHealthDiagnostics returns safe aggregated pool/target health visibility.
// Viewers receive aggregated pool status & counts only (no internal addresses);
// operators/admins (holding PermConfigWrite) also receive credential-stripped target details.
func (h *UpstreamHandler) handleUpstreamHealthDiagnostics(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	includeTargets := rbac.Allows(orgCtx.Role, rbac.PermConfigWrite)

	rows, err := h.Store.Pool().Query(r.Context(), `
		SELECT id, name, destination_class
		FROM upstream_pools
		WHERE org_id = $1
		ORDER BY name ASC
	`, orgCtx.OrgID)
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not load upstream pools.")
		return
	}
	defer rows.Close()

	pools := make([]safePoolHealthDiag, 0)
	for rows.Next() {
		var p safePoolHealthDiag
		if err := rows.Scan(&p.PoolID, &p.PoolName, &p.DestinationClass); err != nil {
			WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not scan upstream pool.")
			return
		}
		pools = append(pools, p)
	}
	rows.Close()

	for i := range pools {
		targets, err := h.loadPoolTargets(r, orgCtx.OrgID, pools[i].PoolID)
		if err != nil {
			WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not load upstream targets.")
			return
		}
		for _, t := range targets {
			pools[i].TotalTargets++
			if t.Enabled {
				pools[i].EnabledTargets++
			} else {
				pools[i].DisabledTargets++
			}
			if includeTargets {
				td := safeTargetDiag{
					TargetID: t.ID,
					Enabled:  t.Enabled,
					Weight:   t.Weight,
				}
				if parsed, pErr := url.Parse(t.URL); pErr == nil {
					td.Scheme = parsed.Scheme
					td.Host = parsed.Host // never includes parsed.User credentials or query secrets
				}
				pools[i].Targets = append(pools[i].Targets, td)
			}
		}
		if pools[i].EnabledTargets == 0 {
			pools[i].Status = "unavailable"
		} else {
			pools[i].Status = "healthy"
		}
	}

	WriteJSON(w, http.StatusOK, map[string]any{
		"items":      pools,
		"total":      len(pools),
		"visibility": map[bool]string{true: "operator", false: "aggregated_viewer"}[includeTargets],
	})
}
