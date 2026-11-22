package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// LookupFunc retrieves environment variables (defaults to os.LookupEnv).
type LookupFunc func(key string) (string, bool)

// Environment represents the deployment mode.
type Environment string

const (
	EnvDevelopment Environment = "development"
	EnvTest        Environment = "test"
	EnvProduction  Environment = "production"
)

// ControlPlaneConfig holds validated control-plane startup configuration.
type ControlPlaneConfig struct {
	Env                Environment
	Addr               string
	ShutdownTimeout    time.Duration
	DatabaseURL        string
	RedisAddr          string
	RedisPassword      string
	SessionSecret      string
	GatewaySyncToken   string
	AllowedCORSOrigins []string
	AllowMockUpstreams bool
}

// GatewayConfig holds validated gateway data-plane startup configuration.
type GatewayConfig struct {
	Env                Environment
	InstanceID         string
	Addr               string
	ShutdownTimeout    time.Duration
	ControlPlaneURL    string
	GatewaySyncToken   string
	RedisAddr          string
	RedisPassword      string
	SyncInterval       time.Duration
	AllowMockUpstreams bool
}

// Redacted returns a log-safe summary with all secret fields masked.
func (c ControlPlaneConfig) Redacted() map[string]any {
	return map[string]any{
		"env":                  string(c.Env),
		"addr":                 c.Addr,
		"shutdown_timeout":     c.ShutdownTimeout.String(),
		"database_url":         redactURLPassword(c.DatabaseURL),
		"redis_addr":           c.RedisAddr,
		"redis_password_set":   c.RedisPassword != "",
		"session_secret_set":   c.SessionSecret != "",
		"sync_token_set":       c.GatewaySyncToken != "",
		"allowed_cors_origins": c.AllowedCORSOrigins,
		"allow_mock_upstreams": c.AllowMockUpstreams,
	}
}

// Redacted returns a log-safe summary with all secret fields masked.
func (g GatewayConfig) Redacted() map[string]any {
	return map[string]any{
		"env":                  string(g.Env),
		"instance_id":          g.InstanceID,
		"addr":                 g.Addr,
		"shutdown_timeout":     g.ShutdownTimeout.String(),
		"control_plane_url":    g.ControlPlaneURL,
		"sync_token_set":       g.GatewaySyncToken != "",
		"redis_addr":           g.RedisAddr,
		"redis_password_set":   g.RedisPassword != "",
		"sync_interval":        g.SyncInterval.String(),
		"allow_mock_upstreams": g.AllowMockUpstreams,
	}
}

// LoadControlPlane parses and validates control-plane configuration without echoing secrets in errors.
func LoadControlPlane(lookup LookupFunc) (ControlPlaneConfig, error) {
	var errs []error

	env, err := parseEnvironment(getOrDefault(lookup, "GATEWAYFORGE_ENV", "development"))
	if err != nil {
		errs = append(errs, err)
	}

	addr := getOrDefault(lookup, "CONTROL_PLANE_ADDR", ":8080")
	if err := validateListenAddr("CONTROL_PLANE_ADDR", addr); err != nil {
		errs = append(errs, err)
	}

	shutdownSec, err := parsePositiveInt(lookup, "CONTROL_PLANE_SHUTDOWN_TIMEOUT_SEC", 10, 1, 300)
	if err != nil {
		errs = append(errs, err)
	}

	dbURL := getOrDefault(lookup, "DATABASE_URL", "")
	if dbURL != "" {
		if err := validatePostgresURL(dbURL); err != nil {
			errs = append(errs, err)
		}
	} else if env == EnvProduction {
		errs = append(errs, errors.New("DATABASE_URL is required in production"))
	}

	redisAddr := getOrDefault(lookup, "REDIS_ADDR", "localhost:6379")
	if err := validateHostPort("REDIS_ADDR", redisAddr); err != nil {
		errs = append(errs, err)
	}

	redisPassword := getOrDefault(lookup, "REDIS_PASSWORD", "")

	sessionSecret := getOrDefault(lookup, "SESSION_SECRET", "")
	if sessionSecret != "" && len(sessionSecret) < 32 {
		errs = append(errs, errors.New("SESSION_SECRET must be at least 32 characters"))
	}
	if env == EnvProduction && sessionSecret == "" {
		errs = append(errs, errors.New("SESSION_SECRET is required in production"))
	}

	syncToken := getOrDefault(lookup, "GATEWAY_SYNC_TOKEN", "")
	if syncToken != "" && len(syncToken) < 16 {
		errs = append(errs, errors.New("GATEWAY_SYNC_TOKEN must be at least 16 characters"))
	}
	if env == EnvProduction && syncToken == "" {
		errs = append(errs, errors.New("GATEWAY_SYNC_TOKEN is required in production"))
	}

	allowMocks, err := parseBool(lookup, "ALLOW_MOCK_UPSTREAMS", env != EnvProduction)
	if err != nil {
		errs = append(errs, err)
	}
	if env == EnvProduction && allowMocks {
		errs = append(errs, errors.New("ALLOW_MOCK_UPSTREAMS cannot be enabled in production"))
	}

	originsRaw := getOrDefault(lookup, "CONTROL_PLANE_CORS_ORIGINS", "http://localhost:3000")
	var origins []string
	for _, part := range strings.Split(originsRaw, ",") {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		if trimmed == "*" {
			errs = append(errs, errors.New("CONTROL_PLANE_CORS_ORIGINS must not contain wildcard '*'"))
			continue
		}
		u, uErr := url.Parse(trimmed)
		if uErr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf("CONTROL_PLANE_CORS_ORIGINS contains invalid origin"))
			continue
		}
		origins = append(origins, trimmed)
	}

	if len(errs) > 0 {
		return ControlPlaneConfig{}, errors.Join(errs...)
	}

	return ControlPlaneConfig{
		Env:                env,
		Addr:               addr,
		ShutdownTimeout:    time.Duration(shutdownSec) * time.Second,
		DatabaseURL:        dbURL,
		RedisAddr:          redisAddr,
		RedisPassword:      redisPassword,
		SessionSecret:      sessionSecret,
		GatewaySyncToken:   syncToken,
		AllowedCORSOrigins: origins,
		AllowMockUpstreams: allowMocks,
	}, nil
}

