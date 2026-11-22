package openapi

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

var nonSlugChars = regexp.MustCompile(`[^a-z0-9-]+`)

// ProposedService describes the logical service derived from OpenAPI info metadata.
type ProposedService struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ProposedRoute describes one deterministic route derived from OpenAPI paths/operations.
type ProposedRoute struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Host        string   `json:"host"`
	PathPrefix  string   `json:"path_prefix"`
	Methods     []string `json:"methods"`
	StripPrefix bool     `json:"strip_prefix"`
	Priority    int      `json:"priority"`
	SourcePath  string   `json:"source_path"`
}

// MappingWarning reports an OpenAPI construct that was normalized or skipped.
type MappingWarning struct {
	Code    string `json:"code"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

// PreviewPlan is the deterministic translation of an OpenAPI Document into draft resources.
type PreviewPlan struct {
	OpenAPIVersion string           `json:"openapi_version"`
	Service        ProposedService  `json:"service"`
	DefaultHost    string           `json:"default_host"`
	BasePath       string           `json:"base_path"`
	Routes         []ProposedRoute  `json:"routes"`
	Warnings       []MappingWarning `json:"warnings"`
}

// MapToPreview deterministically maps an OpenAPI 3.0/3.1 Document into proposed GatewayForge
// service and route definitions, explicitly annotating unsupported constructs.
func MapToPreview(doc *Document, overrideHost string) PreviewPlan {
	warnings := make([]MappingWarning, 0)

	svcName := strings.TrimSpace(doc.Info.Title)
	if len(svcName) > 120 {
		svcName = svcName[:120]
	}
	svcSlug := Slugify(svcName)

	host := strings.ToLower(strings.TrimSpace(overrideHost))
	basePath := ""
	if len(doc.Servers) > 1 {
		warnings = append(warnings, MappingWarning{
			Code:    "multiple_servers",
			Message: fmt.Sprintf("Specification declares %d servers; only the first server URL is used to derive default host/base path.", len(doc.Servers)),
		})
	}
	if len(doc.Servers) > 0 {
		rawSrv := strings.TrimSpace(doc.Servers[0].URL)
		if strings.Contains(rawSrv, "{") {
			warnings = append(warnings, MappingWarning{
				Code:    "server_variables_unsupported",
				Message: fmt.Sprintf("Server URL %q contains template variables; falling back to default host.", rawSrv),
			})
		} else if parsed, err := url.Parse(rawSrv); err == nil {
			if host == "" && parsed.Hostname() != "" {
				host = strings.ToLower(parsed.Hostname())
			}
			if parsed.Path != "" && parsed.Path != "/" {
				basePath = "/" + strings.Trim(parsed.Path, "/")
			}
		}
	}
	if host == "" {
		host = "api.gatewayforge.local"
		warnings = append(warnings, MappingWarning{
			Code:    "default_host_assumed",
			Message: "No concrete server hostname found; defaulting route host to api.gatewayforge.local.",
		})
	}

	rawPaths := make([]string, 0, len(doc.Paths))
	for p := range doc.Paths {
		rawPaths = append(rawPaths, p)
	}
	sort.Strings(rawPaths)

	type mergedRoute struct {
		name       string
		pathPrefix string
		sourcePath string
		methods    map[string]struct{}
	}
	byPrefix := make(map[string]*mergedRoute)
	prefixOrder := make([]string, 0)

	for _, rawPath := range rawPaths {
		item := doc.Paths[rawPath]
		if item.Trace != nil {
			warnings = append(warnings, MappingWarning{
				Code:    "unsupported_method_trace",
				Path:    rawPath,
				Message: "HTTP TRACE method is not supported by GatewayForge and was skipped.",
			})
		}

		ops := []struct {
			method string
			op     *Operation
		}{
			{"GET", item.Get},
			{"POST", item.Post},
			{"PUT", item.Put},
			{"PATCH", item.Patch},
			{"DELETE", item.Delete},
			{"HEAD", item.Head},
			{"OPTIONS", item.Options},
		}

		methods := make([]string, 0)
		for _, entry := range ops {
			if entry.op == nil {
				continue
			}
			methods = append(methods, entry.method)
			if len(entry.op.Callbacks) > 0 {
				warnings = append(warnings, MappingWarning{
					Code:    "callbacks_unsupported",
					Path:    rawPath,
					Message: fmt.Sprintf("%s %s declares OpenAPI callbacks, which are not translated to gateway routes.", entry.method, rawPath),
				})
			}
		}
		if len(methods) == 0 {
			continue
		}

		normalized, hadParam := normalizeOpenAPIPath(basePath, rawPath)
		if hadParam {
			warnings = append(warnings, MappingWarning{
				Code:    "path_template_normalized",
				Path:    rawPath,
				Message: fmt.Sprintf("Templated path %q was normalized to prefix %q for gateway prefix matching.", rawPath, normalized),
			})
		}

		mr, exists := byPrefix[normalized]
		if !exists {
			mr = &mergedRoute{
				name:       routeDisplayName(svcSlug, normalized),
				pathPrefix: normalized,
				sourcePath: rawPath,
				methods:    make(map[string]struct{}),
			}
			byPrefix[normalized] = mr
			prefixOrder = append(prefixOrder, normalized)
		}
		for _, m := range methods {
			mr.methods[m] = struct{}{}
		}
	}

	sort.Strings(prefixOrder)
	routes := make([]ProposedRoute, 0, len(prefixOrder))
	for _, prefix := range prefixOrder {
		mr := byPrefix[prefix]
		mlist := make([]string, 0, len(mr.methods))
		for m := range mr.methods {
			mlist = append(mlist, m)
		}
		sort.Strings(mlist)
		routes = append(routes, ProposedRoute{
			Key:         host + ":" + prefix,
			Name:        mr.name,
			Host:        host,
			PathPrefix:  prefix,
			Methods:     mlist,
			StripPrefix: false,
			Priority:    100 + len(prefix),
			SourcePath:  mr.sourcePath,
		})
	}

	return PreviewPlan{
		OpenAPIVersion: doc.OpenAPI,
		Service: ProposedService{
			Slug:        svcSlug,
			Name:        svcName,
			Description: strings.TrimSpace(doc.Info.Description),
		},
		DefaultHost: host,
		BasePath:    basePath,
		Routes:      routes,
		Warnings:    warnings,
	}
}

// Slugify produces a lowercase hyphenated slug matching GatewayForge's service slug regex.
func Slugify(raw string) string {
	lower := strings.ToLower(strings.TrimSpace(raw))
	slug := nonSlugChars.ReplaceAllString(lower, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "imported-service"
	}
	if len(slug) > 63 {
		slug = strings.TrimRight(slug[:63], "-")
	}
	return slug
}

func normalizeOpenAPIPath(basePath, rawPath string) (string, bool) {
	cleaned := "/" + strings.TrimLeft(strings.TrimSpace(rawPath), "/")
	hadParam := false
	if idx := strings.Index(cleaned, "{"); idx >= 0 {
		hadParam = true
		cleaned = strings.TrimRight(cleaned[:idx], "/")
		if cleaned == "" {
			cleaned = "/"
		}
	}
	if basePath != "" {
		if cleaned == "/" {
			return basePath, hadParam
		}
		return basePath + cleaned, hadParam
	}
	return cleaned, hadParam
}

func routeDisplayName(svcSlug, prefix string) string {
	clean := strings.Trim(prefix, "/")
	if clean == "" {
		clean = "root"
	}
	name := svcSlug + "-" + strings.ReplaceAll(clean, "/", "-")
	if len(name) > 120 {
		name = name[:120]
	}
	return name
}
