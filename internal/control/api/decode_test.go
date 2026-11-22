package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeStrictJSON(t *testing.T) {
	type sample struct {
		Name string `json:"name"`
	}

	t.Run("accepts valid single JSON object", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewBufferString(`{"name":"orders"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		var out sample
		if !DecodeStrictJSON(rec, req, 1024, &out) {
			t.Fatalf("expected true, got false: %s", rec.Body.String())
		}
		if out.Name != "orders" {
			t.Fatalf("unexpected decoded value: %+v", out)
		}
	})

	t.Run("rejects unknown fields with 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewBufferString(`{"name":"orders","role":"owner"}`))
		rec := httptest.NewRecorder()
		var out sample
		if DecodeStrictJSON(rec, req, 1024, &out) {
			t.Fatal("expected unknown field rejection")
		}
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("rejects oversized body with 413", func(t *testing.T) {
		huge := `{"name":"` + strings.Repeat("a", 2048) + `"}`
		req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewBufferString(huge))
		rec := httptest.NewRecorder()
		var out sample
		if DecodeStrictJSON(rec, req, 256, &out) {
			t.Fatal("expected oversized payload rejection")
		}
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("expected 413, got %d", rec.Code)
		}
	})

	t.Run("rejects trailing JSON garbage with 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewBufferString(`{"name":"ok"}{"extra":true}`))
		rec := httptest.NewRecorder()
		var out sample
		if DecodeStrictJSON(rec, req, 1024, &out) {
			t.Fatal("expected trailing JSON rejection")
		}
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})
}
