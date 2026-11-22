package openapi

import (
	"reflect"
	"testing"
)

func TestMapToPreviewDeterministicAndAnnotatesUnsupportedConstructs(t *testing.T) {
	spec := `
openapi: "3.0.3"
info:
  title: Payments & Billing API
  version: "1.2.0"
  description: Handles invoices and payments
servers:
  - url: https://payments.acme.local/v2
  - url: https://backup.acme.local/v2
paths:
  /invoices:
    get:
      operationId: listInvoices
    post:
      operationId: createInvoice
      callbacks:
        onPaid: {}
  /invoices/{invoiceId}:
    get:
      operationId: getInvoice
    delete:
      operationId: deleteInvoice
    trace:
      operationId: traceInvoice
  /health:
    get:
      operationId: healthCheck
`
	doc, err := Parse([]byte(spec))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	plan1 := MapToPreview(doc, "")
	plan2 := MapToPreview(doc, "")
	if !reflect.DeepEqual(plan1, plan2) {
		t.Fatal("MapToPreview must be completely deterministic across invocations")
	}

	if plan1.Service.Slug != "payments-billing-api" {
		t.Fatalf("expected slug payments-billing-api, got %q", plan1.Service.Slug)
	}
	if plan1.DefaultHost != "payments.acme.local" || plan1.BasePath != "/v2" {
		t.Fatalf("unexpected host/basePath: %q / %q", plan1.DefaultHost, plan1.BasePath)
	}
	if len(plan1.Routes) != 2 {
		t.Fatalf("expected 2 merged prefix routes (/v2/health and /v2/invoices), got %d: %+v", len(plan1.Routes), plan1.Routes)
	}
	if plan1.Routes[0].PathPrefix != "/v2/health" || plan1.Routes[1].PathPrefix != "/v2/invoices" {
		t.Fatalf("unexpected sorted route prefixes: %+v", plan1.Routes)
	}
	wantMethods := []string{"DELETE", "GET", "POST"}
	if !reflect.DeepEqual(plan1.Routes[1].Methods, wantMethods) {
		t.Fatalf("expected merged methods %v, got %v", wantMethods, plan1.Routes[1].Methods)
	}

	codes := map[string]bool{}
	for _, w := range plan1.Warnings {
		codes[w.Code] = true
	}
	for _, expected := range []string{"multiple_servers", "unsupported_method_trace", "callbacks_unsupported", "path_template_normalized"} {
		if !codes[expected] {
			t.Fatalf("expected warning code %q in %+v", expected, plan1.Warnings)
		}
	}
}
