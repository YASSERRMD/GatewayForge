package syncer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/gateway/table"
	"github.com/YASSERRMD/GatewayForge/internal/shared/events"
	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

// PollerConfig configures authenticated snapshot synchronization from the control plane.
type PollerConfig struct {
	ControlPlaneURL string
	SyncToken       string
	OrgID           string
	Interval        time.Duration
	HTTPTimeout     time.Duration
	Logger          *slog.Logger
	Client          *http.Client
	Hints           *events.HintCoalescer
}

// Poller pulls verified versioned snapshots from the control plane on startup, on Redis hints,
// and on a periodic reconciliation interval so missed pub/sub events heal automatically.
type Poller struct {
	cfg    PollerConfig
	holder *table.Holder
	client *http.Client
	logger *slog.Logger

	mu          sync.RWMutex
	lastETag    string
	lastSyncAt  time.Time
	lastErr     string
	syncCount   int64
	rejectCount int64
}

// SyncStatus reports operator-safe synchronization diagnostics.
type SyncStatus struct {
	ActiveVersion int       `json:"active_version"`
	Checksum      string    `json:"checksum"`
	LastSyncAt    time.Time `json:"last_sync_at"`
	LastError     string    `json:"last_error,omitempty"`
	SyncCount     int64     `json:"sync_count"`
	RejectCount   int64     `json:"reject_count"`
}

// NewPoller creates a snapshot Poller bound to holder.
func NewPoller(holder *table.Holder, cfg PollerConfig) *Poller {
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Second
	}
	if cfg.HTTPTimeout <= 0 {
		cfg.HTTPTimeout = 5 * time.Second
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: cfg.HTTPTimeout}
	}
	return &Poller{
		cfg:    cfg,
		holder: holder,
		client: client,
		logger: logger,
	}
}

// Status returns the latest synchronization status without sensitive credentials.
func (p *Poller) Status() SyncStatus {
	tbl := p.holder.Current()
	p.mu.RLock()
	defer p.mu.RUnlock()
	return SyncStatus{
		ActiveVersion: tbl.Version,
		Checksum:      tbl.Checksum,
		LastSyncAt:    p.lastSyncAt,
		LastError:     p.lastErr,
		SyncCount:     p.syncCount,
		RejectCount:   p.rejectCount,
	}
}

// SyncOnce pulls the active snapshot once, verifies its checksum, and atomically applies it to the routing table.
// On any network, decode, checksum, or validation error, the previous last-known-good table remains active.
func (p *Poller) SyncOnce(ctx context.Context) error {
	endpoint := strings.TrimRight(p.cfg.ControlPlaneURL, "/") + "/api/v1/gateway/snapshots/active"
	if p.cfg.OrgID != "" {
		endpoint += "?org_id=" + p.cfg.OrgID
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return p.recordErr(fmt.Errorf("build sync request: %w", err))
	}
	if p.cfg.SyncToken != "" {
		req.Header.Set("Authorization", "Bearer "+p.cfg.SyncToken)
	}
	p.mu.RLock()
	etag := p.lastETag
	p.mu.RUnlock()
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return p.recordErr(fmt.Errorf("fetch active snapshot: %w", err))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotModified {
		p.mu.Lock()
		p.lastSyncAt = time.Now().UTC()
		p.lastErr = ""
		p.syncCount++
		p.mu.Unlock()
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return p.recordErr(fmt.Errorf("snapshot endpoint returned HTTP %d", resp.StatusCode))
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return p.recordErr(fmt.Errorf("read snapshot body: %w", err))
	}

	var snap snapshot.Snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return p.recordReject(fmt.Errorf("decode snapshot JSON: %w", err))
	}
	if _, err := p.holder.Apply(snap, true); err != nil {
		return p.recordReject(fmt.Errorf("apply snapshot: %w", err))
	}

	newETag := strings.TrimSpace(resp.Header.Get("ETag"))
	if newETag == "" && snap.Checksum != "" {
		newETag = `"` + snap.Checksum + `"`
	}
	p.mu.Lock()
	p.lastETag = newETag
	p.lastSyncAt = time.Now().UTC()
	p.lastErr = ""
	p.syncCount++
	p.mu.Unlock()

	p.logger.Info("applied verified gateway snapshot",
		"version", snap.Version,
		"checksum", snap.Checksum,
		"routes", len(snap.Routes),
	)
	return nil
}

// Run performs an initial sync and then loops on periodic reconciliation and optional Redis hints until ctx is canceled.
func (p *Poller) Run(ctx context.Context) {
	if err := p.SyncOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
		p.logger.Warn("initial snapshot sync failed; keeping last-known-good", "error", err)
	}

	ticker := time.NewTicker(p.cfg.Interval)
	defer ticker.Stop()

	var hintCh <-chan events.ConfigChangeHint
	if p.cfg.Hints != nil {
		hintCh = p.cfg.Hints.Triggers()
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := p.SyncOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				p.logger.Warn("periodic snapshot reconciliation failed; keeping last-known-good", "error", err)
			}
		case hint := <-hintCh:
			p.logger.Info("received config-change invalidation hint; pulling snapshot",
				"hint_version", hint.Version,
				"hint_checksum", hint.Checksum,
			)
			if err := p.SyncOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				p.logger.Warn("hint-triggered snapshot sync failed; keeping last-known-good", "error", err)
			}
		}
	}
}

func (p *Poller) recordErr(err error) error {
	p.mu.Lock()
	p.lastErr = err.Error()
	p.mu.Unlock()
	return err
}

func (p *Poller) recordReject(err error) error {
	p.mu.Lock()
	p.lastErr = err.Error()
	p.rejectCount++
	p.mu.Unlock()
	return err
}
