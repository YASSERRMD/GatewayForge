package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/YASSERRMD/GatewayForge/internal/control/auth"
	"github.com/YASSERRMD/GatewayForge/internal/control/rbac"
	"github.com/YASSERRMD/GatewayForge/internal/control/store"
)

// AuditRecorder writes redacted append-only audit events into PostgreSQL.
type AuditRecorder struct {
	Store *store.Store
}

// NewAuditRecorder constructs an AuditRecorder.
func NewAuditRecorder(st *store.Store) *AuditRecorder {
	return &AuditRecorder{Store: st}
}

// RecordRedacted marshals and deeply redacts before/after maps before persisting an audit event.
func (a *AuditRecorder) RecordRedacted(
	ctx context.Context,
	orgID *string,
	actorUserID *string,
	actorEmail, action, targetType, targetID, requestID string,
	before, after map[string]any,
) error {
	if requestID == "" {
		requestID = "req_internal"
	}
	var beforeJSON, afterJSON json.RawMessage
	if before != nil {
		b, err := json.Marshal(auth.RedactSensitiveMap(before))
		if err != nil {
			return fmt.Errorf("marshal before audit payload: %w", err)
		}
		beforeJSON = b
	}
	if after != nil {
		b, err := json.Marshal(auth.RedactSensitiveMap(after))
		if err != nil {
			return fmt.Errorf("marshal after audit payload: %w", err)
		}
		afterJSON = b
	}
	_, err := a.Store.AppendAuditEvent(ctx, store.AuditEvent{
		OrgID:          orgID,
		ActorUserID:    actorUserID,
		ActorEmail:     actorEmail,
		Action:         action,
		TargetType:     targetType,
		TargetID:       targetID,
		RequestID:      requestID,
		BeforeRedacted: beforeJSON,
		AfterRedacted:  afterJSON,
	})
	return err
}

// AuditMutationsMiddleware wraps org-scoped mutation handlers and automatically records
// redacted audit events for every successful 2xx POST/PUT/PATCH/DELETE request.
func AuditMutationsMiddleware(recorder *AuditRecorder) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if recorder == nil {
				next.ServeHTTP(w, r)
				return
			}
			switch r.Method {
			case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			default:
				next.ServeHTTP(w, r)
				return
			}

			var reqBodyCopy []byte
			if r.Body != nil {
				raw, err := io.ReadAll(io.LimitReader(r.Body, 256<<10))
				if err == nil {
					reqBodyCopy = raw
					r.Body = io.NopCloser(bytes.NewReader(raw))
				}
			}

			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)

			if rec.status < 200 || rec.status >= 300 {
				return
			}

			orgCtx, hasOrg := OrgContextFromContext(r.Context())
			if !hasOrg {
				return
			}
			principal, _ := PrincipalFromContext(r.Context())
			reqID := RequestIDFromContext(r.Context())

			var afterMap map[string]any
			if len(reqBodyCopy) > 0 {
				var parsed map[string]any
				if err := json.Unmarshal(reqBodyCopy, &parsed); err == nil {
					afterMap = auth.RedactSensitiveMap(parsed)
				}
			}
			if afterMap == nil {
				afterMap = map[string]any{}
			}
			afterMap["http_status"] = rec.status
			afterMap["http_method"] = r.Method

			targetType, targetID := inferAuditTarget(r.URL.Path, orgCtx.OrgID)
			action := strings.ToLower(r.Method) + ":" + targetType
			orgIDCopy := orgCtx.OrgID
			var userIDPtr *string
			if principal.UserID != "" {
				uid := principal.UserID
				userIDPtr = &uid
			}

			_ = recorder.RecordRedacted(
				r.Context(),
				&orgIDCopy,
				userIDPtr,
				principal.Email,
				action,
				targetType,
				targetID,
				reqID,
				nil,
				afterMap,
			)
		})
	}
}

func inferAuditTarget(path, orgID string) (string, string) {
	prefix := "/api/v1/orgs/" + orgID
	trimmed := strings.TrimPrefix(path, prefix)
	trimmed = strings.Trim(trimmed, "/")
	if trimmed == "" {
		return "organization", orgID
	}
	parts := strings.Split(trimmed, "/")
	targetType := parts[0]
	targetID := ""
	if len(parts) > 1 {
		targetID = parts[1]
	}
	return targetType, targetID
}

// AuditHandler exposes tenant-scoped read-only audit event listing and detail endpoints.
type AuditHandler struct {
	Store    *store.Store
	Sessions *auth.SessionManager
}

// MountAuditRoutes mounts /orgs/{orgId}/audit-events routes under v1.
func (h *AuditHandler) MountAuditRoutes(v1 chi.Router) {
	v1.With(RequireAuth(h.Sessions), RequireOrgContext(h.Sessions)).Route("/orgs/{orgId}/audit-events", func(r chi.Router) {
		r.With(RequirePermission(rbac.PermAuditRead)).Get("/", h.handleListAuditEvents)
		r.With(RequirePermission(rbac.PermAuditRead)).Get("/{eventId}", h.handleGetAuditEvent)
	})
}

