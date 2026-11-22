package rbac

import (
	"fmt"
	"sort"
)

// Role represents a tenant-scoped organization membership role.
type Role string

const (
	RoleOwner     Role = "owner"
	RoleAdmin     Role = "admin"
	RoleDeveloper Role = "developer"
	RoleViewer    Role = "viewer"
)

// Permission identifies a discrete control-plane capability.
type Permission string

const (
	PermOrgRead          Permission = "org:read"
	PermOrgUpdate        Permission = "org:update"
	PermOrgDelete        Permission = "org:delete"
	PermMemberRead       Permission = "member:read"
	PermMemberWrite      Permission = "member:write"
	PermMemberOwnerGrant Permission = "member:owner_grant"
	PermConfigRead       Permission = "config:read"
	PermConfigWrite      Permission = "config:write"
	PermConsumerRead     Permission = "consumer:read"
	PermConsumerWrite    Permission = "consumer:write"
	PermKeyManage        Permission = "key:manage"
	PermReleaseRead      Permission = "release:read"
	PermReleasePublish   Permission = "release:publish"
	PermReleaseActivate  Permission = "release:activate"
	PermReleaseRollback  Permission = "release:rollback"
	PermAuditRead        Permission = "audit:read"
	PermMetricsRead      Permission = "metrics:read"
	PermConsoleUse       Permission = "console:use"
)

var roleMatrix = map[Role]map[Permission]struct{}{
	RoleViewer: setOf(
		PermOrgRead,
		PermMemberRead,
		PermConfigRead,
		PermConsumerRead,
		PermReleaseRead,
		PermAuditRead,
		PermMetricsRead,
	),
	RoleDeveloper: setOf(
		PermOrgRead,
		PermMemberRead,
		PermConfigRead,
		PermConfigWrite,
		PermConsumerRead,
		PermReleaseRead,
		PermAuditRead,
		PermMetricsRead,
		PermConsoleUse,
	),
	RoleAdmin: setOf(
		PermOrgRead,
		PermOrgUpdate,
		PermMemberRead,
		PermMemberWrite,
		PermConfigRead,
		PermConfigWrite,
		PermConsumerRead,
		PermConsumerWrite,
		PermKeyManage,
		PermReleaseRead,
		PermReleasePublish,
		PermReleaseActivate,
		PermReleaseRollback,
		PermAuditRead,
		PermMetricsRead,
		PermConsoleUse,
	),
	RoleOwner: setOf(
		PermOrgRead,
		PermOrgUpdate,
		PermOrgDelete,
		PermMemberRead,
		PermMemberWrite,
		PermMemberOwnerGrant,
		PermConfigRead,
		PermConfigWrite,
		PermConsumerRead,
		PermConsumerWrite,
		PermKeyManage,
		PermReleaseRead,
		PermReleasePublish,
		PermReleaseActivate,
		PermReleaseRollback,
		PermAuditRead,
		PermMetricsRead,
		PermConsoleUse,
	),
}

func setOf(perms ...Permission) map[Permission]struct{} {
	m := make(map[Permission]struct{}, len(perms))
	for _, p := range perms {
		m[p] = struct{}{}
	}
	return m
}

// ParseRole validates a string into a canonical Role.
func ParseRole(raw string) (Role, error) {
	r := Role(raw)
	if _, ok := roleMatrix[r]; !ok {
		return "", fmt.Errorf("invalid role %q", raw)
	}
	return r, nil
}

// Allows returns true iff the role explicitly grants perm. Defaults to false (fail-deny).
func Allows(role Role, perm Permission) bool {
	perms, ok := roleMatrix[role]
	if !ok {
		return false
	}
	_, allowed := perms[perm]
	return allowed
}

// PermissionsFor returns a sorted slice of permissions granted to role.
func PermissionsFor(role Role) []Permission {
	perms, ok := roleMatrix[role]
	if !ok {
		return nil
	}
	out := make([]Permission, 0, len(perms))
	for p := range perms {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
