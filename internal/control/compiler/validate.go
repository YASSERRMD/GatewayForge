package compiler

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/YASSERRMD/GatewayForge/internal/shared/netsec"
)

var validPolicyTypes = map[string]struct{}{
	"api_key": {}, "jwt": {}, "rate_limit": {}, "quota": {},
	"retry": {}, "timeout": {}, "circuit_breaker": {}, "cors": {}, "header_transform": {},
}

// ValidationIssue describes a single error or warning discovered in a draft configuration.
type ValidationIssue struct {
	Code     string `json:"code"`
	Severity string `json:"severity"` // error | warning
	Resource string `json:"resource"`
	Message  string `json:"message"`
}

// ValidationReport summarizes draft readiness for publication.
type ValidationReport struct {
	Valid         bool              `json:"valid"`
	DraftRevision int64             `json:"draft_revision"`
	Errors        []ValidationIssue `json:"errors"`
	Warnings      []ValidationIssue `json:"warnings"`
}

// ValidateDraft performs exhaustive semantic, referential, SSRF, and conflict checks on DraftInput.
func ValidateDraft(in DraftInput, pol netsec.TargetPolicy) ValidationReport {
	rep := ValidationReport{
		Valid:         true,
		DraftRevision: in.DraftRevision,
		Errors:        make([]ValidationIssue, 0),
		Warnings:      make([]ValidationIssue, 0),
	}
	addErr := func(code, resource, msg string) {
		rep.Valid = false
		rep.Errors = append(rep.Errors, ValidationIssue{Code: code, Severity: "error", Resource: resource, Message: msg})
	}
	addWarn := func(code, resource, msg string) {
		rep.Warnings = append(rep.Warnings, ValidationIssue{Code: code, Severity: "warning", Resource: resource, Message: msg})
	}

	// Track pools and enabled target counts.
	poolByID := make(map[string]int, len(in.Pools)) // value = count of enabled valid targets
	for _, p := range in.Pools {
		resName := "upstream_pool:" + p.Name
		if p.ConnectTimeoutMS < 100 || p.ConnectTimeoutMS > 60000 {
			addErr("invalid_timeout", resName, "connect_timeout_ms must be between 100 and 60000")
		}
		if p.ReadTimeoutMS < 100 || p.ReadTimeoutMS > 120000 {
			addErr("invalid_timeout", resName, "read_timeout_ms must be between 100 and 120000")
		}
		enabledCount := 0
		for _, t := range in.TargetsByPool[p.ID] {
			if _, err := netsec.ValidateTargetURL(t.URL, pol); err != nil {
				addErr("disallowed_target_url", resName, fmt.Sprintf("target %q: %v", t.URL, err))
			}
			if t.Weight < 1 || t.Weight > 1000 {
				addErr("invalid_weight", resName, fmt.Sprintf("target %q weight %d must be 1..1000", t.URL, t.Weight))
			}
			if t.Enabled {
				enabledCount++
			}
		}
		poolByID[p.ID] = enabledCount
		if enabledCount == 0 {
			addWarn("pool_no_enabled_targets", resName, "upstream pool has zero enabled targets")
		}
	}

	// Validate routes, dangling references, and ambiguous conflicts.
	type routeKey struct {
		host     string
		prefix   string
		priority int
	}
	routeMethodOwner := make(map[routeKey]map[string]string)

	for _, r := range in.Routes {
		resName := "route:" + r.Name
		if strings.TrimSpace(r.Host) == "" {
			addErr("invalid_route_host", resName, "host must not be empty")
		}
		if !strings.HasPrefix(r.PathPrefix, "/") || strings.Contains(r.PathPrefix, "..") {
			addErr("invalid_route_path", resName, "path_prefix must start with '/' and not contain '..'")
		}
		if len(r.Methods) == 0 {
			addErr("invalid_route_methods", resName, "route must declare at least one HTTP method")
		}
		if _, ok := in.Services[r.ServiceID]; !ok {
			addErr("dangling_service_ref", resName, fmt.Sprintf("references missing or deleted service %q", r.ServiceID))
		}
		enabledTargets, poolExists := poolByID[r.UpstreamPoolID]
		if !poolExists {
			addErr("dangling_pool_ref", resName, fmt.Sprintf("references missing upstream pool %q", r.UpstreamPoolID))
		} else if r.Enabled && enabledTargets == 0 {
			addErr("route_pool_empty", resName, "enabled route points to an upstream pool with zero enabled targets")
		}

		if r.Enabled {
			rk := routeKey{
				host:     strings.ToLower(strings.TrimSpace(r.Host)),
				prefix:   strings.TrimRight(strings.TrimSpace(r.PathPrefix), "/"),
				priority: r.Priority,
			}
			if rk.prefix == "" {
				rk.prefix = "/"
			}
			if routeMethodOwner[rk] == nil {
				routeMethodOwner[rk] = make(map[string]string)
			}
			for _, m := range r.Methods {
				up := strings.ToUpper(strings.TrimSpace(m))
				if prev, dup := routeMethodOwner[rk][up]; dup {
					addErr("route_conflict", resName, fmt.Sprintf("conflicts with route %q on (%s %s %s priority=%d)", prev, up, rk.host, rk.prefix, rk.priority))
				} else {
					routeMethodOwner[rk][up] = r.Name
				}
			}
		}
	}

	// Validate policies and attachments.
	routeExists := make(map[string]bool, len(in.Routes))
	for _, r := range in.Routes {
		routeExists[r.ID] = true
	}

	for _, p := range in.Policies {
		resName := "policy:" + p.Name
		if _, ok := validPolicyTypes[p.PolicyType]; !ok {
			addErr("unknown_policy_type", resName, fmt.Sprintf("unsupported policy_type %q", p.PolicyType))
		}
		if p.SchemaVersion != 1 {
			addErr("unsupported_policy_version", resName, fmt.Sprintf("unsupported schema_version %d", p.SchemaVersion))
		}
		var obj map[string]any
		if err := json.Unmarshal(p.Config, &obj); err != nil || obj == nil {
			addErr("invalid_policy_config", resName, "policy config must be a valid JSON object")
		}
	}

	for _, att := range in.Attachments {
		if _, ok := in.Policies[att.PolicyID]; !ok {
			addErr("dangling_policy_attachment", "policy_attachment", fmt.Sprintf("attachment references missing policy %q", att.PolicyID))
			continue
		}
		switch att.ScopeType {
		case "org":
		case "service":
			if _, ok := in.Services[att.ServiceID]; !ok {
				addErr("dangling_policy_attachment", "policy_attachment", fmt.Sprintf("service attachment references missing service %q", att.ServiceID))
			}
		case "route":
			if !routeExists[att.RouteID] {
				addErr("dangling_policy_attachment", "policy_attachment", fmt.Sprintf("route attachment references missing route %q", att.RouteID))
			}
		default:
			addErr("invalid_attachment_scope", "policy_attachment", fmt.Sprintf("unknown scope_type %q", att.ScopeType))
		}
	}

	sort.Slice(rep.Errors, func(i, j int) bool {
		if rep.Errors[i].Code != rep.Errors[j].Code {
			return rep.Errors[i].Code < rep.Errors[j].Code
		}
		return rep.Errors[i].Resource < rep.Errors[j].Resource
	})
	return rep
}
