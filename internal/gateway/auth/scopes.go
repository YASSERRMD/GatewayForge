package gwauth

import "strings"

// AuthorizeScopes verifies that an authenticated identity satisfies all required route/service scopes
// and any explicit consumer allowlist configured on the attached auth policy.
func AuthorizeScopes(id ConsumerIdentity, requiredScopes []string, allowedConsumerIDs []string) error {
	if len(allowedConsumerIDs) > 0 {
		found := false
		for _, cid := range allowedConsumerIDs {
			if cid == id.ConsumerID {
				found = true
				break
			}
		}
		if !found {
			return ErrForbidden
		}
	}

	if len(requiredScopes) == 0 {
		return nil
	}
	granted := make(map[string]struct{}, len(id.Scopes))
	for _, s := range id.Scopes {
		trimmed := strings.TrimSpace(s)
		if trimmed != "" {
			granted[trimmed] = struct{}{}
		}
	}

	for _, req := range requiredScopes {
		req = strings.TrimSpace(req)
		if req == "" {
			continue
		}
		if _, ok := granted[req]; ok {
			continue
		}
		// Allow explicit namespace wildcard such as "orders:*" when granted on the credential.
		if ns, _, hasColon := strings.Cut(req, ":"); hasColon {
			if _, ok := granted[ns+":*"]; ok {
				continue
			}
		}
		return ErrForbidden
	}
	return nil
}
