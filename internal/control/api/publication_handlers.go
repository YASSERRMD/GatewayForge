package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/YASSERRMD/GatewayForge/internal/control/auth"
	"github.com/YASSERRMD/GatewayForge/internal/control/compiler"
	"github.com/YASSERRMD/GatewayForge/internal/control/rbac"
	"github.com/YASSERRMD/GatewayForge/internal/control/store"
	"github.com/YASSERRMD/GatewayForge/internal/shared/netsec"
)

// ActivationEvent is emitted strictly AFTER a PostgreSQL publication/activation commit succeeds.
type ActivationEvent struct {
	OrgID       string    `json:"org_id"`
	Version     int       `json:"version"`
	Checksum    string    `json:"checksum"`
	PublishedAt time.Time `json:"published_at"`
}

// PublicationHandler serves draft inspection, validation, immutable publishing, and activation/rollback endpoints.
type PublicationHandler struct {
	Store        *store.Store
	Sessions     *auth.SessionManager
	Audit        *AuditRecorder
	TargetPolicy netsec.TargetPolicy
	OnActivated  func(ctx context.Context, ev ActivationEvent)
}

// MountPublicationRoutes mounts /orgs/{orgId}/draft* and /orgs/{orgId}/publications* under v1.
func (h *PublicationHandler) MountPublicationRoutes(v1 chi.Router) {
	v1.With(RequireAuth(h.Sessions), RequireOrgContext(h.Sessions), AuditMutationsMiddleware(h.Audit)).Route("/orgs/{orgId}", func(r chi.Router) {
		r.With(RequirePermission(rbac.PermConfigRead)).Get("/draft", h.handleGetDraft)
		r.With(RequirePermission(rbac.PermConfigRead)).Post("/draft/validate", h.handleValidateDraft)
		r.With(RequirePermission(rbac.PermReleaseRead)).Get("/publications", h.handleListPublications)
		r.With(RequirePermission(rbac.PermReleasePublish)).Post("/publications", h.handleCreatePublication)
		r.With(RequirePermission(rbac.PermReleaseActivate)).Post("/publications/{version}/activate", h.handleActivatePublication)
	})
}

func (h *PublicationHandler) handleGetDraft(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	draft, err := compiler.LoadDraftFromDB(r.Context(), h.Store.Pool(), orgCtx.OrgID)
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Draft Load Failed", "Could not load current draft configuration.")
		return
	}
	preview, err := compiler.Compile(draft, 0, time.Now().UTC())
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Draft Compile Failed", "Could not compile draft preview.")
		return
	}
	report := compiler.ValidateDraft(draft, h.TargetPolicy)
	WriteJSON(w, http.StatusOK, map[string]any{
		"draft_revision": draft.DraftRevision,
		"validation":     report,
		"preview":        preview,
	})
}

func (h *PublicationHandler) handleValidateDraft(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	draft, err := compiler.LoadDraftFromDB(r.Context(), h.Store.Pool(), orgCtx.OrgID)
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Draft Load Failed", "Could not load draft for validation.")
		return
	}
	report := compiler.ValidateDraft(draft, h.TargetPolicy)
	status := http.StatusOK
	if !report.Valid {
		status = http.StatusUnprocessableEntity
	}
	WriteJSON(w, status, report)
}

type publicationView struct {
	ID            string          `json:"id"`
	OrgID         string          `json:"org_id"`
	Version       int             `json:"version"`
	DraftRevision int64           `json:"draft_revision"`
	Checksum      string          `json:"checksum"`
	Notes         string          `json:"notes"`
	PublishedBy   *string         `json:"published_by,omitempty"`
	PublishedAt   time.Time       `json:"published_at"`
	IsActive      bool            `json:"is_active"`
	ActivationRev int64           `json:"activation_revision,omitempty"`
	Snapshot      json.RawMessage `json:"snapshot,omitempty"`
}