func (h *AuditHandler) handleListAuditEvents(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	limit, offset := parsePagination(r)

	where := []string{"org_id = $1"}
	args := []any{orgCtx.OrgID}
	argIdx := 2

	if action := strings.TrimSpace(r.URL.Query().Get("action")); action != "" {
		where = append(where, fmt.Sprintf("action = $%d", argIdx))
		args = append(args, action)
		argIdx++
	}
	if targetType := strings.TrimSpace(r.URL.Query().Get("target_type")); targetType != "" {
		where = append(where, fmt.Sprintf("target_type = $%d", argIdx))
		args = append(args, targetType)
		argIdx++
	}
	if actorEmail := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("actor_email"))); actorEmail != "" {
		where = append(where, fmt.Sprintf("actor_email = $%d", argIdx))
		args = append(args, actorEmail)
		argIdx++
	}
	if fromRaw := strings.TrimSpace(r.URL.Query().Get("from")); fromRaw != "" {
		ts, err := time.Parse(time.RFC3339, fromRaw)
		if err != nil {
			WriteProblem(w, r, http.StatusBadRequest, "invalid_query", "Invalid Query Parameter", "from must be RFC3339 timestamp.")
			return
		}
		where = append(where, fmt.Sprintf("created_at >= $%d", argIdx))
		args = append(args, ts)
		argIdx++
	}
	if toRaw := strings.TrimSpace(r.URL.Query().Get("to")); toRaw != "" {
		ts, err := time.Parse(time.RFC3339, toRaw)
		if err != nil {
			WriteProblem(w, r, http.StatusBadRequest, "invalid_query", "Invalid Query Parameter", "to must be RFC3339 timestamp.")
			return
		}
		where = append(where, fmt.Sprintf("created_at <= $%d", argIdx))
		args = append(args, ts)
		argIdx++
	}

	clause := strings.Join(where, " AND ")
	var total int
	if err := h.Store.Pool().QueryRow(r.Context(), "SELECT count(*) FROM audit_events WHERE "+clause, args...).Scan(&total); err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not count audit events.")
		return
	}

	queryArgs := append(append([]any(nil), args...), limit, offset)
	query := fmt.Sprintf(`
		SELECT id, org_id, actor_user_id, actor_email, action, target_type, target_id,
		       request_id, before_redacted, after_redacted, created_at
		FROM audit_events
		WHERE %s
		ORDER BY created_at DESC, id DESC
		LIMIT $%d OFFSET $%d
	`, clause, argIdx, argIdx+1)

	rows, err := h.Store.Pool().Query(r.Context(), query, queryArgs...)
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not query audit events.")
		return
	}
	defer rows.Close()

	items := make([]store.AuditEvent, 0)
	for rows.Next() {
		var ev store.AuditEvent
		if err := rows.Scan(
			&ev.ID, &ev.OrgID, &ev.ActorUserID, &ev.ActorEmail, &ev.Action, &ev.TargetType, &ev.TargetID,
			&ev.RequestID, &ev.BeforeRedacted, &ev.AfterRedacted, &ev.CreatedAt,
		); err != nil {
			WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not read audit event.")
			return
		}
		items = append(items, ev)
	}

	WriteJSON(w, http.StatusOK, map[string]any{
		"items":  items,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

func (h *AuditHandler) handleGetAuditEvent(w http.ResponseWriter, r *http.Request) {
	orgCtx, _ := OrgContextFromContext(r.Context())
	eventID, err := strconv.ParseInt(chi.URLParam(r, "eventId"), 10, 64)
	if err != nil || eventID < 1 {
		WriteProblem(w, r, http.StatusBadRequest, "invalid_event_id", "Invalid Event ID", "Audit event ID must be a positive integer.")
		return
	}

	var ev store.AuditEvent
	err = h.Store.Pool().QueryRow(r.Context(), `
		SELECT id, org_id, actor_user_id, actor_email, action, target_type, target_id,
		       request_id, before_redacted, after_redacted, created_at
		FROM audit_events
		WHERE org_id = $1 AND id = $2
	`, orgCtx.OrgID, eventID).Scan(
		&ev.ID, &ev.OrgID, &ev.ActorUserID, &ev.ActorEmail, &ev.Action, &ev.TargetType, &ev.TargetID,
		&ev.RequestID, &ev.BeforeRedacted, &ev.AfterRedacted, &ev.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		WriteProblem(w, r, http.StatusNotFound, "not_found", "Not Found", "Audit event not found.")
		return
	}
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Database Error", "Could not retrieve audit event.")
		return
	}
	WriteJSON(w, http.StatusOK, ev)
}
