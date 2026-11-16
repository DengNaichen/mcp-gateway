package maintenance

import (
	"fmt"
	"path/filepath"
	"strings"

	"gatewaylab/internal/jsonutil"
)

func (m *Maintainer) AdapterPath(t Tool) (string, error) {
	if !validID(t.Revision) || t.Adapter == "" || filepath.Base(t.Adapter) != t.Adapter || !strings.HasSuffix(t.Adapter, ".mjs") {
		return "", invalid("适配器版本或路径无效")
	}
	dir, err := filepath.Abs(filepath.Join(m.dir, "maintenance", t.Revision, "revision"))
	if err != nil {
		return "", fmt.Errorf("resolve adapter: %w", err)
	}
	b, err := regularArtifact(dir, t.Adapter)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	run, ok := m.runs[t.Revision]
	want := run.Artifacts[t.Adapter]
	m.mu.Unlock()
	if !ok || run.ServerID != t.ServerID || want == "" || jsonutil.MustDigest(string(b)) != want {
		return "", invalid("适配器文件与已验证版本不一致")
	}
	return filepath.Join(dir, t.Adapter), nil
}
