package api

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/YASSERRMD/GatewayForge/internal/control/store"
	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

// SyncHandler exposes the least-privilege read-only gateway snapshot pull interface.
// Gateways authenticate with a rotation-ready Bearer sync token and never hold admin write credentials.
type SyncHandler struct {
	Store      *store.Store
	SyncTokens []string // supports multiple active tokens during zero-downtime rotation
}

// ParseSyncTokens splits comma-separated rotation-ready sync tokens.
func ParseSyncTokens(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		t := strings.TrimSpace(part)
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

// MountGatewaySyncRoutes registers GET /gateway/snapshots/active under v1.
func (h *SyncHandler) MountGatewaySyncRoutes(v1 chi.Router) {
	v1.Get("/gateway/snapshots/active", h.handleGetActiveSnapshot)
}

func (h *SyncHandler) authenticateSyncToken(r *http.Request) bool {
	if len(h.SyncTokens) == 0 {
		return false
	}
	authz := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(authz, "Bearer ") {
		return false
	}
	presented := strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
	if presented == "" {
		return false
	}
	matched := 0
	for _, valid := range h.SyncTokens {
		if len(valid) > 0 && subtle.ConstantTimeCompare([]byte(presented), []byte(valid)) == 1 {
			matched = 1
		}
	}
	return matched == 1
}

func (h *SyncHandler) handleGetActiveSnapshot(w http.ResponseWriter, r *http.Request) {
	if !h.authenticateSyncToken(r) {
		WriteProblem(w, r, http.StatusUnauthorized, "invalid_sync_token", "Unauthorized", "Valid gateway sync bearer token is required.")
		return
	}

	orgFilter := strings.TrimSpace(r.URL.Query().Get("org_id"))
	if orgFilter != "" && !uuidPattern.MatchString(orgFilter) {
		WriteProblem(w, r, http.StatusBadRequest, "invalid_org_id", "Invalid Organization ID", "org_id must be a valid UUID.")
		return
	}

	query := `
		SELECT p.org_id, p.version, p.snapshot_json, p.published_at, ap.activation_revision
		FROM active_publications ap
		JOIN publications p ON p.id = ap.publication_id
		JOIN organizations o ON o.id = ap.org_id
		WHERE o.enabled = TRUE
	`
	args := []any{}
	if orgFilter != "" {
		query += " AND ap.org_id = $1"
		args = append(args, orgFilter)
	}
	query += " ORDER BY p.org_id ASC"

	rows, err := h.Store.Pool().Query(r.Context(), query, args...)
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not query active snapshots.")
		return
	}
	defer rows.Close()

	merged := snapshot.Snapshot{
		SchemaVersion: snapshot.SchemaVersion,
		OrgID:         orgFilter,
		Version:       0,
		Pools:         make([]snapshot.UpstreamPool, 0),
		Routes:        make([]snapshot.Route, 0),
		Consumers:     make([]snapshot.Consumer, 0),
	}

	var latestPublished time.Time
	var totalActivationRev int
	for rows.Next() {
		var orgID string
		var version int
		var snapRaw json.RawMessage
		var pubAt time.Time
		var actRev int
		if err := rows.Scan(&orgID, &version, &snapRaw, &pubAt, &actRev); err != nil {
			WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not scan active snapshot.")
			return
		}
		var one snapshot.Snapshot
		if err := json.Unmarshal(snapRaw, &one); err != nil {
			WriteProblem(w, r, http.StatusInternalServerError, "corrupt_snapshot", "Snapshot Error", "Stored snapshot JSON could not be decoded.")
			return
		}
		merged.Pools = append(merged.Pools, one.Pools...)
		merged.Routes = append(merged.Routes, one.Routes...)
		merged.Consumers = append(merged.Consumers, one.Consumers...)
		totalActivationRev += actRev
		if orgFilter != "" {
			merged.Version = version
		} else {
			merged.Version = totalActivationRev
		}
		if pubAt.After(latestPublished) {
			latestPublished = pubAt
		}
	}
	if err := rows.Err(); err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Error reading active snapshots.")
		return
	}

	sort.Slice(merged.Pools, func(i, j int) bool { return merged.Pools[i].ID < merged.Pools[j].ID })
	sort.Slice(merged.Routes, func(i, j int) bool {
		if merged.Routes[i].Priority != merged.Routes[j].Priority {
			return merged.Routes[i].Priority > merged.Routes[j].Priority
		}
		if len(merged.Routes[i].PathPrefix) != len(merged.Routes[j].PathPrefix) {
			return len(merged.Routes[i].PathPrefix) > len(merged.Routes[j].PathPrefix)
		}
		return merged.Routes[i].ID < merged.Routes[j].ID
	})
	sort.Slice(merged.Consumers, func(i, j int) bool { return merged.Consumers[i].ID < merged.Consumers[j].ID })
	merged.PublishedAt = latestPublished.UTC().Truncate(time.Second)

	sum, err := merged.ComputeChecksum()
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "checksum_error", "Checksum Error", "Could not compute snapshot checksum.")
		return
	}
	merged.Checksum = sum

	etag := `"` + sum + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-store")
	if strings.TrimSpace(r.Header.Get("If-None-Match")) == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	WriteJSON(w, http.StatusOK, merged)
}
