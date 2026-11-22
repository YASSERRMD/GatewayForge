package proxy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/YASSERRMD/GatewayForge/internal/gateway/table"
	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

func TestReverseProxyStreamingLargeBodies(t *testing.T) {
	// Upstream echoes the SHA-256 of streamed upload and streams back a 2 MiB payload.
	const payloadSize = 2 << 20
	expectedResp := bytes.Repeat([]byte("GWF-STREAM-CHUNK"), payloadSize/16)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := sha256.New()
		n, err := io.Copy(h, r.Body)
		if err != nil {
			t.Errorf("upstream read body: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("X-Upload-SHA256", hex.EncodeToString(h.Sum(nil)))
		w.Header().Set("X-Upload-Bytes", string(rune('0'))+r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/octet-stream")
		if n == 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		// Stream response in 32 KiB chunks.
		for off := 0; off < len(expectedResp); off += 32768 {
			end := off + 32768
			if end > len(expectedResp) {
				end = len(expectedResp)
			}
			_, _ = w.Write(expectedResp[off:end])
			if fl, ok := w.(http.Flusher); ok {
				fl.Flush()
			}
		}
	}))
	defer upstream.Close()

	holder := table.NewHolder()
	snap := snapshot.Snapshot{
		SchemaVersion: snapshot.SchemaVersion,
		OrgID:         "org-stream",
		Version:       1,
		Pools: []snapshot.UpstreamPool{
			{
				ID:   "pool-stream",
				Name: "stream",
				Targets: []snapshot.UpstreamTarget{
					{ID: "t1", URL: upstream.URL, Weight: 100, Enabled: true},
				},
			},
		},
		Routes: []snapshot.Route{
			{
				ID:             "r-stream",
				OrgID:          "org-stream",
				UpstreamPoolID: "pool-stream",
				Host:           "stream.local",
				PathPrefix:     "/upload",
				Methods:        []string{"POST"},
				Priority:       100,
				Enabled:        true,
			},
		},
	}
	if _, err := holder.Apply(snap, false); err != nil {
		t.Fatalf("apply snapshot: %v", err)
	}

	pol := DefaultDestinationPolicy(true)
	pol.AllowLoopback = true
	gwServer := httptest.NewServer(NewEngine(holder, nil, pol))
	defer gwServer.Close()

	uploadPayload := bytes.Repeat([]byte("0123456789abcdef"), payloadSize/16)
	wantUploadSum := sha256.Sum256(uploadPayload)

	req, err := http.NewRequest(http.MethodPost, gwServer.URL+"/upload/big?mode=stream", bytes.NewReader(uploadPayload))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Host = "stream.local"

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("proxy request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 preserved from upstream, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Upload-SHA256"); got != hex.EncodeToString(wantUploadSum[:]) {
		t.Fatalf("upload stream corrupted: got sha %q want %q", got, hex.EncodeToString(wantUploadSum[:]))
	}

	gotBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read streamed response: %v", err)
	}
	if !bytes.Equal(gotBody, expectedResp) {
		t.Fatalf("response stream mismatch: got %d bytes want %d", len(gotBody), len(expectedResp))
	}
}