// LoadGateway parses and validates gateway data-plane configuration without echoing secrets in errors.
func LoadGateway(lookup LookupFunc) (GatewayConfig, error) {
	var errs []error

	env, err := parseEnvironment(getOrDefault(lookup, "GATEWAYFORGE_ENV", "development"))
	if err != nil {
		errs = append(errs, err)
	}

	instanceID := strings.TrimSpace(getOrDefault(lookup, "GATEWAY_INSTANCE_ID", "gateway-local-1"))
	if instanceID == "" || len(instanceID) > 64 {
		errs = append(errs, errors.New("GATEWAY_INSTANCE_ID must be 1..64 characters"))
	}

	addr := getOrDefault(lookup, "GATEWAY_ADDR", ":8081")
	if err := validateListenAddr("GATEWAY_ADDR", addr); err != nil {
		errs = append(errs, err)
	}

	shutdownSec, err := parsePositiveInt(lookup, "GATEWAY_SHUTDOWN_TIMEOUT_SEC", 15, 1, 300)
	if err != nil {
		errs = append(errs, err)
	}

	cpURL := getOrDefault(lookup, "CONTROL_PLANE_URL", "http://localhost:8080")
	u, uErr := url.Parse(cpURL)
	if uErr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		errs = append(errs, errors.New("CONTROL_PLANE_URL must be a valid http or https URL"))
	}

	syncToken := getOrDefault(lookup, "GATEWAY_SYNC_TOKEN", "")
	if syncToken != "" && len(syncToken) < 16 {
		errs = append(errs, errors.New("GATEWAY_SYNC_TOKEN must be at least 16 characters"))
	}
	if env == EnvProduction && syncToken == "" {
		errs = append(errs, errors.New("GATEWAY_SYNC_TOKEN is required in production"))
	}

	redisAddr := getOrDefault(lookup, "REDIS_ADDR", "localhost:6379")
	if err := validateHostPort("REDIS_ADDR", redisAddr); err != nil {
		errs = append(errs, err)
	}
	redisPassword := getOrDefault(lookup, "REDIS_PASSWORD", "")

	syncSec, err := parsePositiveInt(lookup, "GATEWAY_SYNC_INTERVAL_SEC", 5, 1, 300)
	if err != nil {
		errs = append(errs, err)
	}

	allowMocks, err := parseBool(lookup, "ALLOW_MOCK_UPSTREAMS", env != EnvProduction)
	if err != nil {
		errs = append(errs, err)
	}
	if env == EnvProduction && allowMocks {
		errs = append(errs, errors.New("ALLOW_MOCK_UPSTREAMS cannot be enabled in production"))
	}

	if len(errs) > 0 {
		return GatewayConfig{}, errors.Join(errs...)
	}

	return GatewayConfig{
		Env:                env,
		InstanceID:         instanceID,
		Addr:               addr,
		ShutdownTimeout:    time.Duration(shutdownSec) * time.Second,
		ControlPlaneURL:    cpURL,
		GatewaySyncToken:   syncToken,
		RedisAddr:          redisAddr,
		RedisPassword:      redisPassword,
		SyncInterval:       time.Duration(syncSec) * time.Second,
		AllowMockUpstreams: allowMocks,
	}, nil
}

func getOrDefault(lookup LookupFunc, key, fallback string) string {
	if v, ok := lookup(key); ok {
		return strings.TrimSpace(v)
	}
	return fallback
}

func parseEnvironment(raw string) (Environment, error) {
	switch Environment(strings.ToLower(strings.TrimSpace(raw))) {
	case EnvDevelopment:
		return EnvDevelopment, nil
	case EnvTest:
		return EnvTest, nil
	case EnvProduction:
		return EnvProduction, nil
	default:
		return "", errors.New("GATEWAYFORGE_ENV must be one of development, test, production")
	}
}

func parsePositiveInt(lookup LookupFunc, key string, fallback, minVal, maxVal int) (int, error) {
	raw, ok := lookup(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < minVal || n > maxVal {
		return 0, fmt.Errorf("%s must be an integer between %d and %d", key, minVal, maxVal)
	}
	return n, nil
}

func parseBool(lookup LookupFunc, key string, fallback bool) (bool, error) {
	raw, ok := lookup(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	b, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean (true or false)", key)
	}
	return b, nil
}

func validateListenAddr(key, addr string) error {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%s must be a valid host:port listen address", key)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 0 || port > 65535 {
		return fmt.Errorf("%s port must be between 0 and 65535", key)
	}
	return nil
}

func validateHostPort(key, addr string) error {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return fmt.Errorf("%s must be a valid host:port address", key)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("%s port must be between 1 and 65535", key)
	}
	return nil
}

func validatePostgresURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("DATABASE_URL is not a valid URL")
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return errors.New("DATABASE_URL must use postgres:// or postgresql:// scheme")
	}
	if u.Host == "" || strings.TrimPrefix(u.Path, "/") == "" {
		return errors.New("DATABASE_URL must specify host and database name")
	}
	return nil
}

func redactURLPassword(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "[invalid-url]"
	}
	if u.User != nil {
		u.User = url.UserPassword(u.User.Username(), "REDACTED")
	}
	return u.String()
}
