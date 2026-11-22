package api

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/YASSERRMD/GatewayForge/internal/control/auth"
	"github.com/YASSERRMD/GatewayForge/internal/control/rbac"
	"github.com/YASSERRMD/GatewayForge/internal/control/store"
)

var orgSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}[a-z0-9]$`)

// OrgHandler implements organization CRUD endpoints.
type OrgHandler struct {
	Store    *store.Store
	Sessions *auth.SessionManager
}

// MountOrgRoutes registers /orgs and /orgs/{orgId} routes under v1.
func (h *OrgHandler) MountOrgRoutes(v1 chi.Router) {
	v1.With(RequireAuth(h.Sessions)).Route("/orgs", func(r chi.Router) {
		r.Get("/", h.handleListOrgs)
		r.Post("/", h.handleCreateOrg)

		r.With(RequireOrgContext(h.Sessions)).Route("/{orgId}", func(orgR chi.Router) {
			orgR.With(RequirePermission(rbac.PermOrgRead)).Get("/", h.handleGetOrg)
			orgR.With(RequirePermission(rbac.PermOrgUpdate)).Patch("/", h.handleUpdateOrg)
		})
	})
}

func parsePagination(r *http.Request) (limit, offset int) {
	limit = 25
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 1 && parsed <= 100 {
			limit = parsed
		}
	}
	offset = 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
			offset = parsed
		}
	}
	return limit, offset
}

func (h *OrgHandler) handleListOrgs(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		WriteProblem(w, r, http.StatusUnauthorized, "unauthenticated", "Unauthorized", "Authentication required.")
		return
	}
	limit, offset := parsePagination(r)

	var total int
	if err := h.Store.Pool().QueryRow(r.Context(), `
		SELECT count(*)
		FROM organizations o
		JOIN memberships m ON m.org_id = o.id
		WHERE m.user_id = $1
	`, principal.UserID).Scan(&total); err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not count organizations.")
		return
	}

	rows, err := h.Store.Pool().Query(r.Context(), `
		SELECT o.id, o.slug, o.display_name, o.enabled, o.revision, o.created_at, o.updated_at, m.role
		FROM organizations o
		JOIN memberships m ON m.org_id = o.id
		WHERE m.user_id = $1
		ORDER BY o.slug ASC, o.id ASC
		LIMIT $2 OFFSET $3
	`, principal.UserID, limit, offset)
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not list organizations.")
		return
	}
	defer rows.Close()

	type orgWithRole struct {
		store.Organization
		Role string `json:"role"`
	}
	items := make([]orgWithRole, 0)
	for rows.Next() {
		var item orgWithRole
		if err := rows.Scan(
			&item.ID, &item.Slug, &item.DisplayName, &item.Enabled, &item.Revision,
			&item.CreatedAt, &item.UpdatedAt, &item.Role,
		); err != nil {
			WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not read organization.")
			return
		}
		items = append(items, item)
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"items":  items,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

type createOrgRequest struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
}

func (h *OrgHandler) handleCreateOrg(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		WriteProblem(w, r, http.StatusUnauthorized, "unauthenticated", "Unauthorized", "Authentication required.")
		return
	}
	var in createOrgRequest
	if !DecodeStrictJSON(w, r, 64<<10, &in) {
		return
	}
	slug := strings.ToLower(strings.TrimSpace(in.Slug))
	displayName := strings.TrimSpace(in.DisplayName)
	var fieldErrs []FieldError
	if !orgSlugPattern.MatchString(slug) {
		fieldErrs = append(fieldErrs, FieldError{Field: "slug", Message: "must be 3..64 lowercase alphanumeric or hyphen characters"})
	}
	if len(displayName) < 1 || len(displayName) > 120 {
		fieldErrs = append(fieldErrs, FieldError{Field: "display_name", Message: "must be 1..120 characters"})
	}
	if len(fieldErrs) > 0 {
		WriteProblem(w, r, http.StatusUnprocessableEntity, "validation_error", "Validation Failed", "Invalid organization fields.", fieldErrs...)
		return
	}

	var created store.Organization
	tx, beginErr := h.Store.Pool().Begin(r.Context())
	if beginErr != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not start transaction.")
		return
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(r.Context())
		}
	}()

	err := tx.QueryRow(r.Context(), `
		INSERT INTO organizations (slug, display_name)
		VALUES ($1, $2)
		RETURNING id, slug, display_name, enabled, revision, created_at, updated_at
	`, slug, displayName).Scan(
		&created.ID, &created.Slug, &created.DisplayName, &created.Enabled, &created.Revision, &created.CreatedAt, &created.UpdatedAt,
	)
	if err != nil {
		if strings.Contains(err.Error(), "23505") || strings.Contains(err.Error(), "unique") {
			WriteProblem(w, r, http.StatusConflict, "org_slug_conflict", "Conflict", "An organization with this slug already exists.")
			return
		}
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not create organization.")
		return
	}
	if _, err := tx.Exec(r.Context(), `INSERT INTO draft_revisions (org_id, revision) VALUES ($1, 1)`, created.ID); err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not initialize draft revision.")
		return
	}
	if _, err := tx.Exec(r.Context(), `
		INSERT INTO environments (org_id, slug, display_name, is_production) VALUES ($1, 'dev', 'Development', FALSE)
	`, created.ID); err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not create default environment.")
		return
	}
	if _, err := tx.Exec(r.Context(), `
		INSERT INTO memberships (org_id, user_id, role) VALUES ($1, $2, $3)
	`, created.ID, principal.UserID, string(rbac.RoleOwner)); err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not assign owner membership.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not commit organization.")
		return
	}
	committed = true

	w.Header().Set("ETag", formatRevisionETag(created.Revision))
	WriteJSON(w, http.StatusCreated, created)
}

func (h *OrgHandler) handleGetOrg(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	org, err := h.Store.GetOrganization(r.Context(), orgCtx.OrgID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			WriteProblem(w, r, http.StatusNotFound, "not_found", "Not Found", "Organization not found.")
			return
		}
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not retrieve organization.")
		return
	}
	w.Header().Set("ETag", formatRevisionETag(org.Revision))
	WriteJSON(w, http.StatusOK, org)
}

type updateOrgRequest struct {
	DisplayName *string `json:"display_name"`
	Enabled     *bool   `json:"enabled"`
	Revision    int64   `json:"revision"`
}

func (h *OrgHandler) handleUpdateOrg(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	var in updateOrgRequest
	if !DecodeStrictJSON(w, r, 64<<10, &in) {
		return
	}

	expectedRev := in.Revision
	if ifMatch := strings.TrimSpace(r.Header.Get("If-Match")); ifMatch != "" {
		parsed, ok := parseRevisionETag(ifMatch)
		if !ok {
			WriteProblem(w, r, http.StatusBadRequest, "invalid_etag", "Invalid If-Match Header", "If-Match must use format W/\"rev-<n>\" or \"rev-<n>\".")
			return
		}
		expectedRev = parsed
	}
	if expectedRev < 1 {
		WriteProblem(w, r, http.StatusUnprocessableEntity, "validation_error", "Validation Failed", "Either revision (>=1) or If-Match header is required.")
		return
	}

	current, err := h.Store.GetOrganization(r.Context(), orgCtx.OrgID)
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not load organization.")
		return
	}
	nextName := current.DisplayName
	if in.DisplayName != nil {
		nextName = strings.TrimSpace(*in.DisplayName)
		if len(nextName) < 1 || len(nextName) > 120 {
			WriteProblem(w, r, http.StatusUnprocessableEntity, "validation_error", "Validation Failed", "display_name must be 1..120 characters.")
			return
		}
	}
	nextEnabled := current.Enabled
	if in.Enabled != nil {
		nextEnabled = *in.Enabled
	}

	updated, err := h.Store.UpdateOrganization(r.Context(), orgCtx.OrgID, nextName, nextEnabled, expectedRev)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			WriteProblem(w, r, http.StatusConflict, "revision_conflict", "Conflict", "The organization was modified by another request. Refresh and retry.")
			return
		}
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not update organization.")
		return
	}
	w.Header().Set("ETag", formatRevisionETag(updated.Revision))
	WriteJSON(w, http.StatusOK, updated)
}

func formatRevisionETag(rev int64) string {
	return fmt.Sprintf(`W/"rev-%d"`, rev)
}

func parseRevisionETag(raw string) (int64, bool) {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "W/")
	s = strings.Trim(s, `"`)
	if !strings.HasPrefix(s, "rev-") {
		return 0, false
	}
	v, err := strconv.ParseInt(strings.TrimPrefix(s, "rev-"), 10, 64)
	if err != nil || v < 1 {
		return 0, false
	}
	return v, true
}
