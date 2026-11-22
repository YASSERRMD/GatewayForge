package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/YASSERRMD/GatewayForge/internal/control/auth"
	"github.com/YASSERRMD/GatewayForge/internal/control/rbac"
	"github.com/YASSERRMD/GatewayForge/internal/control/store"
)

var serviceSlugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// ServiceHandler serves tenant-scoped environment listing and service CRUD endpoints.
type ServiceHandler struct {
	Store    *store.Store
	Sessions *auth.SessionManager
	Audit    *AuditRecorder
}

// MountServiceRoutes mounts /orgs/{orgId}/environments and /orgs/{orgId}/services routes on v1.
func (h *ServiceHandler) MountServiceRoutes(v1 chi.Router) {
	v1.With(RequireAuth(h.Sessions), RequireOrgContext(h.Sessions), AuditMutationsMiddleware(h.Audit)).Route("/orgs/{orgId}", func(r chi.Router) {
		r.With(RequirePermission(rbac.PermConfigRead)).Get("/environments", h.handleListEnvironments)

		r.Route("/services", func(sr chi.Router) {
			sr.With(RequirePermission(rbac.PermConfigRead)).Get("/", h.handleListServices)
			sr.With(RequirePermission(rbac.PermConfigWrite)).Post("/", h.handleCreateService)
			sr.With(RequirePermission(rbac.PermConfigRead)).Get("/{serviceId}", h.handleGetService)
			sr.With(RequirePermission(rbac.PermConfigWrite)).Patch("/{serviceId}", h.handleUpdateService)
			sr.With(RequirePermission(rbac.PermConfigWrite)).Delete("/{serviceId}", h.handleDeleteService)
		})
	})
}

