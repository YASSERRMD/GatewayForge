package cors

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

// PolicyConfig defines the typed configuration for a gateway CORS policy.
type PolicyConfig struct {
	AllowedOrigins   []string `json:"allowed_origins"`
	AllowedMethods   []string `json:"allowed_methods"`
	AllowedHeaders   []string `json:"allowed_headers"`
	ExposedHeaders   []string `json:"exposed_headers,omitempty"`
	AllowCredentials bool     `json:"allow_credentials"`
	MaxAgeSeconds    int      `json:"max_age_seconds,omitempty"`
}

// ErrWildcardWithCredentials is returned when a CORS policy attempts to combine "*" with allow_credentials=true.
var ErrWildcardWithCredentials = errors.New("CORS policy cannot allow wildcard '*' when allow_credentials is true")

// ValidateConfig checks a CORS policy for security misconfigurations such as wildcard+credentials.
func ValidateConfig(cfg PolicyConfig) error {
	if cfg.MaxAgeSeconds < 0 || cfg.MaxAgeSeconds > 86400 {
		return errors.New("max_age_seconds must be between 0 and 86400")
	}
	for _, o := range cfg.AllowedOrigins {
		trimmed := strings.TrimSpace(o)
		if trimmed == "" || strings.ContainsAny(trimmed, " \t\r\n") {
			return errors.New("allowed_origins contains empty or whitespace-containing entry")
		}
		if trimmed == "*" && cfg.AllowCredentials {
			return ErrWildcardWithCredentials
		}
	}
	if cfg.AllowCredentials {
		for _, h := range cfg.AllowedHeaders {
			if strings.TrimSpace(h) == "*" {
				return errors.New("CORS policy cannot allow wildcard '*' header when allow_credentials is true")
			}
		}
		for _, m := range cfg.AllowedMethods {
			if strings.TrimSpace(m) == "*" {
				return errors.New("CORS policy cannot allow wildcard '*' method when allow_credentials is true")
			}
		}
	}
	return nil
}

// ResolveEffective returns the highest-precedence CORS policy attached to a route.
// Policies are expected in precedence order: route > service > org, priority DESC.
func ResolveEffective(policies []snapshot.AttachedPolicy) (PolicyConfig, bool) {
	for _, p := range policies {
		if p.PolicyType != "cors" {
			continue
		}
		var cfg PolicyConfig
		if err := json.Unmarshal(p.Config, &cfg); err != nil {
			continue
		}
		return cfg, true
	}
	return PolicyConfig{}, false
}

// IsPreflight reports whether r is a browser CORS preflight request.
func IsPreflight(r *http.Request) bool {
	return r.Method == http.MethodOptions &&
		strings.TrimSpace(r.Header.Get("Origin")) != "" &&
		strings.TrimSpace(r.Header.Get("Access-Control-Request-Method")) != ""
}

