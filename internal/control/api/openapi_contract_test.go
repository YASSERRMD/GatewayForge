package api

import (
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"
)

type openAPIDoc struct {
	OpenAPI    string                    `yaml:"openapi"`
	Info       map[string]any            `yaml:"info"`
	Paths      map[string]map[string]any `yaml:"paths"`
	Components map[string]any            `yaml:"components"`
}

func TestOpenAPIContractMatchesRouter(t *testing.T) {
	specPath := filepath.Join("..", "..", "..", "api", "openapi.yaml")
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read openapi.yaml: %v", err)
	}

	var doc openAPIDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse openapi.yaml: %v", err)
	}
	if !strings.HasPrefix(doc.OpenAPI, "3.1") {
		t.Fatalf("expected OpenAPI 3.1.x, got %q", doc.OpenAPI)
	}
	if len(doc.Paths) < 10 {
		t.Fatalf("expected >= 10 documented paths, got %d", len(doc.Paths))
	}

	// Build full mounted router and collect every registered (METHOD, path) pair.
	authH := &AuthHandler{}
	orgH := &OrgHandler{}
	memH := &MembershipHandler{}
	auditH := &AuditHandler{}

	r := NewRouter(RouterOptions{
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		MountV1: func(v1 chi.Router) {
			authH.MountAuthRoutes(v1)
			orgH.MountOrgRoutes(v1)
			memH.MountMembershipRoutes(v1)
			auditH.MountAuditRoutes(v1)
		},
	})

	mounted := make(map[string]bool)
	err = chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		normalized := strings.TrimSuffix(route, "/")
		if normalized == "" {
			normalized = "/"
		}
		mounted[strings.ToUpper(method)+" "+normalized] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walk router: %v", err)
	}

	for path, ops := range doc.Paths {
		normalized := strings.TrimSuffix(path, "/")
		for method := range ops {
			upper := strings.ToUpper(method)
			switch upper {
			case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
				key := upper + " " + normalized
				if !mounted[key] {
					t.Errorf("OpenAPI documents %q, but router does not mount it", key)
				}
			}
		}
	}
}
