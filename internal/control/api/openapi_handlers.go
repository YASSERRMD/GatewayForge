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
		r.With(RequirePermission(rbac.PermConfigWrite)).Post("/import", h.handleImport)
		r.With(RequirePermission(rbac.PermConfigRead)).Get("/export", h.handleExport)
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

type importRouteDecision struct {
	Key             string   `json:"key"`
	Action          string   `json:"action"` // create | skip | rename | overwrite
	Name            string   `json:"name"`
	Host            string   `json:"host"`
	PathPrefix      string   `json:"path_prefix"`
	Methods         []string `json:"methods"`
	StripPrefix     bool     `json:"strip_prefix"`
	Priority        int      `json:"priority"`
	ExistingRouteID string   `json:"existing_route_id"`
}

type openAPIImportRequest struct {
	Service        openapi.ProposedService `json:"service"`
	UpstreamPoolID string                  `json:"upstream_pool_id"`
	Routes         []importRouteDecision   `json:"routes"`
}

// OpenAPIImportResult summarizes a transactional OpenAPI draft import commit.
type OpenAPIImportResult struct {
	ServiceID      string `json:"service_id"`
	ServiceCreated bool   `json:"service_created"`
	CreatedRoutes  int    `json:"created_routes"`
	UpdatedRoutes  int    `json:"updated_routes"`
	SkippedRoutes  int    `json:"skipped_routes"`
}

func (h *OpenAPIHandler) handleImport(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	principal, _ := PrincipalFromContext(r.Context())

	var in openAPIImportRequest
	if !DecodeStrictJSON(w, r, 512<<10, &in) {
		return
	}

	if !uuidPattern.MatchString(in.UpstreamPoolID) {
		WriteProblem(w, r, http.StatusUnprocessableEntity, "invalid_pool_id", "Invalid Upstream Pool", "upstream_pool_id must be a valid UUID.")
		return
	}
	slug := openapi.Slugify(in.Service.Slug)
	svcName := strings.TrimSpace(in.Service.Name)
	if svcName == "" || len(svcName) > 120 {
		WriteProblem(w, r, http.StatusUnprocessableEntity, "invalid_service", "Invalid Service", "service.name must be 1..120 characters.")
		return
	}

	tx, err := h.Store.Pool().Begin(r.Context())
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not start import transaction.")
		return
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(r.Context())
		}
	}()

	var envID string
	if err := tx.QueryRow(r.Context(), `
		SELECT env_id FROM upstream_pools WHERE org_id = $1 AND id = $2
	`, orgCtx.OrgID, in.UpstreamPoolID).Scan(&envID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			WriteProblem(w, r, http.StatusUnprocessableEntity, "invalid_pool", "Invalid Upstream Pool", "Upstream pool not found in this organization.")
			return
		}
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not verify upstream pool.")
		return
	}

	var serviceID string
	serviceCreated := false
	err = tx.QueryRow(r.Context(), `
		SELECT id FROM services WHERE org_id = $1 AND slug = $2 AND deleted_at IS NULL LIMIT 1
	`, orgCtx.OrgID, slug).Scan(&serviceID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(r.Context(), `
			INSERT INTO services (org_id, env_id, slug, name, description, enabled)
			VALUES ($1, $2, $3, $4, $5, TRUE)
			RETURNING id
		`, orgCtx.OrgID, envID, slug, svcName, strings.TrimSpace(in.Service.Description)).Scan(&serviceID)
		if err != nil {
			WriteProblem(w, r, http.StatusConflict, "service_conflict", "Service Creation Failed", "Could not create imported service in transaction.")
			return
		}
		serviceCreated = true
	} else if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not query existing service.")
		return
	}

	var res OpenAPIImportResult
	res.ServiceID = serviceID
	res.ServiceCreated = serviceCreated

	for _, rd := range in.Routes {
		action := strings.ToLower(strings.TrimSpace(rd.Action))
		if action == "" {
			action = "skip"
		}
		if action == "skip" {
			res.SkippedRoutes++
			continue
		}

		name := strings.TrimSpace(rd.Name)
		host := strings.ToLower(strings.TrimSpace(rd.Host))
		pathPrefix := strings.TrimSpace(rd.PathPrefix)
		methods, methodErrs := normalizeMethods(rd.Methods)
		if len(methodErrs) > 0 || len(name) < 1 || len(name) > 120 ||
			len(host) < 1 || len(host) > 253 || strings.ContainsAny(host, " /:@\t\r\n") ||
			!strings.HasPrefix(pathPrefix, "/") || strings.Contains(pathPrefix, "..") ||
			rd.Priority < 0 || rd.Priority > 10000 {
			WriteProblem(w, r, http.StatusUnprocessableEntity, "invalid_import_route", "Invalid Route In Import",
				"Route "+name+" failed validation; entire import transaction rolled back.")
			return
		}

		switch action {
		case "create", "rename":
			var newID string
			err := tx.QueryRow(r.Context(), `
				INSERT INTO routes (org_id, env_id, service_id, upstream_pool_id, name, host, path_prefix, methods, strip_prefix, priority, enabled)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, TRUE)
				RETURNING id
			`, orgCtx.OrgID, envID, serviceID, in.UpstreamPoolID, name, host, pathPrefix, methods, rd.StripPrefix, rd.Priority).Scan(&newID)
			if err != nil {
				WriteProblem(w, r, http.StatusConflict, "import_route_conflict", "Route Import Conflict",
					"Route "+name+" could not be created (duplicate or conflict); entire import rolled back.")
				return
			}
			res.CreatedRoutes++

		case "overwrite":
			if !uuidPattern.MatchString(rd.ExistingRouteID) {
				WriteProblem(w, r, http.StatusUnprocessableEntity, "invalid_existing_route", "Invalid Overwrite Target",
					"Overwrite action requires valid existing_route_id; entire import rolled back.")
				return
			}
			tag, err := tx.Exec(r.Context(), `
				UPDATE routes
				SET service_id = $3, upstream_pool_id = $4, name = $5, host = $6, path_prefix = $7,
				    methods = $8, strip_prefix = $9, priority = $10, revision = revision + 1, updated_at = NOW()
				WHERE org_id = $1 AND id = $2
			`, orgCtx.OrgID, rd.ExistingRouteID, serviceID, in.UpstreamPoolID, name, host, pathPrefix, methods, rd.StripPrefix, rd.Priority)
			if err != nil || tag.RowsAffected() == 0 {
				WriteProblem(w, r, http.StatusConflict, "import_overwrite_failed", "Overwrite Failed",
					"Existing route could not be overwritten; entire import rolled back.")
				return
			}
			res.UpdatedRoutes++

		default:
			WriteProblem(w, r, http.StatusUnprocessableEntity, "invalid_action", "Invalid Import Action",
				"Unsupported route action "+action+"; entire import rolled back.")
			return
		}
	}

	if err := tx.Commit(r.Context()); err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not commit OpenAPI import transaction.")
		return
	}
	committed = true

	_ = bumpDraftRevision(r.Context(), h.Store, orgCtx.OrgID, principal.UserID)
	if h.Audit != nil {
		_ = h.Audit.RecordRedacted(
			r.Context(),
			&orgCtx.OrgID,
			&principal.UserID,
			principal.Email,
			"openapi.import",
			"service",
			res.ServiceID,
			RequestIDFromContext(r.Context()),
			nil,
			map[string]any{
				"service_created": res.ServiceCreated,
				"created_routes":  res.CreatedRoutes,
				"updated_routes":  res.UpdatedRoutes,
				"skipped_routes":  res.SkippedRoutes,
			},
		)
	}

	WriteJSON(w, http.StatusCreated, res)
}

