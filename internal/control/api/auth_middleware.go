package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"

	"github.com/YASSERRMD/GatewayForge/internal/control/auth"
)

const (
	principalCtxKey ctxKey = "auth_principal"
	orgCtxKey       ctxKey = "org_membership"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// PrincipalFromContext returns the authenticated session principal if present.
func PrincipalFromContext(ctx context.Context) (auth.SessionPrincipal, bool) {
	p, ok := ctx.Value(principalCtxKey).(auth.SessionPrincipal)
	return p, ok
}

// OrgContextFromContext returns the verified organization membership for the current request.
func OrgContextFromContext(ctx context.Context) (auth.OrgMembership, bool) {
	m, ok := ctx.Value(orgCtxKey).(auth.OrgMembership)
	return m, ok
}

// RequireAuth authenticates the session cookie and enforces CSRF on state-changing requests.
func RequireAuth(sessions *auth.SessionManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(auth.SessionCookieName)
			if err != nil || cookie.Value == "" {
				WriteProblem(w, r, http.StatusUnauthorized, "unauthenticated", "Unauthorized", "Authentication required.")
				return
			}
			principal, err := sessions.Authenticate(r.Context(), cookie.Value)
			if err != nil {
				WriteProblem(w, r, http.StatusUnauthorized, "unauthenticated", "Unauthorized", "Session is invalid or expired.")
				return
			}
			if err := auth.VerifyCSRF(r, principal); err != nil {
				WriteProblem(w, r, http.StatusForbidden, "csrf_invalid", "Forbidden", "Missing or invalid X-CSRF-Token header.")
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalCtxKey, principal)))
		})
	}
}

// RequireOrgContext resolves explicit membership for the {orgId} route parameter.
// Rejects implicit fallback or missing membership with 403 Forbidden.
func RequireOrgContext(sessions *auth.SessionManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := PrincipalFromContext(r.Context())
			if !ok {
				WriteProblem(w, r, http.StatusUnauthorized, "unauthenticated", "Unauthorized", "Authentication required.")
				return
			}
			orgID := chi.URLParam(r, "orgId")
			if !uuidPattern.MatchString(orgID) {
				WriteProblem(w, r, http.StatusBadRequest, "invalid_org_id", "Invalid Organization ID", "Organization ID must be a valid UUID.")
				return
			}
			membership, err := sessions.ResolveOrgMembership(r.Context(), principal.UserID, orgID)
			if err != nil {
				if errors.Is(err, auth.ErrForbiddenOrg) {
					WriteProblem(w, r, http.StatusForbidden, "org_forbidden", "Forbidden", "You do not have access to this organization.")
					return
				}
				WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Authorization Error", "Failed to verify organization membership.")
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), orgCtxKey, membership)))
		})
	}
}
