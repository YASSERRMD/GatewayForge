package openapi

import (
	"errors"
	"strings"
	"testing"
)

func TestParseOpenAPI30YAMLAnd31JSON(t *testing.T) {
	yamlSpec := `
openapi: "3.0.3"
info:
  title: Orders API
  version: "1.0.0"
servers:
  - url: https://api.acme.test/v1
paths:
  /orders:
    get:
      operationId: listOrders
      responses:
        "200":
          $ref: "#/components/responses/Ok"
`
	doc, err := Parse([]byte(yamlSpec))
	if err != nil {
		t.Fatalf("expected valid YAML spec to parse, got %v", err)
	}
	if doc.Info.Title != "Orders API" || len(doc.Paths) != 1 {
		t.Fatalf("unexpected parsed doc: %+v", doc)
	}

	jsonSpec := `{
		"openapi": "3.1.0",
		"info": {"title": "Catalog API", "version": "2.0.0"},
		"paths": {"/items": {"get": {"operationId": "listItems"}}}
	}`
	doc2, err := Parse([]byte(jsonSpec))
	if err != nil {
		t.Fatalf("expected valid 3.1 JSON spec to parse, got %v", err)
	}
	if doc2.Info.Title != "Catalog API" {
		t.Fatalf("unexpected title: %q", doc2.Info.Title)
	}
}

func TestParseRejectsExternalRefsMalformedAndOversized(t *testing.T) {
	externalCases := []string{
		`{"openapi":"3.0.3","info":{"title":"X","version":"1"},"paths":{"/a":{"get":{"responses":{"200":{"$ref":"http://169.254.169.254/latest/meta-data"}}}}}}`,
		`{"openapi":"3.0.3","info":{"title":"X","version":"1"},"paths":{"/a":{"get":{"responses":{"200":{"$ref":"file:///etc/passwd"}}}}}}`,
		"openapi: '3.0.3'\ninfo:\n  title: X\npaths:\n  /a:\n    $ref: './other.yaml#/paths/a'\n",
	}
	for _, raw := range externalCases {
		_, err := Parse([]byte(raw))
		var pe *ParseError
		if !errors.As(err, &pe) || pe.Code != "external_ref_forbidden" {
			t.Fatalf("expected external_ref_forbidden for %s, got %v", raw, err)
		}
	}

	// Swagger 2.0 unsupported
	_, err := Parse([]byte(`{"swagger":"2.0","info":{"title":"Legacy"},"paths":{"/a":{}}}`))
	var pe *ParseError
	if !errors.As(err, &pe) || pe.Code != "unsupported_version" {
		t.Fatalf("expected unsupported_version, got %v", err)
	}

	// Malformed JSON
	_, err = Parse([]byte(`{"openapi": "3.0.3",`))
	if !errors.As(err, &pe) || pe.Code != "invalid_json" {
		t.Fatalf("expected invalid_json, got %v", err)
	}

	// Oversized spec (> 2 MiB)
	big := make([]byte, MaxSpecBytes+10)
	copy(big, []byte(strings.Repeat(" ", len(big))))
	_, err = Parse(big)
	if !errors.As(err, &pe) || pe.Code != "spec_too_large" {
		t.Fatalf("expected spec_too_large, got %v", err)
	}
}
