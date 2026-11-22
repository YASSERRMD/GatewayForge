package metrics

import (
	"errors"
	"fmt"
	"strings"
)

// ProhibitedLabelNames enumerates high-cardinality or secret label keys forbidden from metrics.
var ProhibitedLabelNames = map[string]struct{}{
	"api_key":       {},
	"authorization": {},
	"token":         {},
	"jwt":           {},
	"consumer_key":  {},
	"client_ip":     {},
	"ip":            {},
	"path":          {},
	"raw_path":      {},
	"query":         {},
	"request_id":    {},
	"trace_id":      {},
	"user_id":       {},
	"email":         {},
}

// AllowedLabelNames enumerates the exact bounded label keys permitted on GatewayForge Prometheus metrics.
var AllowedLabelNames = map[string]struct{}{
	"route_id":     {},
	"service_id":   {},
	"method":       {},
	"status_class": {},
	"status":       {},
	"outcome":      {},
	"backend":      {},
	"le":           {},
}

// ValidateLabel enforces the label allowlist and rejects secret-like or unbounded path-parameter values.
func ValidateLabel(name, value string) error {
	ln := strings.ToLower(strings.TrimSpace(name))
	if _, forbidden := ProhibitedLabelNames[ln]; forbidden {
		return fmt.Errorf("metric label %q is prohibited by cardinality/security policy", name)
	}
	if _, ok := AllowedLabelNames[ln]; !ok {
		return fmt.Errorf("metric label %q is not in the bounded allowlist", name)
	}
	if strings.HasPrefix(value, "gwf_") || strings.HasPrefix(value, "eyJ") {
		return errors.New("metric label value appears to contain a raw credential")
	}
	if strings.ContainsAny(value, "/?=& ") {
		return errors.New("metric label value must not contain raw URL paths or query parameters")
	}
	return nil
}
