package api

import (
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

// Consumer represents a tenant-scoped API consumer.
type Consumer struct {
	ID          string    `json:"id"`
	OrgID       string    `json:"org_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	OwnerEmail  string    `json:"owner_email"`
	Status      string    `json:"status"`
	Revision    int64     `json:"revision"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ConsumerHandler manages tenant-scoped consumers and their API keys.
type ConsumerHandler struct {
	Store        *store.Store
	Sessions     *auth.SessionManager
	Audit        *AuditRecorder
	KeySecret    []byte
	OnKeyChanged func(r *http.Request, orgID string)
}

// MountConsumerRoutes registers /orgs/{orgId}/consumers routes under v1.
func (h *ConsumerHandler) MountConsumerRoutes(v1 chi.Router) {
	v1.With(RequireAuth(h.Sessions), RequireOrgContext(h.Sessions), AuditMutationsMiddleware(h.Audit)).Route("/orgs/{orgId}/consumers", func(r chi.Router) {
		r.With(RequirePermission(rbac.PermConsumerRead)).Get("/", h.handleListConsumers)
		r.With(RequirePermission(rbac.PermConsumerWrite)).Post("/", h.handleCreateConsumer)
		r.With(RequirePermission(rbac.PermConsumerRead)).Get("/{consumerId}", h.handleGetConsumer)
		r.With(RequirePermission(rbac.PermConsumerWrite)).Patch("/{consumerId}", h.handleUpdateConsumer)
		r.With(RequirePermission(rbac.PermConsumerWrite)).Delete("/{consumerId}", h.handleDeleteConsumer)
	})
}

func (h *ConsumerHandler) handleListConsumers(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	limit, offset := parsePagination(r)

	var total int
	if err := h.Store.Pool().QueryRow(r.Context(), `SELECT count(*) FROM consumers WHERE org_id = $1`, orgCtx.OrgID).Scan(&total); err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not count consumers.")
		return
	}

	rows, err := h.Store.Pool().Query(r.Context(), `
		SELECT id, org_id, name, description, owner_email, status, revision, created_at, updated_at
		FROM consumers
		WHERE org_id = $1
		ORDER BY created_at DESC, id ASC
		LIMIT $2 OFFSET $3
	`, orgCtx.OrgID, limit, offset)
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not list consumers.")
		return
	}
	defer rows.Close()

	items := make([]Consumer, 0)
	for rows.Next() {
		var c Consumer
		if err := rows.Scan(&c.ID, &c.OrgID, &c.Name, &c.Description, &c.OwnerEmail, &c.Status, &c.Revision, &c.CreatedAt, &c.UpdatedAt); err != nil {
			WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not scan consumer.")
			return
		}
		items = append(items, c)
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"items":  items,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

type createConsumerRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	OwnerEmail  string `json:"owner_email"`
	Status      string `json:"status"`
}

func (h *ConsumerHandler) handleCreateConsumer(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	principal, _ := PrincipalFromContext(r.Context())

	var in createConsumerRequest
	if !DecodeStrictJSON(w, r, 64<<10, &in) {
		return
	}
	name := strings.TrimSpace(in.Name)
	desc := strings.TrimSpace(in.Description)
	ownerEmail := strings.ToLower(strings.TrimSpace(in.OwnerEmail))
	status := strings.TrimSpace(in.Status)
	if status == "" {
		status = "active"
	}

	var errs []FieldError
	if len(name) < 1 || len(name) > 120 {
		errs = append(errs, FieldError{Field: "name", Message: "must be 1..120 characters"})
	}
	if status != "active" && status != "suspended" {
		errs = append(errs, FieldError{Field: "status", Message: "must be active or suspended"})
	}
	if len(errs) > 0 {
		WriteProblem(w, r, http.StatusUnprocessableEntity, "validation_error", "Validation Failed", "Invalid consumer fields.", errs...)
		return
	}

	var c Consumer
	err := h.Store.Pool().QueryRow(r.Context(), `
		INSERT INTO consumers (org_id, name, description, owner_email, status)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, org_id, name, description, owner_email, status, revision, created_at, updated_at
	`, orgCtx.OrgID, name, desc, ownerEmail, status).Scan(
		&c.ID, &c.OrgID, &c.Name, &c.Description, &c.OwnerEmail, &c.Status, &c.Revision, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		if strings.Contains(err.Error(), "23505") || strings.Contains(err.Error(), "unique") {
			WriteProblem(w, r, http.StatusConflict, "consumer_conflict", "Conflict", "A consumer with this name already exists.")
			return
		}
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not create consumer.")
		return
	}
	_ = bumpDraftRevision(r.Context(), h.Store, orgCtx.OrgID, principal.UserID)

	w.Header().Set("ETag", formatRevisionETag(c.Revision))
	WriteJSON(w, http.StatusCreated, c)
}

func (h *ConsumerHandler) handleGetConsumer(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	consumerID := chi.URLParam(r, "consumerId")
	if !uuidPattern.MatchString(consumerID) {
		WriteProblem(w, r, http.StatusBadRequest, "invalid_consumer_id", "Invalid Consumer ID", "Consumer ID must be a valid UUID.")
		return
	}
	var c Consumer
	err := h.Store.Pool().QueryRow(r.Context(), `
		SELECT id, org_id, name, description, owner_email, status, revision, created_at, updated_at
		FROM consumers WHERE org_id = $1 AND id = $2
	`, orgCtx.OrgID, consumerID).Scan(
		&c.ID, &c.OrgID, &c.Name, &c.Description, &c.OwnerEmail, &c.Status, &c.Revision, &c.CreatedAt, &c.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		WriteProblem(w, r, http.StatusNotFound, "not_found", "Not Found", "Consumer not found.")
		return
	}
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not retrieve consumer.")
		return
	}
	w.Header().Set("ETag", formatRevisionETag(c.Revision))
	WriteJSON(w, http.StatusOK, c)
}

type updateConsumerRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	OwnerEmail  *string `json:"owner_email"`
	Status      *string `json:"status"`
	Revision    int64   `json:"revision"`
}

func (h *ConsumerHandler) handleUpdateConsumer(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	principal, _ := PrincipalFromContext(r.Context())
	consumerID := chi.URLParam(r, "consumerId")
	if !uuidPattern.MatchString(consumerID) {
		WriteProblem(w, r, http.StatusBadRequest, "invalid_consumer_id", "Invalid Consumer ID", "Consumer ID must be a valid UUID.")
		return
	}
	var in updateConsumerRequest
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

	var cur Consumer
	err := h.Store.Pool().QueryRow(r.Context(), `
		SELECT id, org_id, name, description, owner_email, status, revision, created_at, updated_at
		FROM consumers WHERE org_id = $1 AND id = $2
	`, orgCtx.OrgID, consumerID).Scan(
		&cur.ID, &cur.OrgID, &cur.Name, &cur.Description, &cur.OwnerEmail, &cur.Status, &cur.Revision, &cur.CreatedAt, &cur.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		WriteProblem(w, r, http.StatusNotFound, "not_found", "Not Found", "Consumer not found.")
		return
	}
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not load consumer.")
		return
	}

	if in.Name != nil {
		cur.Name = strings.TrimSpace(*in.Name)
	}
	if in.Description != nil {
		cur.Description = strings.TrimSpace(*in.Description)
	}
	if in.OwnerEmail != nil {
		cur.OwnerEmail = strings.ToLower(strings.TrimSpace(*in.OwnerEmail))
	}
	if in.Status != nil {
		st := strings.TrimSpace(*in.Status)
		if st != "active" && st != "suspended" {
			WriteProblem(w, r, http.StatusUnprocessableEntity, "validation_error", "Validation Failed", "status must be active or suspended.")
			return
		}
		cur.Status = st
	}

	var updated Consumer
	err = h.Store.Pool().QueryRow(r.Context(), `
		UPDATE consumers
		SET name = $3, description = $4, owner_email = $5, status = $6, revision = revision + 1, updated_at = NOW()
		WHERE org_id = $1 AND id = $2 AND revision = $7
		RETURNING id, org_id, name, description, owner_email, status, revision, created_at, updated_at
	`, orgCtx.OrgID, consumerID, cur.Name, cur.Description, cur.OwnerEmail, cur.Status, expectedRev).Scan(
		&updated.ID, &updated.OrgID, &updated.Name, &updated.Description, &updated.OwnerEmail, &updated.Status, &updated.Revision, &updated.CreatedAt, &updated.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		WriteProblem(w, r, http.StatusConflict, "revision_conflict", "Conflict", "Consumer was modified concurrently.")
		return
	}
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not update consumer.")
		return
	}
	_ = bumpDraftRevision(r.Context(), h.Store, orgCtx.OrgID, principal.UserID)

	w.Header().Set("ETag", formatRevisionETag(updated.Revision))
	WriteJSON(w, http.StatusOK, updated)
}

func (h *ConsumerHandler) handleDeleteConsumer(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	principal, _ := PrincipalFromContext(r.Context())
	consumerID := chi.URLParam(r, "consumerId")
	if !uuidPattern.MatchString(consumerID) {
		WriteProblem(w, r, http.StatusBadRequest, "invalid_consumer_id", "Invalid Consumer ID", "Consumer ID must be a valid UUID.")
		return
	}
	tag, err := h.Store.Pool().Exec(r.Context(), `DELETE FROM consumers WHERE org_id = $1 AND id = $2`, orgCtx.OrgID, consumerID)
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not delete consumer.")
		return
	}
	if tag.RowsAffected() == 0 {
		WriteProblem(w, r, http.StatusNotFound, "not_found", "Not Found", "Consumer not found.")
		return
	}
	_ = bumpDraftRevision(r.Context(), h.Store, orgCtx.OrgID, principal.UserID)
	w.WriteHeader(http.StatusNoContent)
}
