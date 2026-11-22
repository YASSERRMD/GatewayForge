package openapi

import (
	"bytes"
	"testing"
)

func TestExportInventoryDeterministicAndReparseable(t *testing.T) {
	services := []ExportServiceInput{
		{ID: "svc-1", Slug: "orders", Name: "Orders API"},
	}
	routes := []ExportRouteInput{
		{
			ID:          "r-2",
			ServiceID:   "svc-1",
			Name:        "Orders Read",
			Host:        "api.acme.local",
			PathPrefix:  "/v1/orders",
			Methods:     []string{"POST", "GET"},
			StripPrefix: false,
			Priority:    120,
			Enabled:     true,
		},
		{
			ID:         "r-disabled",
			ServiceID:  "svc-1",
			Name:       "Disabled Route",
			Host:       "internal.secret.local",
			PathPrefix: "/v1/internal",
			Methods:    []string{"GET"},
			Priority:   100,
			Enabled:    false,
		},
	}

	doc1 := ExportInventory("Acme Corp", services, routes)
	doc2 := ExportInventory("Acme Corp", services, routes)

	b1, err := MarshalExportJSON(doc1)
	if err != nil {
		t.Fatalf("marshal 1: %v", err)
	}
	b2, err := MarshalExportJSON(doc2)
	if err != nil {
		t.Fatalf("marshal 2: %v", err)
	}
	if !bytes.Equal(b1, b2) {
		t.Fatal("exported OpenAPI JSON must be deterministically identical")
	}

	if len(doc1.Limitations) < 3 {
		t.Fatalf("expected documented export limitations, got %v", doc1.Limitations)
	}
	if bytes.Contains(b1, []byte("internal.secret.local")) {
		t.Fatal("disabled routes must not be exported")
	}

	// Verify exported JSON cleanly passes our own OpenAPI 3.0 parser
	reparsed, err := Parse(b1)
	if err != nil {
		t.Fatalf("exported OpenAPI failed round-trip Parse: %v", err)
	}
	if reparsed.OpenAPI != "3.0.3" || len(reparsed.Paths) != 1 {
		t.Fatalf("unexpected reparsed doc: %+v", reparsed)
	}
}
