package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/YASSERRMD/GatewayForge/internal/control/auth"
	"github.com/YASSERRMD/GatewayForge/internal/control/compiler"
	"github.com/YASSERRMD/GatewayForge/internal/control/rbac"
	"github.com/YASSERRMD/GatewayForge/internal/control/store"
	"github.com/YASSERRMD/GatewayForge/internal/shared/netsec"
)

// PublicationHandler serves draft inspection, validation, immutable publishing, and activation/rollback endpoints.
type PublicationHandler struct {
	Store        *store.Store
	Sessions     *auth.SessionManager
	Audit        *AuditRecorder
	TargetPolicy netsec.TargetPolicy
}

// MountPublicationRoutes mounts /orgs/{orgId}/draft* and /orgs/{orgId}/publications* under v1.
func (h *PublicationHandler) MountPublicationRoutes(v1 chi.Router) {
	v1.With(RequireAuth(h.Sessions), RequireOrgContext(h.Sessions), AuditMutationsMiddleware(h.Audit)).Route("/orgs/{orgId}", func(r chi.Router) {
		r.With(RequirePermission(rbac.PermConfigRead)).Get("/draft", h.handleGetDraft)
		r.With(RequirePermission(rbac.PermConfigRead)).Post("/draft/validate", h.handleValidateDraft)
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
