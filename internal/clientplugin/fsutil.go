package clientplugin

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type pluginReceipt struct {
	InstallationID string            `json:"installation_id,omitempty"`
	Client         string            `json:"client"`
	Version        string            `json:"version"`
	Digest         string            `json:"digest"`
	Endpoint       string            `json:"endpoint"`
	InstallRoot    string            `json:"install_root"`
	Marketplace    string            `json:"marketplace,omitempty"`
	PackageSource  string            `json:"package_source,omitempty"`
	Files          map[string]string `json:"files,omitempty"`
}

func (m *Manager) receiptPath(id string) string {
	return filepath.Join(m.stateDir, id+".json")
}

func (m *Manager) loadReceipt(id string) (*pluginReceipt, error) {
	b, err := readOptionalFile(m.receiptPath(id))
	if err != nil || len(b) == 0 {
		return nil, err
	}
	var r pluginReceipt
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("插件安装记录损坏: %w", err)
	}
	return &r, nil
}

func (m *Manager) saveReceipt(r pluginReceipt) error {
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(m.receiptPath(r.Client), append(raw, '\n'), 0o600)
}

func (m *Manager) deleteReceipt(id string) error {
	err := os.Remove(m.receiptPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func readOptionalFile(path string) ([]byte, error) {
	b, err := os.ReadFile(path) //nolint:gosec // G304: path is built from user-local config/state directories
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return b, err
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("拒绝写入符号链接路径 %s", path)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("目标路径不是普通文件 %s", path)
		}
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".gw-plugin-*")
	if err != nil {
		return err
	}
	tmpName := f.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := f.Chmod(perm); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func writeRenderedTree(root string, files []renderedFile) (map[string]string, error) {
	if err := os.MkdirAll(root, 0o755); err != nil { //nolint:gosec // G301: client plugin directories need normal user read/exec permissions
		return nil, err
	}
	wanted := make(map[string]renderedFile, len(files))
	hashes := make(map[string]string, len(files))
	for _, f := range files {
		rel := filepath.ToSlash(filepath.Clean(f.RelPath))
		if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			return nil, fmt.Errorf("非法相对路径 %q", f.RelPath)
		}
		wanted[rel] = f
		hashes[rel] = sha256Hex(f.Data)
		target := filepath.Join(root, filepath.FromSlash(rel))
		existing, err := readOptionalFile(target)
		if err == nil && existing != nil && bytes.Equal(existing, f.Data) {
			_ = os.Chmod(target, f.Perm)
			continue
		}
		if err := atomicWriteFile(target, f.Data, f.Perm); err != nil {
			return nil, err
		}
	}

	// Remove stale files previously inside root that are not in wanted.
	var stale []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if _, ok := wanted[relSlash]; !ok {
			stale = append(stale, p)
		}
		return nil
	})
	for _, p := range stale {
		_ = os.Remove(p)
	}
	pruneEmptyDirs(root, root)
	return hashes, nil
}

func verifyTreeMatches(root string, expected map[string]string) bool {
	if len(expected) == 0 {
		return false
	}
	for rel, wantHash := range expected {
		p := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Lstat(p)
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
		raw, err := os.ReadFile(p) //nolint:gosec // G304: verified relative plugin path within managed root
		if err != nil || sha256Hex(raw) != wantHash {
			return false
		}
	}
	return true
}

func verifyOwnedFilesAbsentOrMatching(root string, files []renderedFile, r *pluginReceipt) error {
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("目标插件目录异常: %s", root)
	}
	if r == nil {
		return fmt.Errorf("目录 %s 已存在但无本应用写入记录，未覆盖", root)
	}
	for rel, wantHash := range r.Files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		raw, err := os.ReadFile(p) //nolint:gosec // G304: verified relative plugin path within managed root
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if sha256Hex(raw) != wantHash {
			return fmt.Errorf("插件文件 %s 已被外部修改，未覆盖", p)
		}
	}
	// Ensure no unexpected non-managed files conflict with new writes.
	for _, f := range files {
		rel := filepath.ToSlash(filepath.Clean(f.RelPath))
		if _, owned := r.Files[rel]; owned {
			continue
		}
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
			return fmt.Errorf("目标文件 %s 已存在且不属于本插件，未覆盖", rel)
		}
	}
	return nil
}

func pruneEmptyDirs(dir, stopAt string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			pruneEmptyDirs(filepath.Join(dir, e.Name()), stopAt)
		}
	}
	if dir == stopAt {
		return
	}
	entries, err = os.ReadDir(dir)
	if err == nil && len(entries) == 0 {
		_ = os.Remove(dir)
	}
}

func hashRenderedFiles(files []renderedFile, extra ...string) string {
	sorted := append([]renderedFile(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].RelPath < sorted[j].RelPath })
	h := sha256.New()
	for _, f := range sorted {
		_, _ = h.Write([]byte(f.RelPath))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(f.Data)
		_, _ = h.Write([]byte{0})
	}
	for _, s := range extra {
		_, _ = h.Write([]byte(s))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
