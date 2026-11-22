package ratelimit

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/shared/policy"
	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

// ErrLimiterUnavailable indicates a fail-closed rate limit/quota check failed because its backing store was unavailable.
var ErrLimiterUnavailable = errors.New("rate limiter backend unavailable (fail_closed)")

// EnforceResult aggregates the outcome of evaluating all attached rate-limit and quota policies on a route.
type EnforceResult struct {
	Allowed         bool
	EmitHeaders     bool
	Decision        Decision
	DeniedBy        string // "rate_limit" | "quota" | "backend_unavailable"
	DegradedOpen    bool
	BackendError    error
	MatchedPolicies int
}

// Enforcer evaluates route-attached rate_limit and quota policies with explicit Redis outage fail modes.
type Enforcer struct {
	Primary      Limiter      // Redis distributed limiter (preferred)
	Fallback     Limiter      // optional local limiter when Primary is nil (single-node/tests)
	PrimaryQuota QuotaCounter // Redis distributed quota counter
	LocalQuota   QuotaCounter // optional local quota counter when PrimaryQuota is nil
	OnEvent      func(routeID, outcome, backend string)
}

// NewEnforcer constructs an Enforcer. When primary is nil, local fallback is used directly (single-node mode).
func NewEnforcer(primary Limiter, fallback Limiter, primaryQuota QuotaCounter, localQuota QuotaCounter) *Enforcer {
	return &Enforcer{
		Primary:      primary,
		Fallback:     fallback,
		PrimaryQuota: primaryQuota,
		LocalQuota:   localQuota,
	}
}

// Enforce evaluates all rate_limit and quota policies attached to route for request r and authenticated consumerID.
func (e *Enforcer) Enforce(ctx context.Context, r *http.Request, orgID string, route snapshot.Route, consumerID string) EnforceResult {
	if e == nil {
		return EnforceResult{Allowed: true}
	}

	var bestDecision Decision
	hasDecision := false
	emitHeaders := false
	matched := 0
	degradedOpen := false

	for _, ap := range route.Policies {
		switch ap.PolicyType {
		case "rate_limit":
			matched++
			pol, err := policy.ParseRateLimitPolicy(ap.Config)
			if err != nil {
				// Invalid persisted policy fails closed for safety.
				e.emit(route.ID, "error", "invalid_policy")
				return EnforceResult{
					Allowed:         false,
					DeniedBy:        "backend_unavailable",
					BackendError:    err,
					MatchedPolicies: matched,
				}
			}
			keyIn := KeyInput{
				OrgID:        orgID,
				PolicyID:     ap.ID,
				ConfigDigest: pol.ConfigDigest(),
				ScopeType:    ap.ScopeType,
				ServiceID:    route.ServiceID,
				RouteID:      route.ID,
				ConsumerID:   consumerID,
				Request:      r,
			}
			key := DeriveRateLimitKey(keyIn, pol)
			d, err := e.evalRateLimit(ctx, key, pol)
			if err != nil {
				e.emit(route.ID, "error", "redis")
				if pol.SecurityClass == policy.ClassCritical || pol.FailMode == policy.FailClosed {
					e.emit(route.ID, "denied_fail_closed", "fail_closed")
					return EnforceResult{
						Allowed:         false,
						EmitHeaders:     pol.EmitHeaders(),
						Decision:        Decision{Allowed: false, Limit: pol.Burst, Remaining: 0, RetryAfter: time.Second, Window: time.Duration(pol.WindowSeconds) * time.Second, Backend: "fail_closed"},
						DeniedBy:        "backend_unavailable",
						BackendError:    ErrLimiterUnavailable,
						MatchedPolicies: matched,
					}
				}
				// Explicit fail_open on standard route.
				degradedOpen = true
				e.emit(route.ID, "allowed_fail_open", "fail_open")
				continue
			}

			if pol.EmitHeaders() {
				emitHeaders = true
			}
			if !d.Allowed {
				e.emit(route.ID, "denied", d.Backend)
				return EnforceResult{
					Allowed:         false,
					EmitHeaders:     pol.EmitHeaders(),
					Decision:        d,
					DeniedBy:        "rate_limit",
					MatchedPolicies: matched,
				}
			}
			e.emit(route.ID, "allowed", d.Backend)
			if !hasDecision || d.Remaining < bestDecision.Remaining {
				bestDecision = d
				hasDecision = true
			}

		case "quota":
			matched++
			qp, err := policy.ParseQuotaPolicy(ap.Config)
			if err != nil {
				e.emit(route.ID, "error", "invalid_policy")
				return EnforceResult{
					Allowed:         false,
					DeniedBy:        "backend_unavailable",
					BackendError:    err,
					MatchedPolicies: matched,
				}
			}
			keyIn := KeyInput{
				OrgID:        orgID,
				PolicyID:     ap.ID,
				ConfigDigest: qp.ConfigDigest(),
				ScopeType:    ap.ScopeType,
				ServiceID:    route.ServiceID,
				RouteID:      route.ID,
				ConsumerID:   consumerID,
				Request:      r,
			}
			d, err := e.evalQuota(ctx, keyIn, qp)
			if err != nil {
				e.emit(route.ID, "error", "redis")
				if qp.SecurityClass == policy.ClassCritical || qp.FailMode == policy.FailClosed {
					e.emit(route.ID, "denied_fail_closed", "fail_closed")
					return EnforceResult{
						Allowed:         false,
						EmitHeaders:     true,
						Decision:        Decision{Allowed: false, Limit: qp.Limit, Remaining: 0, RetryAfter: time.Second, Backend: "fail_closed"},
						DeniedBy:        "backend_unavailable",
						BackendError:    ErrLimiterUnavailable,
						MatchedPolicies: matched,
					}
				}
				degradedOpen = true
				e.emit(route.ID, "allowed_fail_open", "fail_open")
				continue
			}

			emitHeaders = true
			if !d.Allowed {
				e.emit(route.ID, "denied", d.Backend)
				return EnforceResult{
					Allowed:         false,
					EmitHeaders:     true,
					Decision:        d,
					DeniedBy:        "quota",
					MatchedPolicies: matched,
				}
			}
			e.emit(route.ID, "allowed", d.Backend)
			if !hasDecision || d.Remaining < bestDecision.Remaining {
				bestDecision = d
				hasDecision = true
			}
		}
	}

	return EnforceResult{
		Allowed:         true,
		EmitHeaders:     emitHeaders && hasDecision,
		Decision:        bestDecision,
		DegradedOpen:    degradedOpen,
		MatchedPolicies: matched,
	}
}

func (e *Enforcer) evalRateLimit(ctx context.Context, key string, pol policy.RateLimitPolicy) (Decision, error) {
	if e.Primary != nil {
		return e.Primary.Allow(ctx, key, pol)
	}
	if e.Fallback != nil {
		return e.Fallback.Allow(ctx, key, pol)
	}
	return Decision{}, errors.New("no rate limiter configured")
}

func (e *Enforcer) evalQuota(ctx context.Context, keyIn KeyInput, q policy.QuotaPolicy) (Decision, error) {
	if e.PrimaryQuota != nil {
		return e.PrimaryQuota.Consume(ctx, keyIn, q)
	}
	if e.LocalQuota != nil {
		return e.LocalQuota.Consume(ctx, keyIn, q)
	}
	return Decision{}, errors.New("no quota counter configured")
}

func (e *Enforcer) emit(routeID, outcome, backend string) {
	if e.OnEvent != nil {
		e.OnEvent(routeID, outcome, backend)
	}
}
