package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/YASSERRMD/GatewayForge/internal/control/auth"
	"github.com/YASSERRMD/GatewayForge/internal/control/rbac"
)

func withTestOrgCtx(r *http.Request, role rbac.Role, enabled bool) *http.Request {
	mem := auth.OrgMembership{
		OrgID:      "00000000-0000-0000-0000-000000000001",
		OrgSlug:    "test-org",
		OrgEnabled: enabled,
		Role:       role,
	}
	return r.WithContext(context.WithValue(r.Context(), orgCtxKey, mem))
}

func TestRequirePermissionMatrix(t *testing.T) {
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	cases := []struct {
		name       string
		method     string
		role       rbac.Role
		orgEnabled bool
		hasCtx     bool
		perm       rbac.Permission
		wantStatus int
	}{
		{"missing context fails closed", http.MethodGet, "", true, false, rbac.PermConfigRead, http.StatusForbidden},
		{"unknown role fails closed", http.MethodGet, rbac.Role("mystery"), true, true, rbac.PermConfigRead, http.StatusForbidden},
		{"viewer can read config", http.MethodGet, rbac.RoleViewer, true, true, rbac.PermConfigRead, http.StatusOK},
		{"viewer cannot write config", http.MethodPost, rbac.RoleViewer, true, true, rbac.PermConfigWrite, http.StatusForbidden},
		{"developer can write draft config", http.MethodPost, rbac.RoleDeveloper, true, true, rbac.PermConfigWrite, http.StatusOK},
		{"developer cannot publish release", http.MethodPost, rbac.RoleDeveloper, true, true, rbac.PermReleasePublish, http.StatusForbidden},
		{"developer cannot manage keys", http.MethodPost, rbac.RoleDeveloper, true, true, rbac.PermKeyManage, http.StatusForbidden},
		{"admin can publish release", http.MethodPost, rbac.RoleAdmin, true, true, rbac.PermReleasePublish, http.StatusOK},
		{"admin can manage keys", http.MethodPost, rbac.RoleAdmin, true, true, rbac.PermKeyManage, http.StatusOK},
		{"admin cannot grant owner role", http.MethodPost, rbac.RoleAdmin, true, true, rbac.PermMemberOwnerGrant, http.StatusForbidden},
		{"owner can grant owner role", http.MethodPost, rbac.RoleOwner, true, true, rbac.PermMemberOwnerGrant, http.StatusOK},
		{"disabled org blocks config write", http.MethodPost, rbac.RoleOwner, false, true, rbac.PermConfigWrite, http.StatusForbidden},
		{"disabled org still allows org update to re-enable", http.MethodPatch, rbac.RoleOwner, false, true, rbac.PermOrgUpdate, http.StatusOK},
		{"disabled org still allows reads", http.MethodGet, rbac.RoleViewer, false, true, rbac.PermConfigRead, http.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "/api/v1/orgs/00000000-0000-0000-0000-000000000001/test", nil)
			if tc.hasCtx {
				req = withTestOrgCtx(req, tc.role, tc.orgEnabled)
			}
			rec := httptest.NewRecorder()
			RequirePermission(tc.perm)(okHandler).ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("got status %d, want %d (body=%s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
		})
	}
}
