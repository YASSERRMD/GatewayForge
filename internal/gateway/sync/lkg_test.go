package syncer

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/YASSERRMD/GatewayForge/internal/gateway/table"
)

func TestPoisonedSnapshotRejectedAndLKGRestartRecovery(t *testing.T) {
	lkgPath := filepath.Join(t.TempDir(), "lkg-snapshot.json")
	var mode atomic.Int32 // 0=valid v1, 1=tampered checksum, 2=malformed JSON, 3=503 outage

	cp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode.Load() {
		case 0:
			v1 := makeSignedSnap(1, "/healthy")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(v1)
		case 1:
			bad := makeSignedSnap(2, "/poisoned")
			bad.Checksum = "0000000000000000000000000000000000000000000000000000000000000000"
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(bad)
		case 2:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{corrupt-json"))
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer cp.Close()

	silent := slog.New(slog.NewJSONHandler(io.Discard, nil))
	holder := table.NewHolder()
	poller := NewPoller(holder, PollerConfig{
		ControlPlaneURL: cp.URL,
		SyncToken:       "tok",
		LKGPath:         lkgPath,
		Logger:          silent,
	})

	// 1. Apply valid v1 and persist LKG.
	if err := poller.SyncOnce(context.Background()); err != nil {
		t.Fatalf("initial valid sync failed: %v", err)
	}
	if holder.Current().Version != 1 || !holder.Current().Matcher.Match("api.local", "GET", "/healthy").Matched {
		t.Fatalf("expected v1 active")
	}

	// 2. Poisoned checksum snapshot MUST be rejected; v1 MUST stay active; diagnostics populated.
	mode.Store(1)
	if err := poller.SyncOnce(context.Background()); err == nil {
		t.Fatal("expected error for tampered checksum snapshot")
	}
	if holder.Current().Version != 1 || !holder.Current().Matcher.Match("api.local", "GET", "/healthy").Matched {
		t.Fatal("last-known-good v1 was overwritten by poisoned snapshot!")
	}
	if st := poller.Status(); st.RejectCount != 1 || st.LastError == "" {
		t.Fatalf("expected reject_count=1 and diagnostic last_error, got %+v", st)
	}

	// 3. Malformed JSON snapshot MUST also be rejected while v1 keeps serving.
	mode.Store(2)
	if err := poller.SyncOnce(context.Background()); err == nil {
		t.Fatal("expected error for malformed JSON snapshot")
	}
	if holder.Current().Version != 1 || poller.Status().RejectCount != 2 {
		t.Fatalf("expected v1 retained and reject_count=2, got %+v", poller.Status())
	}

	// 4. Simulate gateway restart while control plane is DOWN (HTTP 503):
	// new gateway instance bootstraps from verified LKG file on disk!
	mode.Store(3)
	restartedHolder := table.NewHolder()
	restartedPoller := NewPoller(restartedHolder, PollerConfig{
		ControlPlaneURL: cp.URL,
		SyncToken:       "tok",
		LKGPath:         lkgPath,
		Logger:          silent,
	})
	if err := restartedPoller.BootstrapFromLKG(); err != nil {
		t.Fatalf("BootstrapFromLKG failed: %v", err)
	}
	_ = restartedPoller.SyncOnce(context.Background()) // CP returns 503
	if restartedHolder.Current().Version != 1 || !restartedHolder.Current().Matcher.Match("api.local", "GET", "/healthy").Matched {
		t.Fatalf("expected restarted gateway to serve v1 from LKG during CP outage")
	}

	// 5. Tampered LKG file on disk MUST be rejected cleanly.
	_ = os.WriteFile(lkgPath, []byte(`{"schema_version":1,"version":99,"checksum":"bad"}`), 0o600)
	if _, err := LoadLKG(lkgPath); err == nil {
		t.Fatal("expected tampered LKG file to fail checksum verification")
	}
}