func (h *PublicationHandler) handleListPublications(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	limit, offset := parsePagination(r)
	includeSnap := r.URL.Query().Get("include_snapshot") == "true"

	var total int
	if err := h.Store.Pool().QueryRow(r.Context(), `SELECT count(*) FROM publications WHERE org_id = $1`, orgCtx.OrgID).Scan(&total); err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not count publications.")
		return
	}

	rows, err := h.Store.Pool().Query(r.Context(), `
		SELECT p.id, p.org_id, p.version, p.draft_revision, p.checksum_sha256, p.notes,
		       p.published_by::TEXT, p.published_at,
		       (ap.publication_id = p.id) AS is_active,
		       COALESCE(ap.activation_revision, 0),
		       p.snapshot_json
		FROM publications p
		LEFT JOIN active_publications ap ON ap.org_id = p.org_id
		WHERE p.org_id = $1
		ORDER BY p.version DESC
		LIMIT $2 OFFSET $3
	`, orgCtx.OrgID, limit, offset)
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not list publications.")
		return
	}
	defer rows.Close()

	items := make([]publicationView, 0)
	for rows.Next() {
		var pv publicationView
		var isActive *bool
		var snapRaw json.RawMessage
		if err := rows.Scan(
			&pv.ID, &pv.OrgID, &pv.Version, &pv.DraftRevision, &pv.Checksum, &pv.Notes,
			&pv.PublishedBy, &pv.PublishedAt, &isActive, &pv.ActivationRev, &snapRaw,
		); err != nil {
			WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not scan publication.")
			return
		}
		if isActive != nil && *isActive {
			pv.IsActive = true
		}
		if includeSnap {
			pv.Snapshot = snapRaw
		}
		items = append(items, pv)
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"items":  items,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

type publishRequest struct {
	ExpectedDraftRevision *int64 `json:"expected_draft_revision"`
	Notes                 string `json:"notes"`
	Activate              *bool  `json:"activate"`
}

func (h *PublicationHandler) handleCreatePublication(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	principal, _ := PrincipalFromContext(r.Context())

	var in publishRequest
	if r.Body != nil && r.ContentLength != 0 {
		if !DecodeStrictJSON(w, r, 64<<10, &in) {
			return
		}
	}
	activateNow := true
	if in.Activate != nil {
		activateNow = *in.Activate
	}
	if activateNow && !rbac.Allows(orgCtx.Role, rbac.PermReleaseActivate) {
		WriteProblem(w, r, http.StatusForbidden, "activate_forbidden", "Forbidden", "Your role cannot activate publications.")
		return
	}

	tx, err := h.Store.Pool().Begin(r.Context())
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not start publication transaction.")
		return
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(r.Context())
		}
	}()

	// Ensure draft_revisions row exists and lock it FOR UPDATE to serialize concurrent publishers.
	if _, err := tx.Exec(r.Context(), `
		INSERT INTO draft_revisions (org_id, revision) VALUES ($1, 1) ON CONFLICT (org_id) DO NOTHING
	`, orgCtx.OrgID); err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not initialize draft revision.")
		return
	}
	var currentDraftRev int64
	if err := tx.QueryRow(r.Context(), `
		SELECT revision FROM draft_revisions WHERE org_id = $1 FOR UPDATE
	`, orgCtx.OrgID).Scan(&currentDraftRev); err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not lock draft revision.")
		return
	}

	if in.ExpectedDraftRevision != nil && *in.ExpectedDraftRevision != currentDraftRev {
		WriteProblem(w, r, http.StatusConflict, "draft_revision_conflict", "Conflict", "Draft has changed since you loaded it. Refresh, re-validate, and publish again.")
		return
	}

	draft, err := compiler.LoadDraftFromDB(r.Context(), h.Store.Pool(), orgCtx.OrgID)
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Draft Load Failed", "Could not load draft for publication.")
		return
	}
	draft.DraftRevision = currentDraftRev
	report := compiler.ValidateDraft(draft, h.TargetPolicy)
	if !report.Valid {
		WriteJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"code":       "invalid_draft",
			"message":    "Draft configuration failed validation and cannot be published.",
			"validation": report,
		})
		return
	}

	var maxVersion int
	if err := tx.QueryRow(r.Context(), `
		SELECT COALESCE(MAX(version), 0) FROM publications WHERE org_id = $1
	`, orgCtx.OrgID).Scan(&maxVersion); err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not determine next publication version.")
		return
	}
	nextVersion := maxVersion + 1
	publishedAt := time.Now().UTC().Truncate(time.Second)

	snap, err := compiler.Compile(draft, nextVersion, publishedAt)
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Compile Error", "Could not compile snapshot.")
		return
	}
	snapJSON, err := json.Marshal(snap)
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Serialize Error", "Could not serialize snapshot.")
		return
	}

	var pubID string
	if err := tx.QueryRow(r.Context(), `
		INSERT INTO publications (org_id, version, draft_revision, snapshot_json, checksum_sha256, notes, published_by, published_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id
	`, orgCtx.OrgID, nextVersion, currentDraftRev, snapJSON, snap.Checksum, strings.TrimSpace(in.Notes), principal.UserID, publishedAt).Scan(&pubID); err != nil {
		WriteProblem(w, r, http.StatusConflict, "publish_conflict", "Conflict", "Concurrent publication conflict; please retry.")
		return
	}

	var activationRev int64
	if activateNow {
		if err := tx.QueryRow(r.Context(), `
			INSERT INTO active_publications (org_id, publication_id, activation_revision, activated_by, activated_at)
			VALUES ($1, $2, 1, $3, NOW())
			ON CONFLICT (org_id) DO UPDATE
			SET publication_id = EXCLUDED.publication_id,
			    activation_revision = active_publications.activation_revision + 1,
			    activated_by = EXCLUDED.activated_by,
			    activated_at = NOW()
			RETURNING activation_revision
		`, orgCtx.OrgID, pubID, principal.UserID).Scan(&activationRev); err != nil {
			WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not update active publication pointer.")
			return
		}
		if _, err := tx.Exec(r.Context(), `
			INSERT INTO publication_activations (org_id, publication_id, is_rollback, reason, activated_by)
			VALUES ($1, $2, FALSE, $3, $4)
		`, orgCtx.OrgID, pubID, strings.TrimSpace(in.Notes), principal.UserID); err != nil {
			WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not log publication activation.")
			return
		}
	}

	if _, err := tx.Exec(r.Context(), `
		UPDATE draft_revisions SET revision = revision + 1, updated_by = $2, updated_at = NOW() WHERE org_id = $1
	`, orgCtx.OrgID, principal.UserID); err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not advance draft revision.")
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not commit publication.")
		return
	}
	committed = true

	// Emit post-commit invalidation hint AFTER PostgreSQL commit succeeds!
	if activateNow && h.OnActivated != nil {
		h.OnActivated(r.Context(), ActivationEvent{
			OrgID:       orgCtx.OrgID,
			Version:     nextVersion,
			Checksum:    snap.Checksum,
			PublishedAt: publishedAt,
		})
	}

	uid := principal.UserID
	WriteJSON(w, http.StatusCreated, publicationView{
		ID:            pubID,
		OrgID:         orgCtx.OrgID,
		Version:       nextVersion,
		DraftRevision: currentDraftRev,
		Checksum:      snap.Checksum,
		Notes:         strings.TrimSpace(in.Notes),
		PublishedBy:   &uid,
		PublishedAt:   publishedAt,
		IsActive:      activateNow,
		ActivationRev: activationRev,
	})
}

