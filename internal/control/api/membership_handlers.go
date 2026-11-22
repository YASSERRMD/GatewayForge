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

// MembershipHandler manages organization member listing, creation, role changes, and revocation.
type MembershipHandler struct {
	Store    *store.Store
	Sessions *auth.SessionManager
	Hasher   auth.PasswordHasher
	Audit    *AuditRecorder
}

// MountMembershipRoutes mounts /orgs/{orgId}/members routes under an authenticated org subrouter or directly on v1.
func (h *MembershipHandler) MountMembershipRoutes(v1 chi.Router) {
	v1.With(RequireAuth(h.Sessions), RequireOrgContext(h.Sessions), AuditMutationsMiddleware(h.Audit)).Route("/orgs/{orgId}/members", func(r chi.Router) {
		r.With(RequirePermission(rbac.PermMemberRead)).Get("/", h.handleListMembers)
		r.With(RequirePermission(rbac.PermMemberWrite)).Post("/", h.handleAddMember)
		r.With(RequirePermission(rbac.PermMemberWrite)).Patch("/{userId}", h.handleUpdateMemberRole)
		r.With(RequirePermission(rbac.PermMemberWrite)).Delete("/{userId}", h.handleRevokeMember)
	})
}

type memberView struct {
	MembershipID string    `json:"membership_id"`
	UserID       string    `json:"user_id"`
	Email        string    `json:"email"`
	DisplayName  string    `json:"display_name"`
	Role         string    `json:"role"`
	IsActive     bool      `json:"is_active"`
	CreatedAt    time.Time `json:"created_at"`
}

func (h *MembershipHandler) handleListMembers(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	limit, offset := parsePagination(r)

	var total int
	if err := h.Store.Pool().QueryRow(r.Context(), `
		SELECT count(*) FROM memberships WHERE org_id = $1
	`, orgCtx.OrgID).Scan(&total); err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not count members.")
		return
	}

	rows, err := h.Store.Pool().Query(r.Context(), `
		SELECT m.id, u.id, u.email, u.display_name, m.role, u.is_active, m.created_at
		FROM memberships m
		JOIN users u ON u.id = m.user_id
		WHERE m.org_id = $1
		ORDER BY u.email ASC
		LIMIT $2 OFFSET $3
	`, orgCtx.OrgID, limit, offset)
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not list members.")
		return
	}
	defer rows.Close()

	items := make([]memberView, 0)
	for rows.Next() {
		var mv memberView
		if err := rows.Scan(&mv.MembershipID, &mv.UserID, &mv.Email, &mv.DisplayName, &mv.Role, &mv.IsActive, &mv.CreatedAt); err != nil {
			WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not read member.")
			return
		}
		items = append(items, mv)
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"items":  items,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

type addMemberRequest struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password,omitempty"`
	Role        string `json:"role"`
}

func (h *MembershipHandler) handleAddMember(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())

	var in addMemberRequest
	if !DecodeStrictJSON(w, r, 64<<10, &in) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if !strings.Contains(email, "@") || len(email) < 5 {
		WriteProblem(w, r, http.StatusUnprocessableEntity, "validation_error", "Validation Failed", "Valid email is required.")
		return
	}
	role, err := rbac.ParseRole(strings.TrimSpace(in.Role))
	if err != nil {
		WriteProblem(w, r, http.StatusUnprocessableEntity, "validation_error", "Validation Failed", "Role must be one of owner, admin, developer, viewer.")
		return
	}
	if role == rbac.RoleOwner && !rbac.Allows(orgCtx.Role, rbac.PermMemberOwnerGrant) {
		WriteProblem(w, r, http.StatusForbidden, "owner_grant_forbidden", "Forbidden", "Only organization owners may grant the owner role.")
		return
	}

	tx, err := h.Store.Pool().Begin(r.Context())
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not start transaction.")
		return
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(r.Context())
		}
	}()

	var userID, displayName string
	var active bool
	err = tx.QueryRow(r.Context(), `SELECT id, display_name, is_active FROM users WHERE email = $1`, email).
		Scan(&userID, &displayName, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		displayName = strings.TrimSpace(in.DisplayName)
		if displayName == "" || len(in.Password) < 12 || len(in.Password) > 128 {
			WriteProblem(w, r, http.StatusUnprocessableEntity, "validation_error", "Validation Failed", "New users require display_name and a password of 12..128 characters.")
			return
		}
		if h.Hasher == nil {
			WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Configuration Error", "Password hasher not configured.")
			return
		}
		pwHash, hashErr := h.Hasher.Hash(in.Password)
		if hashErr != nil {
			WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Hash Error", "Could not secure password.")
			return
		}
		if err := tx.QueryRow(r.Context(), `
			INSERT INTO users (email, display_name, password_hash) VALUES ($1, $2, $3) RETURNING id, is_active
		`, email, displayName, pwHash).Scan(&userID, &active); err != nil {
			WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not create user.")
			return
		}
	} else if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not look up user.")
		return
	}

	var mv memberView
	err = tx.QueryRow(r.Context(), `
		INSERT INTO memberships (org_id, user_id, role)
		VALUES ($1, $2, $3)
		RETURNING id, created_at
	`, orgCtx.OrgID, userID, string(role)).Scan(&mv.MembershipID, &mv.CreatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "23505") || strings.Contains(err.Error(), "unique") {
			WriteProblem(w, r, http.StatusConflict, "member_exists", "Conflict", "User is already a member of this organization.")
			return
		}
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not add member.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not commit membership.")
		return
	}
	committed = true

	mv.UserID = userID
	mv.Email = email
	mv.DisplayName = displayName
	mv.Role = string(role)
	mv.IsActive = active
	WriteJSON(w, http.StatusCreated, mv)
}

