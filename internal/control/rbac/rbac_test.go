package rbac

import "testing"

func TestRolePermissionMatrix(t *testing.T) {
	cases := []struct {
		role Role
		perm Permission
		want bool
	}{
		// Viewer: read-only
		{RoleViewer, PermConfigRead, true},
		{RoleViewer, PermAuditRead, true},
		{RoleViewer, PermConfigWrite, false},
		{RoleViewer, PermReleasePublish, false},
		{RoleViewer, PermKeyManage, false},
		{RoleViewer, PermConsoleUse, false},

		// Developer: draft editing and test console, NOT publish/rollback/key issuance
		{RoleDeveloper, PermConfigWrite, true},
		{RoleDeveloper, PermConsoleUse, true},
		{RoleDeveloper, PermReleaseRead, true},
		{RoleDeveloper, PermReleasePublish, false},
		{RoleDeveloper, PermReleaseActivate, false},
		{RoleDeveloper, PermReleaseRollback, false},
		{RoleDeveloper, PermKeyManage, false},
		{RoleDeveloper, PermMemberWrite, false},

		// Admin: full operations, publish, rollback, key management; cannot grant owner or delete org
		{RoleAdmin, PermReleasePublish, true},
		{RoleAdmin, PermReleaseActivate, true},
		{RoleAdmin, PermReleaseRollback, true},
		{RoleAdmin, PermKeyManage, true},
		{RoleAdmin, PermMemberWrite, true},
		{RoleAdmin, PermMemberOwnerGrant, false},
		{RoleAdmin, PermOrgDelete, false},

		// Owner: everything
		{RoleOwner, PermMemberOwnerGrant, true},
		{RoleOwner, PermOrgDelete, true},
		{RoleOwner, PermReleaseRollback, true},

		// Fail-deny on unknown role or unknown permission
		{Role("superadmin"), PermConfigRead, false},
		{Role(""), PermOrgRead, false},
		{RoleOwner, Permission("unknown:perm"), false},
	}

	for _, tc := range cases {
		if got := Allows(tc.role, tc.perm); got != tc.want {
			t.Errorf("Allows(%q, %q) = %v, want %v", tc.role, tc.perm, got, tc.want)
		}
	}
}

func TestParseRole(t *testing.T) {
	for _, valid := range []string{"owner", "admin", "developer", "viewer"} {
		r, err := ParseRole(valid)
		if err != nil {
			t.Fatalf("ParseRole(%q) failed: %v", valid, err)
		}
		if len(PermissionsFor(r)) == 0 {
			t.Fatalf("expected non-empty permissions for %q", r)
		}
	}
	if _, err := ParseRole("root"); err == nil {
		t.Fatal("expected error for unknown role 'root'")
	}
}
