package buildinfo

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestChannelIdentity(t *testing.T) {
	p := Current()
	if p.Channel == "stable" {
		if p.Name != "MCP Gateway" || p.Identifier != "NaichengDeng.mcp-gateway" || p.Slug != "mcp-gateway" || p.Port != 8788 || strings.Contains(p.Version, "dev") {
			t.Fatalf("changed stable identity: %+v", p)
		}
	} else {
		if p.Name != "MCP Gateway Dev" || p.Identifier != "NaichengDeng.mcp-gateway.dev" || p.Slug != "mcp-gateway-dev" || p.Port != 8789 || !strings.Contains(p.Version, "-dev.") {
			t.Fatalf("bad Dev identity: %+v", p)
		}
	}
	t.Setenv("GATEWAY_BUILD_CHANNEL", "opposite")
	if Current() != p {
		t.Fatal("runtime environment changed compiled identity")
	}
}
func TestDataIdentity(t *testing.T) {
	root := t.TempDir()
	if DataIdentity(root) == DataIdentity(filepath.Join(root, "other")) {
		t.Fatal("data identities collide")
	}
	if DataIdentity(root) != DataIdentity(filepath.Join(root, ".")) {
		t.Fatal("equivalent data paths differ")
	}
}