type activateRequest struct {
	ExpectedActivationRevision *int64 `json:"expected_activation_revision"`
	Reason                     string `json:"reason"`
}

func (h *PublicationHandler) handleActivatePublication(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	principal, _ := PrincipalFromContext(r.Context())

	version, err := strconv.Atoi(chi.URLParam(r, "version"))
	if err != nil || version < 1 {
		WriteProblem(w, r, http.StatusBadRequest, "invalid_version", "Invalid Version", "Publication version must be a positive integer.")
		return
	}

	var in activateRequest
	if r.Body != nil && r.ContentLength != 0 {
		if !DecodeStrictJSON(w, r, 32<<10, &in) {
			return
		}
	}

	tx, err := h.Store.Pool().Begin(r.Context())
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not start activation transaction.")
		return
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(r.Context())
		}
	}()

	var targetPubID, checksum string
	var publishedAt time.Time
	err = tx.QueryRow(r.Context(), `
		SELECT id, checksum_sha256, published_at FROM publications WHERE org_id = $1 AND version = $2
	`, orgCtx.OrgID, version).Scan(&targetPubID, &checksum, &publishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		WriteProblem(w, r, http.StatusNotFound, "not_found", "Not Found", "Publication version not found.")
		return
	}
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not load publication.")
		return
	}

	var currentActiveVer int
	var currentActRev int64
	err = tx.QueryRow(r.Context(), `
		SELECT p.version, ap.activation_revision
		FROM active_publications ap
		JOIN publications p ON p.id = ap.publication_id
		WHERE ap.org_id = $1
		FOR UPDATE OF ap
	`, orgCtx.OrgID).Scan(&currentActiveVer, &currentActRev)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not lock active publication.")
		return
	}

	if in.ExpectedActivationRevision != nil && *in.ExpectedActivationRevision != currentActRev {
		WriteProblem(w, r, http.StatusConflict, "activation_conflict", "Conflict", "Active publication changed concurrently. Refresh and retry.")
		return
	}

	isRollback := currentActiveVer > 0 && version < currentActiveVer
	if isRollback && !rbac.Allows(orgCtx.Role, rbac.PermReleaseRollback) {
		WriteProblem(w, r, http.StatusForbidden, "rollback_forbidden", "Forbidden", "Your role cannot perform rollbacks.")
		return
	}

	var nextActRev int64
	if err := tx.QueryRow(r.Context(), `
		INSERT INTO active_publications (org_id, publication_id, activation_revision, activated_by, activated_at)
		VALUES ($1, $2, 1, $3, NOW())
		ON CONFLICT (org_id) DO UPDATE
		SET publication_id = EXCLUDED.publication_id,
		    activation_revision = active_publications.activation_revision + 1,
		    activated_by = EXCLUDED.activated_by,
		    activated_at = NOW()
		RETURNING activation_revision
	`, orgCtx.OrgID, targetPubID, principal.UserID).Scan(&nextActRev); err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not activate publication.")
		return
	}

	if _, err := tx.Exec(r.Context(), `
		INSERT INTO publication_activations (org_id, publication_id, is_rollback, reason, activated_by)
		VALUES ($1, $2, $3, $4, $5)
	`, orgCtx.OrgID, targetPubID, isRollback, strings.TrimSpace(in.Reason), principal.UserID); err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not log activation event.")
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not commit activation.")
		return
	}
	committed = true

	if h.OnActivated != nil {
		h.OnActivated(r.Context(), ActivationEvent{
			OrgID:       orgCtx.OrgID,
			Version:     version,
			Checksum:    checksum,
			PublishedAt: publishedAt,
		})
	}

	WriteJSON(w, http.StatusOK, map[string]any{
		"org_id":              orgCtx.OrgID,
		"publication_id":      targetPubID,
		"version":             version,
		"checksum":            checksum,
		"is_rollback":         isRollback,
		"activation_revision": nextActRev,
	})
}