func (h *ServiceHandler) handleListEnvironments(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	rows, err := h.Store.Pool().Query(r.Context(), `
		SELECT id, org_id, slug, display_name, is_production, created_at, updated_at
		FROM environments
		WHERE org_id = $1
		ORDER BY slug ASC
	`, orgCtx.OrgID)
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not list environments.")
		return
	}
	defer rows.Close()

	items := make([]store.Environment, 0)
	for rows.Next() {
		var env store.Environment
		if err := rows.Scan(&env.ID, &env.OrgID, &env.Slug, &env.DisplayName, &env.IsProduction, &env.CreatedAt, &env.UpdatedAt); err != nil {
			WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not read environment.")
			return
		}
		items = append(items, env)
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (h *ServiceHandler) handleListServices(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	limit, offset := parsePagination(r)
	search := strings.TrimSpace(r.URL.Query().Get("search"))

	var total int
	var err error
	if search != "" {
		like := "%" + search + "%"
		err = h.Store.Pool().QueryRow(r.Context(), `
			SELECT count(*) FROM services
			WHERE org_id = $1 AND deleted_at IS NULL AND (name ILIKE $2 OR slug ILIKE $2)
		`, orgCtx.OrgID, like).Scan(&total)
	} else {
		err = h.Store.Pool().QueryRow(r.Context(), `
			SELECT count(*) FROM services WHERE org_id = $1 AND deleted_at IS NULL
		`, orgCtx.OrgID).Scan(&total)
	}
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not count services.")
		return
	}

	var rows pgx.Rows
	if search != "" {
		like := "%" + search + "%"
		rows, err = h.Store.Pool().Query(r.Context(), `
			SELECT id, org_id, env_id, slug, name, description, enabled, revision, deleted_at, created_at, updated_at
			FROM services
			WHERE org_id = $1 AND deleted_at IS NULL AND (name ILIKE $2 OR slug ILIKE $2)
			ORDER BY created_at DESC, id ASC
			LIMIT $3 OFFSET $4
		`, orgCtx.OrgID, like, limit, offset)
	} else {
		rows, err = h.Store.Pool().Query(r.Context(), `
			SELECT id, org_id, env_id, slug, name, description, enabled, revision, deleted_at, created_at, updated_at
			FROM services
			WHERE org_id = $1 AND deleted_at IS NULL
			ORDER BY created_at DESC, id ASC
			LIMIT $2 OFFSET $3
		`, orgCtx.OrgID, limit, offset)
	}
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not list services.")
		return
	}
	defer rows.Close()

	items := make([]store.Service, 0)
	for rows.Next() {
		var svc store.Service
		if err := rows.Scan(
			&svc.ID, &svc.OrgID, &svc.EnvID, &svc.Slug, &svc.Name, &svc.Description,
			&svc.Enabled, &svc.Revision, &svc.DeletedAt, &svc.CreatedAt, &svc.UpdatedAt,
		); err != nil {
			WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not read service.")
			return
		}
		items = append(items, svc)
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"items":  items,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

type createServiceRequest struct {
	EnvID       string `json:"env_id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (h *ServiceHandler) handleCreateService(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	principal, _ := PrincipalFromContext(r.Context())

	var in createServiceRequest
	if !DecodeStrictJSON(w, r, 64<<10, &in) {
		return
	}
	slug := strings.ToLower(strings.TrimSpace(in.Slug))
	name := strings.TrimSpace(in.Name)
	desc := strings.TrimSpace(in.Description)

	var errs []FieldError
	if !serviceSlugRe.MatchString(slug) {
		errs = append(errs, FieldError{Field: "slug", Message: "must be 1..63 lowercase alphanumeric or hyphen characters"})
	}
	if len(name) < 1 || len(name) > 120 {
		errs = append(errs, FieldError{Field: "name", Message: "must be 1..120 characters"})
	}
	if len(desc) > 1000 {
		errs = append(errs, FieldError{Field: "description", Message: "must be at most 1000 characters"})
	}
	envID := strings.TrimSpace(in.EnvID)
	if envID == "" {
		// Resolve org's default 'dev' environment when env_id is omitted.
		if err := h.Store.Pool().QueryRow(r.Context(), `
			SELECT id FROM environments WHERE org_id = $1 ORDER BY (slug = 'dev') DESC, created_at ASC LIMIT 1
		`, orgCtx.OrgID).Scan(&envID); err != nil {
			errs = append(errs, FieldError{Field: "env_id", Message: "valid env_id is required"})
		}
	} else if !uuidPattern.MatchString(envID) {
		errs = append(errs, FieldError{Field: "env_id", Message: "must be a valid UUID"})
	}
	if len(errs) > 0 {
		WriteProblem(w, r, http.StatusUnprocessableEntity, "validation_error", "Validation Failed", "Invalid service fields.", errs...)
		return
	}

	var svc store.Service
	err := h.Store.WithTx(r.Context(), func(tx *store.Store) error {
		var txErr error
		svc, txErr = tx.CreateService(r.Context(), orgCtx.OrgID, envID, slug, name, desc)
		if txErr != nil {
			return txErr
		}
		return bumpDraftRevision(r.Context(), h.Store, orgCtx.OrgID, principal.UserID)
	})
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			WriteProblem(w, r, http.StatusConflict, "service_slug_conflict", "Conflict", "An active service with this slug already exists in the environment.")
			return
		}
		if strings.Contains(err.Error(), "constraint violation") {
			WriteProblem(w, r, http.StatusUnprocessableEntity, "invalid_reference", "Invalid Reference", "Environment does not belong to this organization.")
			return
		}
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not create service.")
		return
	}

	w.Header().Set("ETag", formatRevisionETag(svc.Revision))
	WriteJSON(w, http.StatusCreated, svc)
}

func (h *ServiceHandler) handleGetService(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	serviceID := chi.URLParam(r, "serviceId")
	if !uuidPattern.MatchString(serviceID) {
		WriteProblem(w, r, http.StatusBadRequest, "invalid_service_id", "Invalid Service ID", "Service ID must be a valid UUID.")
		return
	}
	svc, err := h.Store.GetService(r.Context(), orgCtx.OrgID, serviceID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			WriteProblem(w, r, http.StatusNotFound, "not_found", "Not Found", "Service not found.")
			return
		}
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not retrieve service.")
		return
	}
	w.Header().Set("ETag", formatRevisionETag(svc.Revision))
	WriteJSON(w, http.StatusOK, svc)
}

type updateServiceRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Enabled     *bool   `json:"enabled"`
	Revision    int64   `json:"revision"`
}

func (h *ServiceHandler) handleUpdateService(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	principal, _ := PrincipalFromContext(r.Context())
	serviceID := chi.URLParam(r, "serviceId")
	if !uuidPattern.MatchString(serviceID) {
		WriteProblem(w, r, http.StatusBadRequest, "invalid_service_id", "Invalid Service ID", "Service ID must be a valid UUID.")
		return
	}

	var in updateServiceRequest
	if !DecodeStrictJSON(w, r, 64<<10, &in) {
		return
	}
	expectedRev := in.Revision
	if ifMatch := strings.TrimSpace(r.Header.Get("If-Match")); ifMatch != "" {
		parsed, ok := parseRevisionETag(ifMatch)
		if !ok {
			WriteProblem(w, r, http.StatusBadRequest, "invalid_etag", "Invalid If-Match Header", "If-Match must use format W/\"rev-<n>\".")
			return
		}
		expectedRev = parsed
	}
	if expectedRev < 1 {
		WriteProblem(w, r, http.StatusUnprocessableEntity, "validation_error", "Validation Failed", "Either revision (>=1) or If-Match header is required.")
		return
	}

	cur, err := h.Store.GetService(r.Context(), orgCtx.OrgID, serviceID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			WriteProblem(w, r, http.StatusNotFound, "not_found", "Not Found", "Service not found.")
			return
		}
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not load service.")
		return
	}

	nextName := cur.Name
	if in.Name != nil {
		nextName = strings.TrimSpace(*in.Name)
		if len(nextName) < 1 || len(nextName) > 120 {
			WriteProblem(w, r, http.StatusUnprocessableEntity, "validation_error", "Validation Failed", "name must be 1..120 characters.")
			return
		}
	}
	nextDesc := cur.Description
	if in.Description != nil {
		nextDesc = strings.TrimSpace(*in.Description)
	}
	nextEnabled := cur.Enabled
	if in.Enabled != nil {
		nextEnabled = *in.Enabled
	}

	var updated store.Service
	err = h.Store.Pool().QueryRow(r.Context(), `
		UPDATE services
		SET name = $3, description = $4, enabled = $5, revision = revision + 1, updated_at = NOW()
		WHERE org_id = $1 AND id = $2 AND deleted_at IS NULL AND revision = $6
		RETURNING id, org_id, env_id, slug, name, description, enabled, revision, deleted_at, created_at, updated_at
	`, orgCtx.OrgID, serviceID, nextName, nextDesc, nextEnabled, expectedRev).Scan(
		&updated.ID, &updated.OrgID, &updated.EnvID, &updated.Slug, &updated.Name, &updated.Description,
		&updated.Enabled, &updated.Revision, &updated.DeletedAt, &updated.CreatedAt, &updated.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		WriteProblem(w, r, http.StatusConflict, "revision_conflict", "Conflict", "Service was modified concurrently. Refresh and retry.")
		return
	}
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not update service.")
		return
	}
	_ = bumpDraftRevision(r.Context(), h.Store, orgCtx.OrgID, principal.UserID)

	w.Header().Set("ETag", formatRevisionETag(updated.Revision))
	WriteJSON(w, http.StatusOK, updated)
}

func (h *ServiceHandler) handleDeleteService(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	principal, _ := PrincipalFromContext(r.Context())
	serviceID := chi.URLParam(r, "serviceId")
	if !uuidPattern.MatchString(serviceID) {
		WriteProblem(w, r, http.StatusBadRequest, "invalid_service_id", "Invalid Service ID", "Service ID must be a valid UUID.")
		return
	}

	tag, err := h.Store.Pool().Exec(r.Context(), `
		UPDATE services
		SET deleted_at = NOW(), enabled = FALSE, revision = revision + 1, updated_at = NOW()
		WHERE org_id = $1 AND id = $2 AND deleted_at IS NULL
	`, orgCtx.OrgID, serviceID)
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not tombstone service.")
		return
	}
	if tag.RowsAffected() == 0 {
		WriteProblem(w, r, http.StatusNotFound, "not_found", "Not Found", "Service not found.")
		return
	}
	_ = bumpDraftRevision(r.Context(), h.Store, orgCtx.OrgID, principal.UserID)
	w.WriteHeader(http.StatusNoContent)
}

func bumpDraftRevision(ctx context.Context, st *store.Store, orgID, userID string) error {
	var uid *string
	if userID != "" {
		u := userID
		uid = &u
	}
	_, err := st.Pool().Exec(ctx, `
		INSERT INTO draft_revisions (org_id, revision, updated_by, updated_at)
		VALUES ($1, 1, $2, NOW())
		ON CONFLICT (org_id) DO UPDATE
		SET revision = draft_revisions.revision + 1, updated_by = $2, updated_at = NOW()
	`, orgID, uid)
	return err
}
