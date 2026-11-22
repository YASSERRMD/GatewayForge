package matcher

import (
	"net"
	"sort"
	"strings"

	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

// CompiledRoute is a normalized, immutable route entry ready for fast deterministic matching.
type CompiledRoute struct {
	Route      snapshot.Route
	Host       string
	PathPrefix string
	Methods    map[string]struct{}
}

// Matcher evaluates incoming (host, method, path) tuples strictly against explicit configured routes.
type Matcher struct {
	routes []CompiledRoute
}

// MatchResult describes the result of evaluating a request against the Matcher.
type MatchResult struct {
	Matched        bool
	MethodMismatch bool
	AllowedMethods []string
	Route          snapshot.Route
}

// Compile normalizes and sorts enabled routes in deterministic precedence order:
// 1. Priority DESC
// 2. PathPrefix length DESC (more specific prefix wins)
// 3. Route ID ASC (stable deterministic tie-breaker)
func Compile(routes []snapshot.Route) *Matcher {
	compiled := make([]CompiledRoute, 0, len(routes))
	for _, r := range routes {
		if !r.Enabled {
			continue
		}
		host := NormalizeHost(r.Host)
		prefix := NormalizePathPrefix(r.PathPrefix)
		if host == "" || prefix == "" || len(r.Methods) == 0 {
			continue
		}
		mset := make(map[string]struct{}, len(r.Methods))
		for _, m := range r.Methods {
			upper := strings.ToUpper(strings.TrimSpace(m))
			if upper != "" {
				mset[upper] = struct{}{}
			}
		}
		if len(mset) == 0 {
			continue
		}
		compiled = append(compiled, CompiledRoute{
			Route:      r,
			Host:       host,
			PathPrefix: prefix,
			Methods:    mset,
		})
	}

	sort.SliceStable(compiled, func(i, j int) bool {
		if compiled[i].Route.Priority != compiled[j].Route.Priority {
			return compiled[i].Route.Priority > compiled[j].Route.Priority
		}
		if len(compiled[i].PathPrefix) != len(compiled[j].PathPrefix) {
			return len(compiled[i].PathPrefix) > len(compiled[j].PathPrefix)
		}
		return compiled[i].Route.ID < compiled[j].Route.ID
	})

	return &Matcher{routes: compiled}
}

// Match finds the highest-precedence explicit route matching host, method, and path.
// If host and path match one or more routes but method does not, MethodMismatch is true.
func (m *Matcher) Match(rawHost, method, path string) MatchResult {
	if m == nil || len(m.routes) == 0 {
		return MatchResult{}
	}
	host := NormalizeHost(rawHost)
	if host == "" || !strings.HasPrefix(path, "/") {
		return MatchResult{}
	}
	upperMethod := strings.ToUpper(strings.TrimSpace(method))

	allowedSet := make(map[string]struct{})
	for _, cr := range m.routes {
		if cr.Host != host {
			continue
		}
		if !PathMatchesPrefix(path, cr.PathPrefix) {
			continue
		}
		if _, ok := cr.Methods[upperMethod]; ok {
			return MatchResult{
				Matched: true,
				Route:   cr.Route,
			}
		}
		for allowed := range cr.Methods {
			allowedSet[allowed] = struct{}{}
		}
	}

	if len(allowedSet) > 0 {
		allowed := make([]string, 0, len(allowedSet))
		for k := range allowedSet {
			allowed = append(allowed, k)
		}
		sort.Strings(allowed)
		return MatchResult{
			MethodMismatch: true,
			AllowedMethods: allowed,
		}
	}
	return MatchResult{}
}

// RouteCount returns the number of compiled enabled routes.
func (m *Matcher) RouteCount() int {
	if m == nil {
		return 0
	}
	return len(m.routes)
}

// NormalizeHost lowercases and strips an optional port or trailing dot from a Host header.
func NormalizeHost(raw string) string {
	h := strings.TrimSpace(raw)
	if h == "" {
		return ""
	}
	if strings.Contains(h, ":") {
		if hostOnly, _, err := net.SplitHostPort(h); err == nil {
			h = hostOnly
		}
	}
	h = strings.TrimSuffix(strings.ToLower(h), ".")
	if strings.ContainsAny(h, " \t\r\n/") {
		return ""
	}
	return h
}

// NormalizePathPrefix ensures a clean leading slash without trailing slash (except "/").
func NormalizePathPrefix(prefix string) string {
	p := strings.TrimSpace(prefix)
	if p == "" || !strings.HasPrefix(p, "/") {
		return ""
	}
	if len(p) > 1 {
		p = strings.TrimRight(p, "/")
		if p == "" {
			return "/"
		}
	}
	return p
}

// PathMatchesPrefix enforces segment-boundary matching:
// prefix "/api" matches "/api" and "/api/users", but NEVER "/apiv2".
func PathMatchesPrefix(path, prefix string) bool {
	if prefix == "/" {
		return strings.HasPrefix(path, "/")
	}
	if path == prefix {
		return true
	}
	return strings.HasPrefix(path, prefix+"/")
}
