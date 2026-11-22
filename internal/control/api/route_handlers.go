package api

import (
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/YASSERRMD/GatewayForge/internal/control/auth"
	"github.com/YASSERRMD/GatewayForge/internal/control/rbac"
	"github.com/YASSERRMD/GatewayForge/internal/control/store"
)

var allowedRouteMethods = map[string]struct{}{
	"GET": {}, "HEAD": {}, "POST": {}, "PUT": {}, "PATCH": {}, "DELETE": {}, "OPTIONS": {},
}

// RouteHandler serves tenant-scoped route CRUD and precedence preview endpoints.
type RouteHandler struct {
	Store    *store.Store
	Sessions *auth.SessionManager
	Audit    *AuditRecorder
}

// MountRouteRoutes mounts /orgs/{orgId}/routes endpoints under v1.
func (h *RouteHandler) MountRouteRoutes(v1 chi.Router) {
	v1.With(RequireAuth(h.Sessions), RequireOrgContext(h.Sessions), AuditMutationsMiddleware(h.Audit)).Route("/orgs/{orgId}/routes", func(r chi.Router) {
		r.With(RequirePermission(rbac.PermConfigRead)).Get("/", h.handleListRoutes)
		r.With(RequirePermission(rbac.PermConfigWrite)).Post("/", h.handleCreateRoute)
		r.With(RequirePermission(rbac.PermConfigRead)).Get("/preview", h.handlePreviewRoutes)
		r.With(RequirePermission(rbac.PermConfigRead)).Get("/{routeId}", h.handleGetRoute)
		r.With(RequirePermission(rbac.PermConfigWrite)).Patch("/{routeId}", h.handleUpdateRoute)
		r.With(RequirePermission(rbac.PermConfigWrite)).Delete("/{routeId}", h.handleDeleteRoute)
	})
}

func (h *RouteHandler) handleListRoutes(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	limit, offset := parsePagination(r)
	search := strings.TrimSpace(r.URL.Query().Get("search"))

	var total int
	var err error
	if search != "" {
		like := "%" + search + "%"
		err = h.Store.Pool().QueryRow(r.Context(), `
			SELECT count(*) FROM routes
			WHERE org_id = $1 AND (name ILIKE $2 OR host ILIKE $2 OR path_prefix ILIKE $2)
		`, orgCtx.OrgID, like).Scan(&total)
	} else {
		err = h.Store.Pool().QueryRow(r.Context(), `SELECT count(*) FROM routes WHERE org_id = $1`, orgCtx.OrgID).Scan(&total)
	}
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not count routes.")
		return
	}

	var rows pgx.Rows
	if search != "" {
		like := "%" + search + "%"
		rows, err = h.Store.Pool().Query(r.Context(), `
			SELECT id, org_id, env_id, service_id, upstream_pool_id,
			       name, host, path_prefix, methods, strip_prefix, priority, enabled,
			       revision, created_at, updated_at
			FROM routes
			WHERE org_id = $1 AND (name ILIKE $2 OR host ILIKE $2 OR path_prefix ILIKE $2)
			ORDER BY priority DESC, length(path_prefix) DESC, id ASC
			LIMIT $3 OFFSET $4
		`, orgCtx.OrgID, like, limit, offset)
	} else {
		rows, err = h.Store.Pool().Query(r.Context(), `
			SELECT id, org_id, env_id, service_id, upstream_pool_id,
			       name, host, path_prefix, methods, strip_prefix, priority, enabled,
			       revision, created_at, updated_at
			FROM routes
			WHERE org_id = $1
			ORDER BY priority DESC, length(path_prefix) DESC, id ASC
			LIMIT $2 OFFSET $3
		`, orgCtx.OrgID, limit, offset)
	}
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not list routes.")
		return
	}
	defer rows.Close()

	items := make([]store.Route, 0)
	for rows.Next() {
		var rt store.Route
		if err := rows.Scan(
			&rt.ID, &rt.OrgID, &rt.EnvID, &rt.ServiceID, &rt.UpstreamPoolID,
			&rt.Name, &rt.Host, &rt.PathPrefix, &rt.Methods, &rt.StripPrefix, &rt.Priority, &rt.Enabled,
			&rt.Revision, &rt.CreatedAt, &rt.UpdatedAt,
		); err != nil {
			WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not scan route.")
			return
		}
		items = append(items, rt)
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"items":  items,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// RouteConflict describes two enabled routes that share identical host, path_prefix, priority, and at least one HTTP method.
type RouteConflict struct {
	RouteA  string   `json:"route_a"`
	RouteB  string   `json:"route_b"`
	Host    string   `json:"host"`
	Path    string   `json:"path_prefix"`
	Methods []string `json:"methods"`
}

func (h *RouteHandler) handlePreviewRoutes(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	rows, err := h.Store.Pool().Query(r.Context(), `
		SELECT id, org_id, env_id, service_id, upstream_pool_id,
		       name, host, path_prefix, methods, strip_prefix, priority, enabled,
		       revision, created_at, updated_at
		FROM routes
		WHERE org_id = $1 AND enabled = TRUE
		ORDER BY priority DESC, length(path_prefix) DESC, id ASC
	`, orgCtx.OrgID)
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not preview routes.")
		return
	}
	defer rows.Close()

	routes := make([]store.Route, 0)
	for rows.Next() {
		var rt store.Route
		if err := rows.Scan(
			&rt.ID, &rt.OrgID, &rt.EnvID, &rt.ServiceID, &rt.UpstreamPoolID,
			&rt.Name, &rt.Host, &rt.PathPrefix, &rt.Methods, &rt.StripPrefix, &rt.Priority, &rt.Enabled,
			&rt.Revision, &rt.CreatedAt, &rt.UpdatedAt,
		); err != nil {
			WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not scan route.")
			return
		}
		routes = append(routes, rt)
	}

	conflicts := DetectRouteConflicts(routes)
	WriteJSON(w, http.StatusOK, map[string]any{
		"ordered_routes": routes,
		"conflicts":      conflicts,
	})
}

