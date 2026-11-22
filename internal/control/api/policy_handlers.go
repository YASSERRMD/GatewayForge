package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/YASSERRMD/GatewayForge/internal/control/auth"
	"github.com/YASSERRMD/GatewayForge/internal/control/rbac"
	"github.com/YASSERRMD/GatewayForge/internal/control/store"
)

// PolicyView represents a persisted typed policy and its scope attachments.
type PolicyView struct {
	ID            string             `json:"id"`
	OrgID         string             `json:"org_id"`
	Name          string             `json:"name"`
	PolicyType    string             `json:"policy_type"`
	SchemaVersion int                `json:"schema_version"`
	Config        json.RawMessage    `json:"config"`
	Enabled       bool               `json:"enabled"`
	Revision      int64              `json:"revision"`
	Attachments   []PolicyAttachView `json:"attachments,omitempty"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
}

// PolicyAttachView represents one policy scope binding.
type PolicyAttachView struct {
	ID        string    `json:"id"`
	PolicyID  string    `json:"policy_id"`
	ScopeType string    `json:"scope_type"`
	ServiceID *string   `json:"service_id,omitempty"`
	RouteID   *string   `json:"route_id,omitempty"`
	Priority  int       `json:"priority"`
	CreatedAt time.Time `json:"created_at"`
}

// PolicyHandler serves CRUD and attachment management for typed policies.
type PolicyHandler struct {
	Store    *store.Store
	Sessions *auth.SessionManager
	Audit    *AuditRecorder
}

// MountPolicyRoutes registers /orgs/{orgId}/policies routes on v1.
func (h *PolicyHandler) MountPolicyRoutes(v1 chi.Router) {
	orgAuth := v1.With(RequireAuth(h.Sessions), RequireOrgContext(h.Sessions), AuditMutationsMiddleware(h.Audit))
	orgAuth.Route("/orgs/{orgId}/policies", func(r chi.Router) {
		r.With(RequirePermission(rbac.PermConfigRead)).Get("/", h.handleListPolicies)
		r.With(RequirePermission(rbac.PermConfigWrite)).Post("/", h.handleCreatePolicy)
		r.With(RequirePermission(rbac.PermConfigRead)).Get("/{policyId}", h.handleGetPolicy)
		r.With(RequirePermission(rbac.PermConfigWrite)).Patch("/{policyId}", h.handleUpdatePolicy)
		r.With(RequirePermission(rbac.PermConfigWrite)).Delete("/{policyId}", h.handleDeletePolicy)
		r.With(RequirePermission(rbac.PermConfigWrite)).Post("/{policyId}/attachments", h.handleAttachPolicy)
		r.With(RequirePermission(rbac.PermConfigWrite)).Delete("/{policyId}/attachments/{attachmentId}", h.handleDetachPolicy)
	})
}

func (h *PolicyHandler) handleListPolicies(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	limit, offset := parsePagination(r)

	var total int
	if err := h.Store.Pool().QueryRow(r.Context(), `SELECT count(*) FROM policies WHERE org_id = $1`, orgCtx.OrgID).Scan(&total); err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not count policies.")
		return
	}

	rows, err := h.Store.Pool().Query(r.Context(), `
		SELECT id, org_id, name, policy_type, schema_version, config, enabled, revision, created_at, updated_at
		FROM policies
		WHERE org_id = $1
		ORDER BY created_at DESC, id ASC
		LIMIT $2 OFFSET $3
	`, orgCtx.OrgID, limit, offset)
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not list policies.")
		return
	}
	defer rows.Close()

	items := make([]PolicyView, 0)
	for rows.Next() {
		var pv PolicyView
		if err := rows.Scan(&pv.ID, &pv.OrgID, &pv.Name, &pv.PolicyType, &pv.SchemaVersion, &pv.Config, &pv.Enabled, &pv.Revision, &pv.CreatedAt, &pv.UpdatedAt); err != nil {
			WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not scan policy.")
			return
		}
		items = append(items, pv)
	}
	rows.Close()

	for i := range items {
		atts, _ := h.loadAttachments(r, orgCtx.OrgID, items[i].ID)
		items[i].Attachments = atts
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}

func (h *PolicyHandler) loadAttachments(r *http.Request, orgID, policyID string) ([]PolicyAttachView, error) {
	rows, err := h.Store.Pool().Query(r.Context(), `
		SELECT id, policy_id, scope_type, service_id::TEXT, route_id::TEXT, priority, created_at
		FROM policy_attachments
		WHERE org_id = $1 AND policy_id = $2
		ORDER BY priority DESC, created_at ASC
	`, orgID, policyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]PolicyAttachView, 0)
	for rows.Next() {
		var a PolicyAttachView
		if err := rows.Scan(&a.ID, &a.PolicyID, &a.ScopeType, &a.ServiceID, &a.RouteID, &a.Priority, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

type createPolicyRequest struct {
	Name       string          `json:"name"`
	PolicyType string          `json:"policy_type"`
	Config     json.RawMessage `json:"config"`
	Enabled    *bool           `json:"enabled"`
}

func (h *PolicyHandler) handleCreatePolicy(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	principal, _ := PrincipalFromContext(r.Context())

	var in createPolicyRequest
	if !DecodeStrictJSON(w, r, 128<<10, &in) {
		return
	}
	name := strings.TrimSpace(in.Name)
	pType := strings.TrimSpace(in.PolicyType)
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	var obj map[string]any
	if err := json.Unmarshal(in.Config, &obj); err != nil || obj == nil {
		WriteProblem(w, r, http.StatusUnprocessableEntity, "validation_error", "Validation Failed", "config must be a JSON object.")
		return
	}

	var pv PolicyView
	err := h.Store.Pool().QueryRow(r.Context(), `
		INSERT INTO policies (org_id, name, policy_type, schema_version, config, enabled)
		VALUES ($1, $2, $3, 1, $4, $5)
		RETURNING id, org_id, name, policy_type, schema_version, config, enabled, revision, created_at, updated_at
	`, orgCtx.OrgID, name, pType, in.Config, enabled).Scan(
		&pv.ID, &pv.OrgID, &pv.Name, &pv.PolicyType, &pv.SchemaVersion, &pv.Config, &pv.Enabled, &pv.Revision, &pv.CreatedAt, &pv.UpdatedAt,
	)
	if err != nil {
		if strings.Contains(err.Error(), "23505") {
			WriteProblem(w, r, http.StatusConflict, "policy_conflict", "Conflict", "A policy with this name already exists.")
			return
		}
		WriteProblem(w, r, http.StatusUnprocessableEntity, "validation_error", "Validation Failed", "Invalid policy name or type.")
		return
	}
	_ = bumpDraftRevision(r.Context(), h.Store, orgCtx.OrgID, principal.UserID)
	w.Header().Set("ETag", formatRevisionETag(pv.Revision))
	WriteJSON(w, http.StatusCreated, pv)
}

func (h *PolicyHandler) handleGetPolicy(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	policyID := chi.URLParam(r, "policyId")
	if !uuidPattern.MatchString(policyID) {
		WriteProblem(w, r, http.StatusBadRequest, "invalid_policy_id", "Invalid Policy ID", "Policy ID must be a valid UUID.")
		return
	}
	var pv PolicyView
	err := h.Store.Pool().QueryRow(r.Context(), `
		SELECT id, org_id, name, policy_type, schema_version, config, enabled, revision, created_at, updated_at
		FROM policies WHERE org_id = $1 AND id = $2
	`, orgCtx.OrgID, policyID).Scan(
		&pv.ID, &pv.OrgID, &pv.Name, &pv.PolicyType, &pv.SchemaVersion, &pv.Config, &pv.Enabled, &pv.Revision, &pv.CreatedAt, &pv.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		WriteProblem(w, r, http.StatusNotFound, "not_found", "Not Found", "Policy not found.")
		return
	}
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not retrieve policy.")
		return
	}
	pv.Attachments, _ = h.loadAttachments(r, orgCtx.OrgID, pv.ID)
	w.Header().Set("ETag", formatRevisionETag(pv.Revision))
	WriteJSON(w, http.StatusOK, pv)
}

type updatePolicyRequest struct {
	Name     *string         `json:"name"`
	Config   json.RawMessage `json:"config"`
	Enabled  *bool           `json:"enabled"`
	Revision int64           `json:"revision"`
}

func (h *PolicyHandler) handleUpdatePolicy(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	principal, _ := PrincipalFromContext(r.Context())
	policyID := chi.URLParam(r, "policyId")
	if !uuidPattern.MatchString(policyID) {
		WriteProblem(w, r, http.StatusBadRequest, "invalid_policy_id", "Invalid Policy ID", "Policy ID must be a valid UUID.")
		return
	}
	var in updatePolicyRequest
	if !DecodeStrictJSON(w, r, 128<<10, &in) {
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

	var cur PolicyView
	err := h.Store.Pool().QueryRow(r.Context(), `
		SELECT id, org_id, name, policy_type, schema_version, config, enabled, revision, created_at, updated_at
		FROM policies WHERE org_id = $1 AND id = $2
	`, orgCtx.OrgID, policyID).Scan(
		&cur.ID, &cur.OrgID, &cur.Name, &cur.PolicyType, &cur.SchemaVersion, &cur.Config, &cur.Enabled, &cur.Revision, &cur.CreatedAt, &cur.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		WriteProblem(w, r, http.StatusNotFound, "not_found", "Not Found", "Policy not found.")
		return
	}
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not load policy.")
		return
	}
	if in.Name != nil {
		cur.Name = strings.TrimSpace(*in.Name)
	}
	if len(in.Config) > 0 {
		cur.Config = in.Config
	}
	if in.Enabled != nil {
		cur.Enabled = *in.Enabled
	}

	var updated PolicyView
	err = h.Store.Pool().QueryRow(r.Context(), `
		UPDATE policies
		SET name = $3, config = $4, enabled = $5, revision = revision + 1, updated_at = NOW()
		WHERE org_id = $1 AND id = $2 AND revision = $6
		RETURNING id, org_id, name, policy_type, schema_version, config, enabled, revision, created_at, updated_at
	`, orgCtx.OrgID, policyID, cur.Name, cur.Config, cur.Enabled, expectedRev).Scan(
		&updated.ID, &updated.OrgID, &updated.Name, &updated.PolicyType, &updated.SchemaVersion, &updated.Config, &updated.Enabled, &updated.Revision, &updated.CreatedAt, &updated.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		WriteProblem(w, r, http.StatusConflict, "revision_conflict", "Conflict", "Policy was modified concurrently.")
		return
	}
	if err != nil {
		WriteProblem(w, r, http.StatusUnprocessableEntity, "validation_error", "Validation Failed", "Invalid policy update.")
		return
	}
	_ = bumpDraftRevision(r.Context(), h.Store, orgCtx.OrgID, principal.UserID)
	w.Header().Set("ETag", formatRevisionETag(updated.Revision))
	WriteJSON(w, http.StatusOK, updated)
}

func (h *PolicyHandler) handleDeletePolicy(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	principal, _ := PrincipalFromContext(r.Context())
	policyID := chi.URLParam(r, "policyId")
	if !uuidPattern.MatchString(policyID) {
		WriteProblem(w, r, http.StatusBadRequest, "invalid_policy_id", "Invalid Policy ID", "Policy ID must be a valid UUID.")
		return
	}
	tag, err := h.Store.Pool().Exec(r.Context(), `DELETE FROM policies WHERE org_id = $1 AND id = $2`, orgCtx.OrgID, policyID)
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not delete policy.")
		return
	}
	if tag.RowsAffected() == 0 {
		WriteProblem(w, r, http.StatusNotFound, "not_found", "Not Found", "Policy not found.")
		return
	}
	_ = bumpDraftRevision(r.Context(), h.Store, orgCtx.OrgID, principal.UserID)
	w.WriteHeader(http.StatusNoContent)
}

type attachPolicyRequest struct {
	ScopeType string  `json:"scope_type"`
	ServiceID *string `json:"service_id,omitempty"`
	RouteID   *string `json:"route_id,omitempty"`
	Priority  *int    `json:"priority"`
}

func (h *PolicyHandler) handleAttachPolicy(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	principal, _ := PrincipalFromContext(r.Context())
	policyID := chi.URLParam(r, "policyId")
	if !uuidPattern.MatchString(policyID) {
		WriteProblem(w, r, http.StatusBadRequest, "invalid_policy_id", "Invalid Policy ID", "Policy ID must be a valid UUID.")
		return
	}
	var in attachPolicyRequest
	if !DecodeStrictJSON(w, r, 32<<10, &in) {
		return
	}
	prio := 100
	if in.Priority != nil {
		prio = *in.Priority
	}

	var av PolicyAttachView
	err := h.Store.Pool().QueryRow(r.Context(), `
		INSERT INTO policy_attachments (org_id, policy_id, scope_type, service_id, route_id, priority)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, policy_id, scope_type, service_id::TEXT, route_id::TEXT, priority, created_at
	`, orgCtx.OrgID, policyID, strings.TrimSpace(in.ScopeType), in.ServiceID, in.RouteID, prio).Scan(
		&av.ID, &av.PolicyID, &av.ScopeType, &av.ServiceID, &av.RouteID, &av.Priority, &av.CreatedAt,
	)
	if err != nil {
		if strings.Contains(err.Error(), "23505") {
			WriteProblem(w, r, http.StatusConflict, "attachment_exists", "Conflict", "Policy is already attached to this scope.")
			return
		}
		WriteProblem(w, r, http.StatusUnprocessableEntity, "invalid_attachment", "Invalid Attachment", "Scope type or target reference is invalid.")
		return
	}
	_ = bumpDraftRevision(r.Context(), h.Store, orgCtx.OrgID, principal.UserID)
	WriteJSON(w, http.StatusCreated, av)
}

func (h *PolicyHandler) handleDetachPolicy(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	principal, _ := PrincipalFromContext(r.Context())
	policyID := chi.URLParam(r, "policyId")
	attID := chi.URLParam(r, "attachmentId")
	if !uuidPattern.MatchString(policyID) || !uuidPattern.MatchString(attID) {
		WriteProblem(w, r, http.StatusBadRequest, "invalid_id", "Invalid ID", "policyId and attachmentId must be valid UUIDs.")
		return
	}
	tag, err := h.Store.Pool().Exec(r.Context(), `
		DELETE FROM policy_attachments WHERE org_id = $1 AND policy_id = $2 AND id = $3
	`, orgCtx.OrgID, policyID, attID)
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not detach policy.")
		return
	}
	if tag.RowsAffected() == 0 {
		WriteProblem(w, r, http.StatusNotFound, "not_found", "Not Found", "Attachment not found.")
		return
	}
	_ = bumpDraftRevision(r.Context(), h.Store, orgCtx.OrgID, principal.UserID)
	w.WriteHeader(http.StatusNoContent)
}