// HandlePreflight evaluates a CORS preflight request and writes the HTTP response.
// Invalid policies (e.g., wildcard with credentials) or disallowed origins/methods/headers fail closed with 403.
func HandlePreflight(w http.ResponseWriter, r *http.Request, cfg PolicyConfig) {
	addVary(w.Header(), "Origin", "Access-Control-Request-Method", "Access-Control-Request-Headers")

	if err := ValidateConfig(cfg); err != nil {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	origin := strings.TrimSpace(r.Header.Get("Origin"))
	allowOrigin, ok := matchOrigin(origin, cfg)
	if !ok {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	reqMethod := strings.ToUpper(strings.TrimSpace(r.Header.Get("Access-Control-Request-Method")))
	if !matchMethod(reqMethod, cfg.AllowedMethods) {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	reqHeaders := parseHeaderList(r.Header.Get("Access-Control-Request-Headers"))
	if !matchHeaders(reqHeaders, cfg.AllowedHeaders) {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	w.Header().Set("Access-Control-Allow-Origin", allowOrigin)
	if cfg.AllowCredentials {
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	}
	if len(cfg.AllowedMethods) > 0 {
		upper := make([]string, 0, len(cfg.AllowedMethods))
		for _, m := range cfg.AllowedMethods {
			if t := strings.ToUpper(strings.TrimSpace(m)); t != "" {
				upper = append(upper, t)
			}
		}
		w.Header().Set("Access-Control-Allow-Methods", strings.Join(upper, ", "))
	} else {
		w.Header().Set("Access-Control-Allow-Methods", reqMethod)
	}
	if len(reqHeaders) > 0 {
		isWildcardOnly := len(cfg.AllowedHeaders) == 1 && strings.TrimSpace(cfg.AllowedHeaders[0]) == "*"
		if len(cfg.AllowedHeaders) > 0 && !isWildcardOnly {
			w.Header().Set("Access-Control-Allow-Headers", strings.Join(cfg.AllowedHeaders, ", "))
		} else {
			w.Header().Set("Access-Control-Allow-Headers", strings.Join(reqHeaders, ", "))
		}
	}
	if cfg.MaxAgeSeconds > 0 {
		w.Header().Set("Access-Control-Max-Age", strconv.Itoa(cfg.MaxAgeSeconds))
	}
	w.WriteHeader(http.StatusNoContent)
}

// ApplyResponseHeaders applies CORS headers to a non-preflight response if Origin is present and permitted.
func ApplyResponseHeaders(w http.ResponseWriter, r *http.Request, cfg PolicyConfig) {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return
	}
	addVary(w.Header(), "Origin")
	if err := ValidateConfig(cfg); err != nil {
		return
	}
	allowOrigin, ok := matchOrigin(origin, cfg)
	if !ok {
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", allowOrigin)
	if cfg.AllowCredentials {
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	}
	if len(cfg.ExposedHeaders) > 0 {
		w.Header().Set("Access-Control-Expose-Headers", strings.Join(cfg.ExposedHeaders, ", "))
	}
}

func matchOrigin(origin string, cfg PolicyConfig) (string, bool) {
	if origin == "" || strings.ContainsAny(origin, "\r\n") {
		return "", false
	}
	for _, allowed := range cfg.AllowedOrigins {
		a := strings.TrimSpace(allowed)
		if a == "*" {
			if cfg.AllowCredentials {
				// Defense in depth: never emit wildcard with credentials.
				return "", false
			}
			return "*", true
		}
		if strings.EqualFold(a, origin) {
			return origin, true
		}
	}
	return "", false
}

func matchMethod(reqMethod string, allowed []string) bool {
	if reqMethod == "" {
		return false
	}
	for _, m := range allowed {
		u := strings.ToUpper(strings.TrimSpace(m))
		if u == "*" || u == reqMethod {
			return true
		}
	}
	return false
}

func matchHeaders(reqHeaders, allowed []string) bool {
	if len(reqHeaders) == 0 {
		return true
	}
	allowSet := make(map[string]struct{}, len(allowed))
	wildcard := false
	for _, h := range allowed {
		lh := strings.ToLower(strings.TrimSpace(h))
		if lh == "*" {
			wildcard = true
			continue
		}
		if lh != "" {
			allowSet[lh] = struct{}{}
		}
	}
	if wildcard {
		return true
	}
	for _, rh := range reqHeaders {
		if _, ok := allowSet[strings.ToLower(rh)]; !ok {
			return false
		}
	}
	return true
}

func parseHeaderList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func addVary(h http.Header, values ...string) {
	existing := make(map[string]struct{})
	for _, cur := range h.Values("Vary") {
		for _, part := range strings.Split(cur, ",") {
			if t := strings.TrimSpace(part); t != "" {
				existing[strings.ToLower(t)] = struct{}{}
			}
		}
	}
	for _, v := range values {
		if _, ok := existing[strings.ToLower(v)]; !ok {
			h.Add("Vary", v)
			existing[strings.ToLower(v)] = struct{}{}
		}
	}
}