// DetectRouteConflicts finds ambiguous overlapping route pairs at the same host, path_prefix, and priority.
func DetectRouteConflicts(routes []store.Route) []RouteConflict {
	conflicts := make([]RouteConflict, 0)
	for i := 0; i < len(routes); i++ {
		if !routes[i].Enabled {
			continue
		}
		for j := i + 1; j < len(routes); j++ {
			if !routes[j].Enabled {
				continue
			}
			a, b := routes[i], routes[j]
			if a.Host != b.Host || a.PathPrefix != b.PathPrefix || a.Priority != b.Priority {
				continue
			}
			overlap := intersectStrings(a.Methods, b.Methods)
			if len(overlap) > 0 {
				conflicts = append(conflicts, RouteConflict{
					RouteA:  a.ID,
					RouteB:  b.ID,
					Host:    a.Host,
					Path:    a.PathPrefix,
					Methods: overlap,
				})
			}
		}
	}
	return conflicts
}

func intersectStrings(a, b []string) []string {
	set := make(map[string]struct{}, len(a))
	for _, x := range a {
		set[x] = struct{}{}
	}
	var out []string
	for _, y := range b {
		if _, ok := set[y]; ok {
			out = append(out, y)
		}
	}
	sort.Strings(out)
	return out
}

type createRouteRequest struct {
	EnvID          string   `json:"env_id"`
	ServiceID      string   `json:"service_id"`
	UpstreamPoolID string   `json:"upstream_pool_id"`
	Name           string   `json:"name"`
	Host           string   `json:"host"`
	PathPrefix     string   `json:"path_prefix"`
	Methods        []string `json:"methods"`
	StripPrefix    bool     `json:"strip_prefix"`
	Priority       *int     `json:"priority"`
	Enabled        *bool    `json:"enabled"`
}

