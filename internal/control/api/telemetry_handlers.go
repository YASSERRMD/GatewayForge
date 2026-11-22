package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/YASSERRMD/GatewayForge/internal/control/auth"
	"github.com/YASSERRMD/GatewayForge/internal/control/rbac"
	"github.com/YASSERRMD/GatewayForge/internal/control/store"
	"github.com/YASSERRMD/GatewayForge/internal/shared/telemetry"
)

// TelemetryHandler serves tenant-isolated gateway instance health, aggregate metrics, trends, and trace links.
type TelemetryHandler struct {
	Store              *store.Store
	Sessions           *auth.SessionManager
	Heartbeats         *telemetry.HeartbeatStore
	Aggregates         *telemetry.AggregateStore
	Correlations       *telemetry.CorrelationStore
	TraceViewerBaseURL string
}

// MountTelemetryRoutes registers /orgs/{orgId}/gateways, /orgs/{orgId}/metrics/*, and /orgs/{orgId}/telemetry/* under v1.
func (h *TelemetryHandler) MountTelemetryRoutes(v1 chi.Router) {
	orgRead := v1.With(RequireAuth(h.Sessions), RequireOrgContext(h.Sessions), RequirePermission(rbac.PermMetricsRead))
	orgRead.Get("/orgs/{orgId}/gateways", h.handleListGateways)
	orgRead.Get("/orgs/{orgId}/metrics/summary", h.handleMetricsSummary)
	orgRead.Get("/orgs/{orgId}/metrics/timeseries", h.handleMetricsTimeSeries)
	orgRead.Get("/orgs/{orgId}/metrics/errors", h.handleMetricsErrors)
	orgRead.Get("/orgs/{orgId}/telemetry/traces/{requestId}", h.handleTraceCorrelation)
}

func (h *TelemetryHandler) activeOrgRelease(r *http.Request, orgID string) (version int, checksum string) {
	if h.Store == nil {
		return 0, ""
	}
	_ = h.Store.Pool().QueryRow(r.Context(), `
		SELECT version, snapshot_checksum
		FROM publications
		WHERE org_id = $1 AND status = 'active'
		ORDER BY version DESC
		LIMIT 1
	`, orgID).Scan(&version, &checksum)
	return version, checksum
}

func (h *TelemetryHandler) handleListGateways(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	activeVer, activeSum := h.activeOrgRelease(r, orgCtx.OrgID)

	if h.Heartbeats == nil {
		WriteJSON(w, http.StatusOK, map[string]any{
			"items":               []telemetry.GatewayInstanceStatus{},
			"total":               0,
			"active_version":      activeVer,
			"active_checksum":     activeSum,
			"telemetry_available": false,
		})
		return
	}

	items, err := h.Heartbeats.ListForOrg(r.Context(), orgCtx.OrgID, activeVer, activeSum)
	if err != nil {
		WriteJSON(w, http.StatusOK, map[string]any{
			"items":               []telemetry.GatewayInstanceStatus{},
			"total":               0,
			"active_version":      activeVer,
			"active_checksum":     activeSum,
			"telemetry_available": false,
		})
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"items":               items,
		"total":               len(items),
		"active_version":      activeVer,
		"active_checksum":     activeSum,
		"telemetry_available": true,
	})
}

func (h *TelemetryHandler) handleMetricsSummary(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	rangeParam := r.URL.Query().Get("range")

	var sum telemetry.MetricsSummary
	if h.Aggregates != nil {
		if s, err := h.Aggregates.Summary(r.Context(), orgCtx.OrgID, rangeParam); err == nil {
			sum = s
		} else {
			lbl, win, _ := telemetry.ParseRange(rangeParam)
			sum = telemetry.MetricsSummary{OrgID: orgCtx.OrgID, Range: lbl, WindowSeconds: int64(win.Seconds()), DataAvailable: false}
		}
	} else {
		lbl, win, _ := telemetry.ParseRange(rangeParam)
		sum = telemetry.MetricsSummary{OrgID: orgCtx.OrgID, Range: lbl, WindowSeconds: int64(win.Seconds()), DataAvailable: false}
	}

	if h.Heartbeats != nil {
		activeVer, activeSum := h.activeOrgRelease(r, orgCtx.OrgID)
		if gws, err := h.Heartbeats.ListForOrg(r.Context(), orgCtx.OrgID, activeVer, activeSum); err == nil {
			for _, g := range gws {
				switch g.Status {
				case "healthy":
					sum.ActiveGateways++
				case "stale":
					sum.StaleGateways++
				}
			}
		}
	}
	WriteJSON(w, http.StatusOK, sum)
}

