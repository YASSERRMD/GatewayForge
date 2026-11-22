package gwauth

import (
	"errors"
	"testing"
)

func TestAuthorizeScopesCrossRoutePrivilege(t *testing.T) {
	ordersReader := ConsumerIdentity{
		Method:     "api_key",
		ConsumerID: "consumer-orders",
		Scopes:     []string{"orders:read"},
	}

	if err := AuthorizeScopes(ordersReader, []string{"orders:read"}, nil); err != nil {
		t.Fatalf("expected orders:read allowed, got %v", err)
	}
	if err := AuthorizeScopes(ordersReader, []string{"payments:write"}, nil); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden for cross-route payments:write, got %v", err)
	}
	if err := AuthorizeScopes(ordersReader, []string{"orders:read", "orders:write"}, nil); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden when missing one required scope, got %v", err)
	}
	if err := AuthorizeScopes(ordersReader, []string{"orders:read"}, []string{"consumer-payments"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden for disallowed consumer ID, got %v", err)
	}

	nsAdmin := ConsumerIdentity{ConsumerID: "consumer-ns", Scopes: []string{"orders:*"}}
	if err := AuthorizeScopes(nsAdmin, []string{"orders:write"}, nil); err != nil {
		t.Fatalf("expected namespace wildcard orders:* to authorize orders:write, got %v", err)
	}
	if err := AuthorizeScopes(nsAdmin, []string{"payments:read"}, nil); !errors.Is(err, ErrForbidden) {
		t.Fatalf("orders:* must NEVER authorize payments:read, got %v", err)
	}
}