func (h *OpenAPIHandler) handleExport(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())

	var orgName string
	_ = h.Store.Pool().QueryRow(r.Context(), `SELECT name FROM organizations WHERE id = $1`, orgCtx.OrgID).Scan(&orgName)

	svcRows, err := h.Store.Pool().Query(r.Context(), `
		SELECT id, slug, name, description
		FROM services
		WHERE org_id = $1 AND deleted_at IS NULL
		ORDER BY slug ASC
	`, orgCtx.OrgID)
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not load services for export.")
		return
	}
	defer svcRows.Close()

	services := make([]openapi.ExportServiceInput, 0)
	for svcRows.Next() {
		var s openapi.ExportServiceInput
		if err := svcRows.Scan(&s.ID, &s.Slug, &s.Name, &s.Description); err != nil {
			WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not scan service for export.")
			return
		}
		services = append(services, s)
	}
	svcRows.Close()

	rtRows, err := h.Store.Pool().Query(r.Context(), `
		SELECT id, service_id, name, host, path_prefix, methods, strip_prefix, priority, enabled
		FROM routes
		WHERE org_id = $1
		ORDER BY priority DESC, path_prefix ASC, id ASC
	`, orgCtx.OrgID)
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not load routes for export.")
		return
	}
	defer rtRows.Close()

	routes := make([]openapi.ExportRouteInput, 0)
	for rtRows.Next() {
		var rt openapi.ExportRouteInput
		if err := rtRows.Scan(&rt.ID, &rt.ServiceID, &rt.Name, &rt.Host, &rt.PathPrefix, &rt.Methods, &rt.StripPrefix, &rt.Priority, &rt.Enabled); err != nil {
			WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not scan route for export.")
			return
		}
		routes = append(routes, rt)
	}

	doc := openapi.ExportInventory(orgName, services, routes)
	raw, err := openapi.MarshalExportJSON(doc)
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Export Error", "Could not serialize OpenAPI export.")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="gatewayforge-openapi.json"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}
