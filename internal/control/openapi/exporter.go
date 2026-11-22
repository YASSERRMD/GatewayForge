package openapi

import (
	"encoding/json"
	"sort"
	"strings"
)

// ExportServiceInput is the service metadata included in OpenAPI export.
type ExportServiceInput struct {
	ID          string
	Slug        string
	Name        string
	Description string
}

// ExportRouteInput is one route inventory item exported to OpenAPI 3.0.3.
type ExportRouteInput struct {
	ID          string
	ServiceID   string
	Name        string
	Host        string
	PathPrefix  string
	Methods     []string
	StripPrefix bool
	Priority    int
	Enabled     bool
}

// ExportedOperation describes one exported path operation with safe GatewayForge metadata.
type ExportedOperation struct {
	OperationID string         `json:"operationId"`
	Summary     string         `json:"summary"`
	Tags        []string       `json:"tags,omitempty"`
	Responses   map[string]any `json:"responses"`
	ServiceSlug string         `json:"x-gatewayforge-service,omitempty"`
	Priority    int            `json:"x-gatewayforge-priority"`
	StripPrefix bool           `json:"x-gatewayforge-strip-prefix"`
}

// ExportedDocument is a deterministic OpenAPI 3.0.3 representation of current gateway routes.
type ExportedDocument struct {
	OpenAPI     string                                  `json:"openapi"`
	Info        Info                                    `json:"info"`
	Servers     []Server                                `json:"servers"`
	Paths       map[string]map[string]ExportedOperation `json:"paths"`
	Limitations []string                                `json:"x-gatewayforge-export-limitations"`
}

// ExportInventory converts an organization's service/route inventory into a stable OpenAPI 3.0.3
// document with explicitly documented limitations and zero secret or internal upstream leakage.
func ExportInventory(orgName string, services []ExportServiceInput, routes []ExportRouteInput) ExportedDocument {
	svcByID := make(map[string]ExportServiceInput, len(services))
	for _, s := range services {
		svcByID[s.ID] = s
	}

	sortedRoutes := make([]ExportRouteInput, 0, len(routes))
	for _, rt := range routes {
		if rt.Enabled {
			sortedRoutes = append(sortedRoutes, rt)
		}
	}
	sort.Slice(sortedRoutes, func(i, j int) bool {
		if sortedRoutes[i].PathPrefix != sortedRoutes[j].PathPrefix {
			return sortedRoutes[i].PathPrefix < sortedRoutes[j].PathPrefix
		}
		if sortedRoutes[i].Priority != sortedRoutes[j].Priority {
			return sortedRoutes[i].Priority > sortedRoutes[j].Priority
		}
		return sortedRoutes[i].Name < sortedRoutes[j].Name
	})

	hostSet := make(map[string]struct{})
	paths := make(map[string]map[string]ExportedOperation)

	for _, rt := range sortedRoutes {
		host := strings.ToLower(strings.TrimSpace(rt.Host))
		if host != "" {
			hostSet[host] = struct{}{}
		}
		p := strings.TrimSpace(rt.PathPrefix)
		if p == "" {
			p = "/"
		}
		if paths[p] == nil {
			paths[p] = make(map[string]ExportedOperation)
		}

		svc := svcByID[rt.ServiceID]
		tags := []string{}
		if svc.Name != "" {
			tags = append(tags, svc.Name)
		}

		methods := append([]string(nil), rt.Methods...)
		sort.Strings(methods)
		for _, m := range methods {
			lm := strings.ToLower(strings.TrimSpace(m))
			if lm == "" {
				continue
			}
			// Keep highest-priority route operation if two enabled routes share prefix+method across hosts.
			if _, exists := paths[p][lm]; exists {
				continue
			}
			paths[p][lm] = ExportedOperation{
				OperationID: Slugify(rt.Name + "-" + lm),
				Summary:     rt.Name,
				Tags:        tags,
				Responses: map[string]any{
					"200": map[string]any{
						"description": "Proxied upstream response (request/response payload schemas are not stored in gateway route metadata).",
					},
				},
				ServiceSlug: svc.Slug,
				Priority:    rt.Priority,
				StripPrefix: rt.StripPrefix,
			}
		}
	}

	hosts := make([]string, 0, len(hostSet))
	for h := range hostSet {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)

	servers := make([]Server, 0, len(hosts))
	for _, h := range hosts {
		servers = append(servers, Server{URL: "https://" + h})
	}
	if len(servers) == 0 {
		servers = append(servers, Server{URL: "https://api.gatewayforge.local"})
	}

	title := strings.TrimSpace(orgName)
	if title == "" {
		title = "GatewayForge Inventory"
	} else {
		title += " — GatewayForge Export"
	}

	return ExportedDocument{
		OpenAPI: "3.0.3",
		Info: Info{
			Title:       title,
			Version:     "1.0.0",
			Description: "Auto-exported route inventory from GatewayForge. Upstream target URLs, policies, and credentials are intentionally excluded.",
		},
		Servers: servers,
		Paths:   paths,
		Limitations: []string{
			"Exported paths reflect GatewayForge prefix matchers rather than full OpenAPI path parameter templates.",
			"Request body schemas, query parameters, and detailed response schemas are not persisted by the gateway and are omitted.",
			"Gateway policies (auth, rate limits, retries, circuit breakers), consumer credentials, and internal upstream target URLs are intentionally excluded for security.",
		},
	}
}

// MarshalExportJSON renders deterministic indented OpenAPI 3.0.3 JSON bytes.
func MarshalExportJSON(doc ExportedDocument) ([]byte, error) {
	return json.MarshalIndent(doc, "", "  ")
}