func (h *RouteHandler) handleCreateRoute(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	principal, _ := PrincipalFromContext(r.Context())

	var in createRouteRequest
	if !DecodeStrictJSON(w, r, 64<<10, &in) {
		return
	}

	name := strings.TrimSpace(in.Name)
	host := strings.ToLower(strings.TrimSpace(in.Host))
	pathPrefix := strings.TrimSpace(in.PathPrefix)
	priority := 100
	if in.Priority != nil {
		priority = *in.Priority
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}

	methods, methodErrs := normalizeMethods(in.Methods)
	var errs []FieldError
	errs = append(errs, methodErrs...)
	if len(name) < 1 || len(name) > 120 {
		errs = append(errs, FieldError{Field: "name", Message: "must be 1..120 characters"})
	}
	if len(host) < 1 || len(host) > 253 || strings.ContainsAny(host, " /:@\t\r\n") {
		errs = append(errs, FieldError{Field: "host", Message: "must be a valid lowercase hostname without scheme or port"})
	}
	if !strings.HasPrefix(pathPrefix, "/") || strings.Contains(pathPrefix, "..") {
		errs = append(errs, FieldError{Field: "path_prefix", Message: "must start with '/' and contain no '..' segments"})
	}
	if priority < 0 || priority > 10000 {
		errs = append(errs, FieldError{Field: "priority", Message: "must be between 0 and 10000"})
	}
	if !uuidPattern.MatchString(in.ServiceID) {
		errs = append(errs, FieldError{Field: "service_id", Message: "must be a valid UUID"})
	}
	if !uuidPattern.MatchString(in.UpstreamPoolID) {
		errs = append(errs, FieldError{Field: "upstream_pool_id", Message: "must be a valid UUID"})
	}

	envID := strings.TrimSpace(in.EnvID)
	if envID == "" {
		if err := h.Store.Pool().QueryRow(r.Context(), `
			SELECT env_id FROM services WHERE org_id = $1 AND id = $2 AND deleted_at IS NULL
		`, orgCtx.OrgID, in.ServiceID).Scan(&envID); err != nil {
			errs = append(errs, FieldError{Field: "service_id", Message: "active service not found in this organization"})
		}
	} else if !uuidPattern.MatchString(envID) {
		errs = append(errs, FieldError{Field: "env_id", Message: "must be a valid UUID"})
	}
	if len(errs) > 0 {
		WriteProblem(w, r, http.StatusUnprocessableEntity, "validation_error", "Validation Failed", "Invalid route fields.", errs...)
		return
	}

	// Pre-check overlapping method conflict at identical (host, path_prefix, priority).
	if enabled {
		var conflictingID string
		err := h.Store.Pool().QueryRow(r.Context(), `
			SELECT id FROM routes
			WHERE org_id = $1 AND env_id = $2 AND enabled = TRUE
			  AND host = $3 AND path_prefix = $4 AND priority = $5
			  AND methods && $6::TEXT[]
			LIMIT 1
		`, orgCtx.OrgID, envID, host, pathPrefix, priority, methods).Scan(&conflictingID)
		if err == nil && conflictingID != "" {
			WriteProblem(w, r, http.StatusConflict, "route_conflict", "Route Conflict", "An enabled route with identical host, path_prefix, priority, and overlapping method already exists.")
			return
		}
	}

	created, err := h.Store.CreateRoute(r.Context(), store.Route{
		OrgID:          orgCtx.OrgID,
		EnvID:          envID,
		ServiceID:      in.ServiceID,
		UpstreamPoolID: in.UpstreamPoolID,
		Name:           name,
		Host:           host,
		PathPrefix:     pathPrefix,
		Methods:        methods,
		StripPrefix:    in.StripPrefix,
		Priority:       priority,
		Enabled:        enabled,
	})
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			WriteProblem(w, r, http.StatusConflict, "route_conflict", "Conflict", "A route with this name or matcher already exists.")
			return
		}
		WriteProblem(w, r, http.StatusUnprocessableEntity, "invalid_reference", "Invalid Reference", "Service or upstream pool does not belong to this organization.")
		return
	}
	_ = bumpDraftRevision(r.Context(), h.Store, orgCtx.OrgID, principal.UserID)

	w.Header().Set("ETag", formatRevisionETag(created.Revision))
	WriteJSON(w, http.StatusCreated, created)
}

