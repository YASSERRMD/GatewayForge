package api

import (
	"net/http"

	"github.com/YASSERRMD/GatewayForge/internal/control/rbac"
)

// RequirePermission enforces that the authenticated caller holds the required
// organization-scoped permission. Defaults to deny if org context is absent.
func RequirePermission(perm rbac.Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			orgCtx, ok := OrgContextFromContext(r.Context())
			if !ok {
				WriteProblem(w, r, http.StatusForbidden, "rbac_denied", "Forbidden", "Organization authorization context is missing.")
				return
			}
			if !orgCtx.OrgEnabled && r.Method != http.MethodGet && r.Method != http.MethodHead && perm != rbac.PermOrgUpdate {
				WriteProblem(w, r, http.StatusForbidden, "org_disabled", "Organization Disabled", "Mutations are blocked while this organization is disabled.")
				return
			}
			if !rbac.Allows(orgCtx.Role, perm) {
				WriteProblem(w, r, http.StatusForbidden, "insufficient_permissions", "Forbidden", "Your role does not permit this action.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
