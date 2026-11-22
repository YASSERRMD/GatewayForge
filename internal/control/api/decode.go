package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultMaxJSONBytes caps standard control-plane JSON payloads at 256 KiB.
const DefaultMaxJSONBytes int64 = 256 << 10

// DecodeStrictJSON reads and validates a single JSON object with strict unknown-field rejection
// and request size caps. Writes an RFC 9457 Problem response and returns false on error.
func DecodeStrictJSON(w http.ResponseWriter, r *http.Request, maxBytes int64, dst any) bool {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxJSONBytes
	}
	if ct := r.Header.Get("Content-Type"); ct != "" {
		mediaType := strings.ToLower(strings.TrimSpace(strings.Split(ct, ";")[0]))
		if mediaType != "application/json" && mediaType != "application/merge-patch+json" {
			WriteProblem(w, r, http.StatusUnsupportedMediaType, "unsupported_media_type", "Unsupported Media Type", "Content-Type must be application/json.")
			return false
		}
	}

	limited := http.MaxBytesReader(w, r.Body, maxBytes)
	raw, err := io.ReadAll(limited)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			WriteProblem(w, r, http.StatusRequestEntityTooLarge, "payload_too_large", "Payload Too Large", "Request body exceeds maximum allowed size.")
			return false
		}
		WriteProblem(w, r, http.StatusBadRequest, "invalid_body", "Invalid Request Body", "Unable to read request body.")
		return false
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		WriteProblem(w, r, http.StatusBadRequest, "empty_body", "Empty Request Body", "Request body must not be empty.")
		return false
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		WriteProblem(w, r, http.StatusBadRequest, "invalid_json", "Invalid JSON", err.Error())
		return false
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		WriteProblem(w, r, http.StatusBadRequest, "invalid_json", "Invalid JSON", "Request body must contain a single JSON object.")
		return false
	}
	return true
}

// IdempotencyStore persists Idempotency-Key responses per organization in PostgreSQL.
type IdempotencyStore struct {
	pool *pgxpool.Pool
}

// NewIdempotencyStore constructs an IdempotencyStore.
func NewIdempotencyStore(pool *pgxpool.Pool) *IdempotencyStore {
	return &IdempotencyStore{pool: pool}
}

// IdempotencyRecord holds a previously recorded idempotent response.
type IdempotencyRecord struct {
	RequestHash    string
	ResponseStatus int
	ResponseBody   json.RawMessage
}

// HashRequestPayload computes a deterministic SHA-256 hash of method, path, and body bytes.
func HashRequestPayload(method, path string, body []byte) string {
	h := sha256.New()
	h.Write([]byte(method))
	h.Write([]byte("\n"))
	h.Write([]byte(path))
	h.Write([]byte("\n"))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// ErrIdempotencyPayloadMismatch is returned when an Idempotency-Key is reused with a different payload.
var ErrIdempotencyPayloadMismatch = errors.New("idempotency key reused with different request payload")

// Lookup checks if an idempotency key already exists for orgID.
// Returns (record, true, nil) on exact replay, (zero, false, nil) if new, or ErrIdempotencyPayloadMismatch.
func (s *IdempotencyStore) Lookup(ctx context.Context, orgID, key, reqHash string) (IdempotencyRecord, bool, error) {
	var rec IdempotencyRecord
	err := s.pool.QueryRow(ctx, `
		SELECT request_hash, response_status, response_body
		FROM idempotency_keys
		WHERE org_id = $1 AND idempotency_key = $2
	`, orgID, key).Scan(&rec.RequestHash, &rec.ResponseStatus, &rec.ResponseBody)
	if errors.Is(err, pgx.ErrNoRows) {
		return IdempotencyRecord{}, false, nil
	}
	if err != nil {
		return IdempotencyRecord{}, false, err
	}
	if rec.RequestHash != reqHash {
		return IdempotencyRecord{}, false, ErrIdempotencyPayloadMismatch
	}
	return rec, true, nil
}

// Save stores the response for an idempotency key.
func (s *IdempotencyStore) Save(ctx context.Context, orgID, key, method, path, reqHash string, status int, body json.RawMessage) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO idempotency_keys (org_id, idempotency_key, request_method, request_path, request_hash, response_status, response_body)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (org_id, idempotency_key) DO NOTHING
	`, orgID, key, method, path, reqHash, status, body)
	return err
}