type updateMemberRoleRequest struct {
	Role string `json:"role"`
}

func (h *MembershipHandler) handleUpdateMemberRole(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	targetUserID := chi.URLParam(r, "userId")
	if !uuidPattern.MatchString(targetUserID) {
		WriteProblem(w, r, http.StatusBadRequest, "invalid_user_id", "Invalid User ID", "User ID must be a valid UUID.")
		return
	}

	var in updateMemberRoleRequest
	if !DecodeStrictJSON(w, r, 16<<10, &in) {
		return
	}
	newRole, err := rbac.ParseRole(strings.TrimSpace(in.Role))
	if err != nil {
		WriteProblem(w, r, http.StatusUnprocessableEntity, "validation_error", "Validation Failed", "Role must be one of owner, admin, developer, viewer.")
		return
	}

	tx, err := h.Store.Pool().Begin(r.Context())
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not start transaction.")
		return
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(r.Context())
		}
	}()

	// Lock organization row to serialize concurrent owner role modifications cleanly.
	var lockOrgID string
	if err := tx.QueryRow(r.Context(), `SELECT id FROM organizations WHERE id = $1 FOR UPDATE`, orgCtx.OrgID).Scan(&lockOrgID); err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not lock organization.")
		return
	}

	var currentRoleRaw string
	err = tx.QueryRow(r.Context(), `SELECT role FROM memberships WHERE org_id = $1 AND user_id = $2 FOR UPDATE`, orgCtx.OrgID, targetUserID).Scan(&currentRoleRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		WriteProblem(w, r, http.StatusNotFound, "not_found", "Not Found", "Membership not found.")
		return
	}
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not load membership.")
		return
	}
	currentRole := rbac.Role(currentRoleRaw)

	if (newRole == rbac.RoleOwner || currentRole == rbac.RoleOwner) && !rbac.Allows(orgCtx.Role, rbac.PermMemberOwnerGrant) {
		WriteProblem(w, r, http.StatusForbidden, "owner_grant_forbidden", "Forbidden", "Only organization owners may promote or demote owners.")
		return
	}

	if currentRole == rbac.RoleOwner && newRole != rbac.RoleOwner {
		var ownerCount int
		if err := tx.QueryRow(r.Context(), `SELECT count(*) FROM memberships WHERE org_id = $1 AND role = 'owner'`, orgCtx.OrgID).Scan(&ownerCount); err != nil {
			WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not count owners.")
			return
		}
		if ownerCount <= 1 {
			WriteProblem(w, r, http.StatusConflict, "last_owner_protected", "Conflict", "Cannot demote the last remaining owner of an organization.")
			return
		}
	}

	if _, err := tx.Exec(r.Context(), `
		UPDATE memberships SET role = $3, updated_at = NOW() WHERE org_id = $1 AND user_id = $2
	`, orgCtx.OrgID, targetUserID, string(newRole)); err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not update role.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not commit role update.")
		return
	}
	committed = true

	WriteJSON(w, http.StatusOK, map[string]string{
		"org_id":  orgCtx.OrgID,
		"user_id": targetUserID,
		"role":    string(newRole),
	})
}

func (h *MembershipHandler) handleRevokeMember(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	targetUserID := chi.URLParam(r, "userId")
	if !uuidPattern.MatchString(targetUserID) {
		WriteProblem(w, r, http.StatusBadRequest, "invalid_user_id", "Invalid User ID", "User ID must be a valid UUID.")
		return
	}

	tx, err := h.Store.Pool().Begin(r.Context())
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not start transaction.")
		return
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(r.Context())
		}
	}()

	var lockOrgID string
	if err := tx.QueryRow(r.Context(), `SELECT id FROM organizations WHERE id = $1 FOR UPDATE`, orgCtx.OrgID).Scan(&lockOrgID); err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not lock organization.")
		return
	}

	var currentRoleRaw string
	err = tx.QueryRow(r.Context(), `SELECT role FROM memberships WHERE org_id = $1 AND user_id = $2 FOR UPDATE`, orgCtx.OrgID, targetUserID).Scan(&currentRoleRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		WriteProblem(w, r, http.StatusNotFound, "not_found", "Not Found", "Membership not found.")
		return
	}
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not load membership.")
		return
	}
	currentRole := rbac.Role(currentRoleRaw)

	if currentRole == rbac.RoleOwner {
		if !rbac.Allows(orgCtx.Role, rbac.PermMemberOwnerGrant) {
			WriteProblem(w, r, http.StatusForbidden, "owner_grant_forbidden", "Forbidden", "Only organization owners may remove another owner.")
			return
		}
		var ownerCount int
		if err := tx.QueryRow(r.Context(), `SELECT count(*) FROM memberships WHERE org_id = $1 AND role = 'owner'`, orgCtx.OrgID).Scan(&ownerCount); err != nil {
			WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not count owners.")
			return
		}
		if ownerCount <= 1 {
			WriteProblem(w, r, http.StatusConflict, "last_owner_protected", "Conflict", "Cannot remove the last remaining owner of an organization.")
			return
		}
	}

	if _, err := tx.Exec(r.Context(), `DELETE FROM memberships WHERE org_id = $1 AND user_id = $2`, orgCtx.OrgID, targetUserID); err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not revoke membership.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not commit revocation.")
		return
	}
	committed = true

	w.WriteHeader(http.StatusNoContent)
}
