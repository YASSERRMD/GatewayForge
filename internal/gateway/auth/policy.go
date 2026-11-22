package gwauth

import (
	"encoding/json"
	"net/http"

	"github.com/YASSERRMD/GatewayForge/internal/gateway/table"
	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

// EffectiveAuthPolicy represents the resolved authentication rules for a single matched route.
type EffectiveAuthPolicy struct {
	Public           bool
	RequireAPIKey    bool
	RequireJWT       bool
	Combinator       string // "all" (default) | "any"
	APIKeyConfig     APIKeyPolicyConfig
	AllowedConsumers []string
	JWTConfig        JWTPolicyConfig
}

type rawAuthExt struct {
	Public           bool     `json:"public"`
	Combinator       string   `json:"combinator"`
	AllowedConsumers []string `json:"allowed_consumers"`
}

// ResolveEffectiveAuth computes the deterministic effective auth policy from route-attached policies.
// Policies are expected in precedence order: route > service > org, then priority DESC.
func ResolveEffectiveAuth(policies []snapshot.AttachedPolicy) EffectiveAuthPolicy {
	eff := EffectiveAuthPolicy{
		Public:     true,
		Combinator: "all",
	}
	var apiKeyScopes []string
	var jwtScopes []string
	var allowedConsumers []string

	for _, p := range policies {
		if p.PolicyType != "api_key" && p.PolicyType != "jwt" {
			continue
		}
		var ext rawAuthExt
		_ = json.Unmarshal(p.Config, &ext)

		// Route-scoped explicit public=true overrides inherited org/service auth policies.
		if p.ScopeType == "route" && ext.Public {
			return EffectiveAuthPolicy{Public: true}
		}
		if ext.Combinator == "any" {
			eff.Combinator = "any"
		}
		allowedConsumers = append(allowedConsumers, ext.AllowedConsumers...)

		switch p.PolicyType {
		case "api_key":
			var cfg APIKeyPolicyConfig
			_ = json.Unmarshal(p.Config, &cfg)
			if !eff.RequireAPIKey {
				eff.APIKeyConfig = cfg
			}
			apiKeyScopes = append(apiKeyScopes, cfg.RequiredScopes...)
			eff.RequireAPIKey = true
			eff.Public = false
		case "jwt":
			var cfg JWTPolicyConfig
			_ = json.Unmarshal(p.Config, &cfg)
			if !eff.RequireJWT {
				eff.JWTConfig = cfg
			}
			jwtScopes = append(jwtScopes, cfg.RequiredScopes...)
			eff.RequireJWT = true
			eff.Public = false
		}
	}

	eff.APIKeyConfig.RequiredScopes = dedupeStrings(apiKeyScopes)
	eff.JWTConfig.RequiredScopes = dedupeStrings(jwtScopes)
	eff.AllowedConsumers = dedupeStrings(allowedConsumers)
	return eff
}

func dedupeStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, dup := seen[s]; !dup {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

// Enforcer evaluates route auth policies using APIKeyAuthenticator and JWTVerifier.
type Enforcer struct {
	APIKeys *APIKeyAuthenticator
	JWT     *JWTVerifier
}

// Enforce authenticates and authorizes r against route policies and returns identity + credential headers to strip.
func (e *Enforcer) Enforce(r *http.Request, tbl *table.CompiledTable, route snapshot.Route) (ConsumerIdentity, []string, error) {
	eff := ResolveEffectiveAuth(route.Policies)
	if eff.Public {
		return ConsumerIdentity{Method: "public"}, nil, nil
	}

	if e == nil {
		return ConsumerIdentity{}, nil, ErrUnauthorized
	}

	var stripHeaders []string

	tryAPIKey := func() (ConsumerIdentity, error) {
		if e.APIKeys == nil {
			return ConsumerIdentity{}, ErrUnauthorized
		}
		id, err := e.APIKeys.Authenticate(r, tbl, eff.APIKeyConfig)
		if err != nil {
			return ConsumerIdentity{}, err
		}
		if err := AuthorizeScopes(id, eff.APIKeyConfig.RequiredScopes, eff.AllowedConsumers); err != nil {
			return id, err
		}
		if eff.APIKeyConfig.StripCredential == nil || *eff.APIKeyConfig.StripCredential {
			_, hdr := ExtractKey(r, eff.APIKeyConfig)
			if hdr != "" {
				stripHeaders = append(stripHeaders, hdr)
			}
		}
		return id, nil
	}

	tryJWT := func() (ConsumerIdentity, error) {
		if e.JWT == nil {
			return ConsumerIdentity{}, ErrUnauthorized
		}
		id, err := e.JWT.Authenticate(r, eff.JWTConfig)
		if err != nil {
			return ConsumerIdentity{}, err
		}
		if err := AuthorizeScopes(id, eff.JWTConfig.RequiredScopes, nil); err != nil {
			return id, err
		}
		if eff.JWTConfig.StripCredential == nil || *eff.JWTConfig.StripCredential {
			stripHeaders = append(stripHeaders, "Authorization")
		}
		return id, nil
	}

	switch {
	case eff.RequireAPIKey && !eff.RequireJWT:
		id, err := tryAPIKey()
		return id, stripHeaders, err
	case !eff.RequireAPIKey && eff.RequireJWT:
		id, err := tryJWT()
		return id, stripHeaders, err
	case eff.RequireAPIKey && eff.RequireJWT && eff.Combinator == "any":
		if id, err := tryAPIKey(); err == nil {
			return id, stripHeaders, nil
		} else if err == ErrForbidden {
			return id, nil, ErrForbidden
		}
		id, err := tryJWT()
		return id, stripHeaders, err
	default: // both required ("all")
		id1, err := tryAPIKey()
		if err != nil {
			return id1, nil, err
		}
		id2, err := tryJWT()
		if err != nil {
			return id2, nil, err
		}
		return id1, stripHeaders, nil
	}
}
