package resilience

import (
	"context"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/shared/policy"
	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

type connectTimeoutKey struct{}

// ResolveTimeoutPolicy returns the highest-precedence valid timeout policy attached to a route.
func ResolveTimeoutPolicy(policies []snapshot.AttachedPolicy) (policy.TimeoutPolicy, bool) {
	for _, ap := range policies {
		if ap.PolicyType != "timeout" {
			continue
		}
		p, err := policy.ParseTimeoutPolicy(ap.Config)
		if err != nil {
			continue
		}
		return p, true
	}
	return policy.TimeoutPolicy{}, false
}

// WithConnectTimeout stores an explicit per-route upstream TCP connect timeout on ctx.
func WithConnectTimeout(ctx context.Context, d time.Duration) context.Context {
	if d <= 0 {
		return ctx
	}
	return context.WithValue(ctx, connectTimeoutKey{}, d)
}

// ConnectTimeoutFromContext retrieves an explicit per-route connect timeout if present.
func ConnectTimeoutFromContext(ctx context.Context) time.Duration {
	if ctx == nil {
		return 0
	}
	if d, ok := ctx.Value(connectTimeoutKey{}).(time.Duration); ok && d > 0 {
		return d
	}
	return 0
}
