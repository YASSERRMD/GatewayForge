package buildinfo

import (
	"os/exec"
	"strings"
	"testing"
)

func TestForService(t *testing.T) {
	info := ForService("control-plane")
	if info.Service != "control-plane" {
		t.Fatalf("expected service control-plane, got %q", info.Service)
	}
	if info.Version == "" {
		t.Fatal("expected non-empty version")
	}
	if !strings.Contains(info.String(), "control-plane") {
		t.Fatalf("expected String() to contain service name, got %q", info.String())
	}
}

func TestPackageBoundaries(t *testing.T) {
	cmd := exec.Command("go", "list", "-f", "{{.ImportPath}}|{{join .Imports \",\"}}", "github.com/YASSERRMD/GatewayForge/...")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list failed: %v\n%s", err, string(out))
	}

	const root = "github.com/YASSERRMD/GatewayForge/"
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		pkg := parts[0]
		imports := ""
		if len(parts) == 2 {
			imports = parts[1]
		}
		switch {
		case strings.HasPrefix(pkg, root+"internal/shared"):
			if strings.Contains(imports, root+"internal/control") || strings.Contains(imports, root+"internal/gateway") {
				t.Errorf("shared package %s must not import control or gateway: %s", pkg, imports)
			}
		case strings.HasPrefix(pkg, root+"internal/control") || strings.HasPrefix(pkg, root+"cmd/control-plane"):
			if strings.Contains(imports, root+"internal/gateway") {
				t.Errorf("control package %s must not import internal/gateway: %s", pkg, imports)
			}
		case strings.HasPrefix(pkg, root+"internal/gateway") || strings.HasPrefix(pkg, root+"cmd/gateway"):
			if strings.Contains(imports, root+"internal/control") {
				t.Errorf("gateway package %s must not import internal/control: %s", pkg, imports)
			}
		}
	}
}
