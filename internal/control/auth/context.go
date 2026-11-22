package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/YASSERRMD/GatewayForge/internal/control/rbac"
)

// ErrForbiddenOrg is returned when an authenticated user lacks explicit membership in an organization.
var ErrForbiddenOrg = errors.New("not a member of the requested organization")

// OrgMembership represents a user's resolved membership and effective permissions in one organization.
type OrgMembership struct {
	OrgID          string            `json:"org_id"`
	OrgSlug        string            `json:"org_slug"`
	OrgDisplayName string            `json:"org_display_name"`
	OrgEnabled     bool              `json:"org_enabled"`
	Role           rbac.Role         `json:"role"`
	Permissions    []rbac.Permission `json:"permissions"`
}

// ListUserMemberships returns all explicit organization memberships for userID ordered by org slug.
func (m *SessionManager) ListUserMemberships(ctx context.Context, userID string) ([]OrgMembership, error) {
	rows, err := m.pool.Query(ctx, `
		SELECT o.id, o.slug, o.display_name, o.enabled, mem.role
		FROM memberships mem
		JOIN organizations o ON o.id = mem.org_id
		WHERE mem.user_id = $1
		ORDER BY o.slug ASC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list user memberships: %w", err)
	}
	defer rows.Close()

	out := make([]OrgMembership, 0)
	for rows.Next() {
		var item OrgMembership
		var roleRaw string
		if err := rows.Scan(&item.OrgID, &item.OrgSlug, &item.OrgDisplayName, &item.OrgEnabled, &roleRaw); err != nil {
			return nil, fmt.Errorf("scan user membership: %w", err)
		}
		role, err := rbac.ParseRole(roleRaw)
		if err != nil {
			continue
		}
		item.Role = role
		item.Permissions = rbac.PermissionsFor(role)
		out = append(out, item)
	}
	return out, rows.Err()
}

// ResolveOrgMembership resolves explicit membership for userID in orgID without any implicit fallback.
func (m *SessionManager) ResolveOrgMembership(ctx context.Context, userID, orgID string) (OrgMembership, error) {
	var item OrgMembership
	var roleRaw string
	err := m.pool.QueryRow(ctx, `
		SELECT o.id, o.slug, o.display_name, o.enabled, mem.role
		FROM memberships mem
		JOIN organizations o ON o.id = mem.org_id
		WHERE mem.user_id = $1 AND mem.org_id = $2
	`, userID, orgID).Scan(&item.OrgID, &item.OrgSlug, &item.OrgDisplayName, &item.OrgEnabled, &roleRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		return OrgMembership{}, ErrForbiddenOrg
	}
	if err != nil {
		return OrgMembership{}, fmt.Errorf("resolve org membership: %w", err)
	}
	role, err := rbac.ParseRole(roleRaw)
	if err != nil {
		return OrgMembership{}, ErrForbiddenOrg
	}
	item.Role = role
	item.Permissions = rbac.PermissionsFor(role)
	return item, nil
}