func (h *TelemetryHandler) handleMetricsTimeSeries(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	rangeParam := r.URL.Query().Get("range")

	if h.Aggregates == nil {
		lbl, _, step := telemetry.ParseRange(rangeParam)
		WriteJSON(w, http.StatusOK, map[string]any{
			"org_id":         orgCtx.OrgID,
			"range":          lbl,
			"bucket_minutes": step,
			"points":         []telemetry.TimeSeriesPoint{},
			"data_available": false,
		})
		return
	}

	lbl, step, pts, err := h.Aggregates.TimeSeries(r.Context(), orgCtx.OrgID, rangeParam)
	if err != nil {
		WriteJSON(w, http.StatusOK, map[string]any{
			"org_id":         orgCtx.OrgID,
			"range":          lbl,
			"bucket_minutes": step,
			"points":         []telemetry.TimeSeriesPoint{},
			"data_available": false,
		})
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"org_id":         orgCtx.OrgID,
		"range":          lbl,
		"bucket_minutes": step,
		"points":         pts,
		"data_available": true,
	})
}

func (h *TelemetryHandler) handleMetricsErrors(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	rangeParam := r.URL.Query().Get("range")

	if h.Aggregates == nil {
		lbl, _, _ := telemetry.ParseRange(rangeParam)
		WriteJSON(w, http.StatusOK, telemetry.ErrorBreakdown{
			OrgID:         orgCtx.OrgID,
			Range:         lbl,
			ByStatusClass: map[string]int64{"2xx": 0, "3xx": 0, "4xx": 0, "5xx": 0},
			ByOutcome:     map[string]int64{},
			DataAvailable: false,
		})
		return
	}
	eb, err := h.Aggregates.Errors(r.Context(), orgCtx.OrgID, rangeParam)
	if err != nil {
		lbl, _, _ := telemetry.ParseRange(rangeParam)
		WriteJSON(w, http.StatusOK, telemetry.ErrorBreakdown{
			OrgID:         orgCtx.OrgID,
			Range:         lbl,
			ByStatusClass: map[string]int64{"2xx": 0, "3xx": 0, "4xx": 0, "5xx": 0},
			ByOutcome:     map[string]int64{},
			DataAvailable: false,
		})
		return
	}
	WriteJSON(w, http.StatusOK, eb)
}

func (h *TelemetryHandler) handleTraceCorrelation(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	reqID := chi.URLParam(r, "requestId")
	if !telemetry.IsSafeRequestID(reqID) {
		WriteProblem(w, r, http.StatusBadRequest, "invalid_request_id", "Invalid Request ID", "requestId must be 4..128 safe characters.")
		return
	}
	if h.Correlations == nil {
		WriteJSON(w, http.StatusOK, telemetry.TraceLinkResponse{
			Found:                false,
			TraceViewerAvailable: false,
			UnavailableReason:    "telemetry correlation store is not configured",
		})
		return
	}
	rec, err := h.Correlations.Lookup(r.Context(), orgCtx.OrgID, reqID)
	if err != nil {
		WriteJSON(w, http.StatusOK, telemetry.TraceLinkResponse{
			Found:                false,
			TraceViewerAvailable: false,
			UnavailableReason:    "telemetry backend temporarily unavailable",
		})
		return
	}
	if rec == nil {
		WriteJSON(w, http.StatusOK, telemetry.TraceLinkResponse{
			Found:                false,
			TraceViewerAvailable: h.TraceViewerBaseURL != "",
			UnavailableReason:    "request correlation not found or expired",
		})
		return
	}

	link, ok := telemetry.BuildTraceViewerURL(h.TraceViewerBaseURL, rec.TraceID)
	resp := telemetry.TraceLinkResponse{
		Found:                true,
		TraceViewerAvailable: ok,
		TraceURL:             link,
		Record:               rec,
	}
	if !ok {
		resp.UnavailableReason = "trace viewer URL is not configured (set TRACE_VIEWER_BASE_URL)"
	}
	WriteJSON(w, http.StatusOK, resp)
}
