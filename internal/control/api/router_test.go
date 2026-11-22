package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestControlPlaneRouterContracts(t *testing.T) {
	silent := slog.New(slog.NewJSONHandler(io.Discard, nil))
	r := NewRouter(RouterOptions{
		Logger:             silent,
		AllowedCORSOrigins: []string{"http://localhost:3000"},
		MountV1: func(v1 chi.Router) {
			v1.Post("/validate-sample", func(w http.ResponseWriter, req *http.Request) {
				WriteProblem(w, req, http.StatusUnprocessableEntity, "validation_error", "Validation Failed", "One or more fields are invalid.", FieldError{
					Field:   "slug",
					Message: "must match lowercase alphanumeric pattern",
				})
			})
		},
	})

	t.Run("unknown route returns RFC 9457 404 with request_id", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/does-not-exist", nil)
		req.Header.Set("X-Request-Id", "req-trace-1234")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json; charset=utf-8" {
			t.Fatalf("expected application/problem+json, got %q", ct)
		}
		var p Problem
		if err := json.NewDecoder(rec.Body).Decode(&p); err != nil {
			t.Fatalf("decode problem: %v", err)
		}
		if p.Code != "not_found" || p.RequestID != "req-trace-1234" || p.Status != 404 {
			t.Fatalf("unexpected problem payload: %+v", p)
		}
	})

	t.Run("disallowed method returns RFC 9457 405", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/meta", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405, got %d", rec.Code)
		}
		var p Problem
		if err := json.NewDecoder(rec.Body).Decode(&p); err != nil {
			t.Fatalf("decode problem: %v", err)
		}
		if p.Code != "method_not_allowed" || p.RequestID == "" {
			t.Fatalf("unexpected 405 problem: %+v", p)
		}
	})

	t.Run("validation problem includes field errors", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/validate-sample", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422, got %d", rec.Code)
		}
		var p Problem
		if err := json.NewDecoder(rec.Body).Decode(&p); err != nil {
			t.Fatalf("decode problem: %v", err)
		}
		if p.Code != "validation_error" || len(p.Errors) != 1 || p.Errors[0].Field != "slug" {
			t.Fatalf("unexpected validation problem: %+v", p)
		}
	})

	t.Run("CORS allows configured origin and ignores untrusted origin", func(t *testing.T) {
		reqOK := httptest.NewRequest(http.MethodOptions, "/api/v1/meta", nil)
		reqOK.Header.Set("Origin", "http://localhost:3000")
		recOK := httptest.NewRecorder()
		r.ServeHTTP(recOK, reqOK)
		if recOK.Code != http.StatusNoContent || recOK.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
			t.Fatalf("expected allowed CORS preflight, got code=%d header=%q", recOK.Code, recOK.Header().Get("Access-Control-Allow-Origin"))
		}

		reqBad := httptest.NewRequest(http.MethodGet, "/api/v1/meta", nil)
		reqBad.Header.Set("Origin", "https://evil.example")
		recBad := httptest.NewRecorder()
		r.ServeHTTP(recBad, reqBad)
		if recBad.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("untrusted origin must not receive ACAO header, got %q", recBad.Header().Get("Access-Control-Allow-Origin"))
		}
	})
}
