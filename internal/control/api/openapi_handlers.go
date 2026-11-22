package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/YASSERRMD/GatewayForge/internal/control/auth"
	"github.com/YASSERRMD/GatewayForge/internal/control/openapi"
	"github.com/YASSERRMD/GatewayForge/internal/control/rbac"
	"github.com/YASSERRMD/GatewayForge/internal/control/store"
)

// OpenAPIHandler serves OpenAPI 3.0/3.1 preview, transactional draft import, and export endpoints.
type OpenAPIHandler struct {
	Store    *store.Store
	Sessions *auth.SessionManager
	Audit    *AuditRecorder
}

// MountOpenAPIRoutes registers /orgs/{orgId}/openapi/* routes under v1.
func (h *OpenAPIHandler) MountOpenAPIRoutes(v1 chi.Router) {
	orgAuth := v1.With(RequireAuth(h.Sessions), RequireOrgContext(h.Sessions), AuditMutationsMiddleware(h.Audit))
	orgAuth.Route("/orgs/{orgId}/openapi", func(r chi.Router) {
		r.With(RequirePermission(rbac.PermConfigWrite)).Post("/preview", h.handlePreview)
	})
}

type openAPIPreviewRequest struct {
	Spec         string `json:"spec"`
	OverrideHost string `json:"override_host"`
}

// PreviewRouteWithConflict annotates a proposed OpenAPI route with existing draft collision metadata.
type PreviewRouteWithConflict struct {
	openapi.ProposedRoute
	Conflict        bool   `json:"conflict"`
	ExistingRouteID string `json:"existing_route_id,omitempty"`
	ConflictReason  string `json:"conflict_reason,omitempty"`
	DefaultAction   string `json:"default_action"` // "create" | "skip"
}

// OpenAPIPreviewResponse is returned by POST /orgs/{orgId}/openapi/preview.
type OpenAPIPreviewResponse struct {
	OpenAPIVersion    string                     `json:"openapi_version"`
	Service           openapi.ProposedService    `json:"service"`
	ServiceExists     bool                       `json:"service_exists"`
	ExistingServiceID string                     `json:"existing_service_id,omitempty"`
	DefaultHost       string                     `json:"default_host"`
	BasePath          string                     `json:"base_path"`
	Routes            []PreviewRouteWithConflict `json:"routes"`
	Warnings          []openapi.MappingWarning   `json:"warnings"`
}

func (h *OpenAPIHandler) handlePreview(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())

	var in openAPIPreviewRequest
	if !DecodeStrictJSON(w, r, openapi.MaxSpecBytes+(64<<10), &in) {
		return
	}

	doc, err := openapi.Parse([]byte(in.Spec))
	if err != nil {
		var pe *openapi.ParseError
		if errors.As(err, &pe) {
			WriteProblem(w, r, http.StatusUnprocessableEntity, pe.Code, "Invalid OpenAPI Specification", pe.Message)
			return
		}
		WriteProblem(w, r, http.StatusUnprocessableEntity, "invalid_openapi", "Invalid OpenAPI Specification", err.Error())
		return
	}

	plan := openapi.MapToPreview(doc, in.OverrideHost)

	var existingSvcID string
	svcExists := false
	err = h.Store.Pool().QueryRow(r.Context(), `
		SELECT id FROM services
		WHERE org_id = $1 AND slug = $2 AND deleted_at IS NULL
		LIMIT 1
	`, orgCtx.OrgID, plan.Service.Slug).Scan(&existingSvcID)
	if err == nil {
		svcExists = true
	} else if !errors.Is(err, pgx.ErrNoRows) {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not inspect existing services.")
		return
	}

	type existingRouteRow struct {
		id         string
		name       string
		host       string
		pathPrefix string
		methods    []string
	}
	rows, err := h.Store.Pool().Query(r.Context(), `
		SELECT id, name, host, path_prefix, methods
		FROM routes
		WHERE org_id = $1
	`, orgCtx.OrgID)
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not inspect existing routes.")
		return
	}
	defer rows.Close()

	existingRoutes := make([]existingRouteRow, 0)
	for rows.Next() {
		var er existingRouteRow
		if scanErr := rows.Scan(&er.id, &er.name, &er.host, &er.pathPrefix, &er.methods); scanErr != nil {
			WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not scan existing route.")
			return
		}
		existingRoutes = append(existingRoutes, er)
	}

	annotated := make([]PreviewRouteWithConflict, 0, len(plan.Routes))
	for _, pr := range plan.Routes {
		item := PreviewRouteWithConflict{
			ProposedRoute: pr,
			DefaultAction: "create",
		}
		for _, er := range existingRoutes {
			if strings.EqualFold(er.host, pr.Host) && er.pathPrefix == pr.PathPrefix {
				item.Conflict = true
				item.ExistingRouteID = er.id
				item.ConflictReason = "An existing route already uses this host and path_prefix."
				item.DefaultAction = "skip"
				break
			}
			if strings.EqualFold(er.name, pr.Name) {
				item.Conflict = true
				item.ExistingRouteID = er.id
				item.ConflictReason = "An existing route already uses this route name."
				item.DefaultAction = "skip"
				break
			}
		}
		annotated = append(annotated, item)
	}

	WriteJSON(w, http.StatusOK, OpenAPIPreviewResponse{
		OpenAPIVersion:    plan.OpenAPIVersion,
		Service:           plan.Service,
		ServiceExists:     svcExists,
		ExistingServiceID: existingSvcID,
		DefaultHost:       plan.DefaultHost,
		BasePath:          plan.BasePath,
		Routes:            annotated,
		Warnings:          plan.Warnings,
	})
}