func (h *RouteHandler) handleGetRoute(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	routeID := chi.URLParam(r, "routeId")
	if !uuidPattern.MatchString(routeID) {
		WriteProblem(w, r, http.StatusBadRequest, "invalid_route_id", "Invalid Route ID", "Route ID must be a valid UUID.")
		return
	}
	var rt store.Route
	err := h.Store.Pool().QueryRow(r.Context(), `
		SELECT id, org_id, env_id, service_id, upstream_pool_id,
		       name, host, path_prefix, methods, strip_prefix, priority, enabled,
		       revision, created_at, updated_at
		FROM routes
		WHERE org_id = $1 AND id = $2
	`, orgCtx.OrgID, routeID).Scan(
		&rt.ID, &rt.OrgID, &rt.EnvID, &rt.ServiceID, &rt.UpstreamPoolID,
		&rt.Name, &rt.Host, &rt.PathPrefix, &rt.Methods, &rt.StripPrefix, &rt.Priority, &rt.Enabled,
		&rt.Revision, &rt.CreatedAt, &rt.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		WriteProblem(w, r, http.StatusNotFound, "not_found", "Not Found", "Route not found.")
		return
	}
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not load route.")
		return
	}
	w.Header().Set("ETag", formatRevisionETag(rt.Revision))
	WriteJSON(w, http.StatusOK, rt)
}

type updateRouteRequest struct {
	Name           *string  `json:"name"`
	Host           *string  `json:"host"`
	PathPrefix     *string  `json:"path_prefix"`
	Methods        []string `json:"methods"`
	StripPrefix    *bool    `json:"strip_prefix"`
	Priority       *int     `json:"priority"`
	Enabled        *bool    `json:"enabled"`
	UpstreamPoolID *string  `json:"upstream_pool_id"`
	Revision       int64    `json:"revision"`
}

