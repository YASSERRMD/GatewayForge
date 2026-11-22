package openapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// MaxSpecBytes caps uploaded OpenAPI specifications to 2 MiB to protect memory and CPU.
const MaxSpecBytes = 2 << 20

const maxASTDepth = 64

// Document is the normalized subset of an OpenAPI 3.0/3.1 document used by GatewayForge.
type Document struct {
	OpenAPI string              `json:"openapi" yaml:"openapi"`
	Info    Info                `json:"info" yaml:"info"`
	Servers []Server            `json:"servers,omitempty" yaml:"servers,omitempty"`
	Paths   map[string]PathItem `json:"paths" yaml:"paths"`
}

// Info holds API title, version, and description.
type Info struct {
	Title       string `json:"title" yaml:"title"`
	Version     string `json:"version" yaml:"version"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// Server describes a declared server URL.
type Server struct {
	URL         string `json:"url" yaml:"url"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// PathItem maps HTTP methods and optional unsupported constructs for a path template.
type PathItem struct {
	Get     *Operation `json:"get,omitempty" yaml:"get,omitempty"`
	Post    *Operation `json:"post,omitempty" yaml:"post,omitempty"`
	Put     *Operation `json:"put,omitempty" yaml:"put,omitempty"`
	Patch   *Operation `json:"patch,omitempty" yaml:"patch,omitempty"`
	Delete  *Operation `json:"delete,omitempty" yaml:"delete,omitempty"`
	Head    *Operation `json:"head,omitempty" yaml:"head,omitempty"`
	Options *Operation `json:"options,omitempty" yaml:"options,omitempty"`
	Trace   *Operation `json:"trace,omitempty" yaml:"trace,omitempty"`
}

// Operation describes one HTTP operation on a path.
type Operation struct {
	OperationID string         `json:"operationId,omitempty" yaml:"operationId,omitempty"`
	Summary     string         `json:"summary,omitempty" yaml:"summary,omitempty"`
	Deprecated  bool           `json:"deprecated,omitempty" yaml:"deprecated,omitempty"`
	Callbacks   map[string]any `json:"callbacks,omitempty" yaml:"callbacks,omitempty"`
}

// ParseError captures an actionable OpenAPI upload or validation failure.
type ParseError struct {
	Code    string
	Message string
}

func (e *ParseError) Error() string {
	return e.Message
}

// Parse validates size limits, decodes JSON or YAML, rejects external $ref pointers,
// and returns a validated OpenAPI 3.0/3.1 Document.
func Parse(raw []byte) (*Document, error) {
	if len(raw) > MaxSpecBytes {
		return nil, &ParseError{Code: "spec_too_large", Message: fmt.Sprintf("OpenAPI specification exceeds maximum size of %d bytes (2 MiB).", MaxSpecBytes)}
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, &ParseError{Code: "empty_spec", Message: "OpenAPI specification is empty."}
	}

	var generic any
	if trimmed[0] == '{' {
		if err := json.Unmarshal(trimmed, &generic); err != nil {
			return nil, &ParseError{Code: "invalid_json", Message: fmt.Sprintf("Invalid OpenAPI JSON: %v", err)}
		}
	} else {
		if err := yaml.Unmarshal(trimmed, &generic); err != nil {
			return nil, &ParseError{Code: "invalid_yaml", Message: fmt.Sprintf("Invalid OpenAPI YAML: %v", err)}
		}
	}

	if err := inspectRefsAndDepth(generic, 0); err != nil {
		var pe *ParseError
		if errors.As(err, &pe) {
			return nil, pe
		}
		return nil, &ParseError{Code: "invalid_spec", Message: err.Error()}
	}

	var doc Document
	if trimmed[0] == '{' {
		if err := json.Unmarshal(trimmed, &doc); err != nil {
			return nil, &ParseError{Code: "invalid_openapi_schema", Message: fmt.Sprintf("Could not decode OpenAPI JSON structure: %v", err)}
		}
	} else {
		if err := yaml.Unmarshal(trimmed, &doc); err != nil {
			return nil, &ParseError{Code: "invalid_openapi_schema", Message: fmt.Sprintf("Could not decode OpenAPI YAML structure: %v", err)}
		}
	}

	ver := strings.TrimSpace(doc.OpenAPI)
	if !strings.HasPrefix(ver, "3.0.") && !strings.HasPrefix(ver, "3.1.") {
		return nil, &ParseError{
			Code:    "unsupported_version",
			Message: fmt.Sprintf("Unsupported OpenAPI version %q; only OpenAPI 3.0.x and 3.1.x are supported.", ver),
		}
	}
	if strings.TrimSpace(doc.Info.Title) == "" {
		return nil, &ParseError{Code: "missing_info_title", Message: "OpenAPI info.title is required."}
	}
	if len(doc.Paths) == 0 {
		return nil, &ParseError{Code: "empty_paths", Message: "OpenAPI document must define at least one path."}
	}

	return &doc, nil
}

// inspectRefsAndDepth recursively checks that no external $ref exists and nesting stays bounded.
func inspectRefsAndDepth(node any, depth int) error {
	if depth > maxASTDepth {
		return &ParseError{Code: "max_depth_exceeded", Message: "OpenAPI document exceeds maximum allowed nesting depth."}
	}
	switch v := node.(type) {
	case map[string]any:
		for k, val := range v {
			if k == "$ref" {
				refStr, ok := val.(string)
				if !ok || !strings.HasPrefix(strings.TrimSpace(refStr), "#/") {
					return &ParseError{
						Code:    "external_ref_forbidden",
						Message: fmt.Sprintf("External or non-local $ref %q is forbidden for security; only local '#/...' references are allowed.", fmt.Sprint(val)),
					}
				}
			}
			if err := inspectRefsAndDepth(val, depth+1); err != nil {
				return err
			}
		}
	case map[any]any:
		for k, val := range v {
			ks := fmt.Sprint(k)
			if ks == "$ref" {
				refStr, ok := val.(string)
				if !ok || !strings.HasPrefix(strings.TrimSpace(refStr), "#/") {
					return &ParseError{
						Code:    "external_ref_forbidden",
						Message: fmt.Sprintf("External or non-local $ref %q is forbidden for security; only local '#/...' references are allowed.", fmt.Sprint(val)),
					}
				}
			}
			if err := inspectRefsAndDepth(val, depth+1); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range v {
			if err := inspectRefsAndDepth(item, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}
