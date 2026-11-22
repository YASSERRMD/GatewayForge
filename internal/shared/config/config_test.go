package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mapLookup(m map[string]string) LookupFunc {
	return func(key string) (string, bool) {
		v, ok := m[key]
		return v, ok
	}
}

func TestLoadControlPlaneValidation(t *testing.T) {
	tests := []struct {
		name           string
		env            map[string]string
		wantErr        bool
		forbiddenInErr []string
	}{
		{
			name:    "safe development defaults",
			env:     map[string]string{},
			wantErr: false,
		},
		{
			name: "valid production config",
			env: map[string]string{
				"GATEWAYFORGE_ENV":           "production",
				"DATABASE_URL":               "postgres://gw_user:supersecretpass@db.internal:5432/gatewayforge?sslmode=require",
				"REDIS_ADDR":                 "redis.internal:6379",
				"SESSION_SECRET":             "0123456789abcdef0123456789abcdef",
				"GATEWAY_SYNC_TOKEN":         "sync-token-0123456789",
				"ALLOW_MOCK_UPSTREAMS":       "false",
				"CONTROL_PLANE_CORS_ORIGINS": "https://console.example.internal",
			},
			wantErr: false,
		},
		{
			name: "rejects short secrets without echoing secret value",
			env: map[string]string{
				"SESSION_SECRET":     "my-short-secret-value",
				"GATEWAY_SYNC_TOKEN": "tiny-tok",
			},
			wantErr:        true,
			forbiddenInErr: []string{"my-short-secret-value", "tiny-tok"},
		},
		{
			name: "rejects bad postgres URL without echoing password",
			env: map[string]string{
				"DATABASE_URL": "mysql://admin:verysecretdbpass@localhost:3306/db",
			},
			wantErr:        true,
			forbiddenInErr: []string{"verysecretdbpass"},
		},
		{
			name: "rejects wildcard CORS origin",
			env: map[string]string{
				"CONTROL_PLANE_CORS_ORIGINS": "*",
			},
			wantErr: true,
		},
		{
			name: "rejects mock upstreams in production",
			env: map[string]string{
				"GATEWAYFORGE_ENV":     "production",
				"DATABASE_URL":         "postgres://u:p@db:5432/gf",
				"SESSION_SECRET":       "0123456789abcdef0123456789abcdef",
				"GATEWAY_SYNC_TOKEN":   "sync-token-0123456789",
				"ALLOW_MOCK_UPSTREAMS": "true",
			},
			wantErr: true,
		},
		{
			name: "rejects invalid environment name",
			env: map[string]string{
				"GATEWAYFORGE_ENV": "staging-unknown",
			},
			wantErr: true,
		},
		{
			name: "rejects invalid listen port",
			env: map[string]string{
				"CONTROL_PLANE_ADDR": ":99999",
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := LoadControlPlane(mapLookup(tc.env))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (cfg=%+v)", cfg)
				}
				for _, secret := range tc.forbiddenInErr {
					if strings.Contains(err.Error(), secret) {
						t.Fatalf("error message leaked secret %q: %v", secret, err)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			summary := cfg.Redacted()
			if dbSummary, ok := summary["database_url"].(string); ok && strings.Contains(dbSummary, "supersecretpass") {
				t.Fatalf("redacted summary leaked DB password: %s", dbSummary)
			}
		})
	}
}

func TestLoadGatewayValidation(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
	}{
		{
			name:    "safe development defaults",
			env:     map[string]string{},
			wantErr: false,
		},
		{
			name: "invalid control plane url",
			env: map[string]string{
				"CONTROL_PLANE_URL": "ftp://localhost:8080",
			},
			wantErr: true,
		},
		{
			name: "production requires sync token",
			env: map[string]string{
				"GATEWAYFORGE_ENV":     "production",
				"ALLOW_MOCK_UPSTREAMS": "false",
			},
			wantErr: true,
		},
		{
			name: "valid production gateway",
			env: map[string]string{
				"GATEWAYFORGE_ENV":     "production",
				"GATEWAY_INSTANCE_ID":  "gw-prod-01",
				"CONTROL_PLANE_URL":    "https://cp.internal:8080",
				"GATEWAY_SYNC_TOKEN":   "sync-token-0123456789",
				"REDIS_ADDR":           "redis.internal:6379",
				"ALLOW_MOCK_UPSTREAMS": "false",
			},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadGateway(mapLookup(tc.env))
			if tc.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestEnvExampleLoadsCleanly(t *testing.T) {
	rootEnv := filepath.Join("..", "..", "..", ".env.example")
	f, err := os.Open(rootEnv)
	if err != nil {
		t.Fatalf("open .env.example: %v", err)
	}
	defer func() { _ = f.Close() }()

	values := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("invalid line in .env.example: %q", line)
		}
		values[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan .env.example: %v", err)
	}

	if _, err := LoadControlPlane(mapLookup(values)); err != nil {
		t.Fatalf(".env.example failed LoadControlPlane: %v", err)
	}
	if _, err := LoadGateway(mapLookup(values)); err != nil {
		t.Fatalf(".env.example failed LoadGateway: %v", err)
	}
}