func (h *RouteHandler) handleUpdateRoute(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	principal, _ := PrincipalFromContext(r.Context())
	routeID := chi.URLParam(r, "routeId")
	if !uuidPattern.MatchString(routeID) {
		WriteProblem(w, r, http.StatusBadRequest, "invalid_route_id", "Invalid Route ID", "Route ID must be a valid UUID.")
		return
	}

	var in updateRouteRequest
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

	var cur store.Route
	err := h.Store.Pool().QueryRow(r.Context(), `
		SELECT id, org_id, env_id, service_id, upstream_pool_id,
		       name, host, path_prefix, methods, strip_prefix, priority, enabled,
		       revision, created_at, updated_at
		FROM routes WHERE org_id = $1 AND id = $2
	`, orgCtx.OrgID, routeID).Scan(
		&cur.ID, &cur.OrgID, &cur.EnvID, &cur.ServiceID, &cur.UpstreamPoolID,
		&cur.Name, &cur.Host, &cur.PathPrefix, &cur.Methods, &cur.StripPrefix, &cur.Priority, &cur.Enabled,
		&cur.Revision, &cur.CreatedAt, &cur.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		WriteProblem(w, r, http.StatusNotFound, "not_found", "Not Found", "Route not found.")
		return
	}
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not load route.")
		return
	}

	if in.Name != nil {
		cur.Name = strings.TrimSpace(*in.Name)
	}
	if in.Host != nil {
		cur.Host = strings.ToLower(strings.TrimSpace(*in.Host))
	}
	if in.PathPrefix != nil {
		cur.PathPrefix = strings.TrimSpace(*in.PathPrefix)
	}
	if len(in.Methods) > 0 {
		m, errs := normalizeMethods(in.Methods)
		if len(errs) > 0 {
			WriteProblem(w, r, http.StatusUnprocessableEntity, "validation_error", "Validation Failed", "Invalid HTTP methods.", errs...)
			return
		}
		cur.Methods = m
	}
	if in.StripPrefix != nil {
		cur.StripPrefix = *in.StripPrefix
	}
	if in.Priority != nil {
		cur.Priority = *in.Priority
	}
	if in.Enabled != nil {
		cur.Enabled = *in.Enabled
	}
	if in.UpstreamPoolID != nil {
		cur.UpstreamPoolID = strings.TrimSpace(*in.UpstreamPoolID)
	}

	var updated store.Route
	err = h.Store.Pool().QueryRow(r.Context(), `
		UPDATE routes
		SET name = $3, host = $4, path_prefix = $5, methods = $6, strip_prefix = $7,
		    priority = $8, enabled = $9, upstream_pool_id = $10,
		    revision = revision + 1, updated_at = NOW()
		WHERE org_id = $1 AND id = $2 AND revision = $11
		RETURNING id, org_id, env_id, service_id, upstream_pool_id,
		          name, host, path_prefix, methods, strip_prefix, priority, enabled,
		          revision, created_at, updated_at
	`, orgCtx.OrgID, routeID, cur.Name, cur.Host, cur.PathPrefix, cur.Methods,
		cur.StripPrefix, cur.Priority, cur.Enabled, cur.UpstreamPoolID, expectedRev).Scan(
		&updated.ID, &updated.OrgID, &updated.EnvID, &updated.ServiceID, &updated.UpstreamPoolID,
		&updated.Name, &updated.Host, &updated.PathPrefix, &updated.Methods, &updated.StripPrefix, &updated.Priority, &updated.Enabled,
		&updated.Revision, &updated.CreatedAt, &updated.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		WriteProblem(w, r, http.StatusConflict, "revision_conflict", "Conflict", "Route was modified concurrently.")
		return
	}
	if err != nil {
		WriteProblem(w, r, http.StatusConflict, "route_conflict", "Conflict", "Route update conflicts with existing route or invalid reference.")
		return
	}
	_ = bumpDraftRevision(r.Context(), h.Store, orgCtx.OrgID, principal.UserID)

	w.Header().Set("ETag", formatRevisionETag(updated.Revision))
	WriteJSON(w, http.StatusOK, updated)
}

func (h *RouteHandler) handleDeleteRoute(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	principal, _ := PrincipalFromContext(r.Context())
	routeID := chi.URLParam(r, "routeId")
	if !uuidPattern.MatchString(routeID) {
		WriteProblem(w, r, http.StatusBadRequest, "invalid_route_id", "Invalid Route ID", "Route ID must be a valid UUID.")
		return
	}
	tag, err := h.Store.Pool().Exec(r.Context(), `DELETE FROM routes WHERE org_id = $1 AND id = $2`, orgCtx.OrgID, routeID)
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not delete route.")
		return
	}
	if tag.RowsAffected() == 0 {
		WriteProblem(w, r, http.StatusNotFound, "not_found", "Not Found", "Route not found.")
		return
	}
	_ = bumpDraftRevision(r.Context(), h.Store, orgCtx.OrgID, principal.UserID)
	w.WriteHeader(http.StatusNoContent)
}

func normalizeMethods(raw []string) ([]string, []FieldError) {
	if len(raw) == 0 {
		return nil, []FieldError{{Field: "methods", Message: "at least one HTTP method is required"}}
	}
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, m := range raw {
		upper := strings.ToUpper(strings.TrimSpace(m))
		if _, ok := allowedRouteMethods[upper]; !ok {
			return nil, []FieldError{{Field: "methods", Message: "unsupported HTTP method " + upper}}
		}
		if _, dup := seen[upper]; !dup {
			seen[upper] = struct{}{}
			out = append(out, upper)
		}
	}
	sort.Strings(out)
	return out, nil
}
