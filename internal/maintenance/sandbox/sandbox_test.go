package sandbox

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestDockerExecutionIsolation(t *testing.T) {
	if os.Getenv("GATEWAY_TEST_DOCKER") != "1" {
		t.Skip("set GATEWAY_TEST_DOCKER=1 to exercise Docker isolation")
	}
	p := Policy{Mode: "docker", Image: DefaultImage}
	dir := t.TempDir()
	hostSecret := filepath.Join(t.TempDir(), "host-only.txt")
	if err := os.WriteFile(hostSecret, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GATEWAY_TEST_PRIVATE_SECRET", "private")
	program := `const fs=require('node:fs');
if(process.env.GATEWAY_TEST_PRIVATE_SECRET) throw Error('host environment leaked');
if(fs.existsSync(process.argv[1])) throw Error('host file leaked');
try { fs.writeFileSync('/job/changed','bad'); throw Error('writable revision'); } catch(e) { if(e.code!=='EROFS'&&e.code!=='EACCES') throw e; }
fetch('https://example.com',{signal:AbortSignal.timeout(1500)}).then(()=>process.exit(4)).catch(()=>console.log('isolated'));`
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Pass host path via a JSON string inside code; positional absolute args are remapped.
	program = strings.ReplaceAll(program, "process.argv[1]", string(mustJSON(t, hostSecret)))
	out, err := p.RunNode(ctx, dir, []string{"-e", program}, nil, false, 4096)
	if err != nil || strings.TrimSpace(string(out)) != "isolated" {
		t.Fatalf("isolation: %s %v", out, err)
	}
}
func TestSandboxPolicyRejectsSharedLocal(t *testing.T) {
	t.Setenv("GATEWAY_EXECUTION_MODE", "local")
	t.Setenv("GATEWAY_AUTH_FILE", "configured")
	if _, err := LoadPolicy(); err == nil {
		t.Fatal("shared mode permitted host execution")
	}
}
