package integration

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/mockupstream"
)

func TestMockUpstreamComposeSmoke(t *testing.T) {
	if os.Getenv("INTEGRATION_TESTS") != "1" {
		t.Skip("set INTEGRATION_TESTS=1 to run mock-upstream Compose smoke test")
	}
	baseURL := os.Getenv("MOCK_UPSTREAM_URL")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:18089"
	}

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/echo/ping?from=smoke", strings.NewReader("hello-upstream"))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("X-Smoke-ID", "phase01")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("call mock-upstream: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var out mockupstream.EchoResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode echo: %v", err)
	}
	if out.Body != "hello-upstream" || out.RawQuery != "from=smoke" {
		t.Fatalf("unexpected echo payload: %+v", out)
	}
}
