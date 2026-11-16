package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type Kind string

const (
	KindGit Kind = "git"
	KindDir Kind = "dir"
)

const (
	identifyCacheTTL        = 30 * time.Second
	maxIdentifyCacheEntries = 512
	gitProbeTimeout         = 3 * time.Second
	gitOutputLimit          = 64 << 10
)

type Info struct {
	Kind        Kind   `json:"kind"`
	Root        string `json:"root"`
	CommonDir   string `json:"common_dir"`
	RemoteHint  string `json:"remote_hint"`
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
}

type cacheEntry struct {
	info      Info
	expiresAt time.Time
}

var (
	identifyCacheMu sync.Mutex
	identifyCache   = make(map[string]cacheEntry)
	identifyNow     = time.Now
)

// ClearCache clears the workspace identification TTL cache.
func ClearCache() {
	identifyCacheMu.Lock()
	clear(identifyCache)
	identifyCacheMu.Unlock()
}

// Identify resolves symlinks for cwd and probes Git safely without locks or hooks,
// executing --git-common-dir and --show-toplevel separately so linked worktrees,
// subdirectories, empty git-init repositories, and bare repositories all resolve
// to a canonical CommonDir. Non-Git directories return Kind="dir".
func Identify(ctx context.Context, cwd string) (Info, error) {
	trimmed := strings.TrimSpace(cwd)
	if trimmed == "" || strings.ContainsRune(trimmed, '\x00') {
		return Info{}, errors.New("workspace cwd is empty or invalid")
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return Info{}, fmt.Errorf("resolve absolute cwd: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return Info{}, fmt.Errorf("evaluate cwd symlinks: %w", err)
	}
	resolved = filepath.Clean(resolved)
	stat, err := os.Stat(resolved)
	if err != nil {
		return Info{}, fmt.Errorf("stat cwd: %w", err)
	}
	if !stat.IsDir() {
		return Info{}, errors.New("workspace cwd is not a directory")
	}

	now := identifyNow()
	identifyCacheMu.Lock()
	if cached, ok := identifyCache[resolved]; ok && now.Before(cached.expiresAt) {
		identifyCacheMu.Unlock()
		return cached.info, nil
	}
	identifyCacheMu.Unlock()

	info := probeWorkspace(ctx, resolved)

	identifyCacheMu.Lock()
	evictIdentifyCacheLocked(now)
	identifyCache[resolved] = cacheEntry{
		info:      info,
		expiresAt: now.Add(identifyCacheTTL),
	}
	identifyCacheMu.Unlock()

	return info, nil
}

func evictIdentifyCacheLocked(now time.Time) {
	if len(identifyCache) < maxIdentifyCacheEntries {
		return
	}
	for k, v := range identifyCache {
		if !now.Before(v.expiresAt) {
			delete(identifyCache, k)
		}
	}
	for len(identifyCache) >= maxIdentifyCacheEntries {
		var oldestKey string
		var oldestExp time.Time
		for k, v := range identifyCache {
			if oldestKey == "" || v.expiresAt.Before(oldestExp) {
				oldestKey = k
				oldestExp = v.expiresAt
			}
		}
		if oldestKey == "" {
			break
		}
		delete(identifyCache, oldestKey)
	}
}

func probeWorkspace(ctx context.Context, resolvedCwd string) Info {
	commonDir, ok := probeGitCommonDir(ctx, resolvedCwd)
	if !ok {
		return Info{
			Kind:        KindDir,
			Root:        resolvedCwd,
			Name:        friendlyDirName(resolvedCwd),
			Fingerprint: makeFingerprint(KindDir, resolvedCwd),
		}
	}

	// Execute --show-toplevel separately so bare repos (and .git subdirs) succeed
	// even when there is no working tree.
	var root string
	if topRaw, topOK := runSafeGit(ctx, resolvedCwd, "rev-parse", "--show-toplevel"); topOK && topRaw != "" {
		if !filepath.IsAbs(topRaw) {
			topRaw = filepath.Join(resolvedCwd, topRaw)
		}
		if evalTop, err := filepath.EvalSymlinks(topRaw); err == nil {
			root = filepath.Clean(evalTop)
		} else {
			root = filepath.Clean(topRaw)
		}
	}
	if root == "" {
		// Bare repo or inside .git directory.
		isBare, bareOK := runSafeGit(ctx, commonDir, "rev-parse", "--is-bare-repository")
		if (!bareOK || isBare != "true") && filepath.Base(commonDir) == ".git" {
			root = filepath.Dir(commonDir)
		} else {
			root = commonDir
		}
	}

	remoteTarget := root
	if _, err := os.Stat(remoteTarget); err != nil {
		remoteTarget = commonDir
	}
	remoteHint := probeRemoteHint(ctx, remoteTarget, commonDir)

	return Info{
		Kind:        KindGit,
		Root:        root,
		CommonDir:   commonDir,
		RemoteHint:  remoteHint,
		Name:        friendlyGitName(commonDir, root),
		Fingerprint: makeFingerprint(KindGit, commonDir),
	}
}

func probeGitCommonDir(ctx context.Context, cwd string) (string, bool) {
	raw, ok := runSafeGit(ctx, cwd, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if !ok || raw == "" {
		// Fallback for older Git versions without --path-format=absolute.
		raw, ok = runSafeGit(ctx, cwd, "rev-parse", "--git-common-dir")
		if !ok || raw == "" {
			return "", false
		}
	}
	if !filepath.IsAbs(raw) {
		raw = filepath.Join(cwd, raw)
	}
	resolved, err := filepath.EvalSymlinks(raw)
	if err != nil {
		return "", false
	}
	cleaned := filepath.Clean(resolved)
	if !filepath.IsAbs(cleaned) {
		return "", false
	}
	return cleaned, true
}

func probeRemoteHint(ctx context.Context, workRoot, commonDir string) string {
	target := workRoot
	if target == "" {
		target = commonDir
	}
	remoteName := ""
	if branch, ok := runSafeGit(ctx, target, "symbolic-ref", "--quiet", "--short", "HEAD"); ok && branch != "" {
		if r, ok := runSafeGit(ctx, target, "config", "--get", "branch."+branch+".pushRemote"); ok && r != "" {
			remoteName = r
		} else if r, ok := runSafeGit(ctx, target, "config", "--get", "remote.pushDefault"); ok && r != "" {
			remoteName = r
		} else if r, ok := runSafeGit(ctx, target, "config", "--get", "branch."+branch+".remote"); ok && r != "" {
			remoteName = r
		}
	}
	if remoteName == "" {
		if r, ok := runSafeGit(ctx, target, "config", "--get", "remote.pushDefault"); ok && r != "" {
			remoteName = r
		} else {
			remoteName = "origin"
		}
	}
	rawURL, ok := runSafeGit(ctx, target, "remote", "get-url", "--", remoteName)
	if !ok || rawURL == "" {
		if remoteName != "origin" {
			rawURL, ok = runSafeGit(ctx, target, "remote", "get-url", "--", "origin")
		}
		if !ok || rawURL == "" {
			return ""
		}
	}
	return NormalizeRemote(rawURL)
}

func runSafeGit(parent context.Context, dir string, gitArgs ...string) (string, bool) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, gitProbeTimeout)
	defer cancel()

	args := make([]string, 0, len(gitArgs)+8)
	args = append(args,
		"--no-pager",
		"--no-replace-objects",
		"-c", "core.hooksPath=/dev/null",
		"-c", "core.fsmonitor=false",
		"-C", dir,
	)
	args = append(args, gitArgs...)

	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // G204: fixed git binary with internally built arguments
	cmd.WaitDelay = 100 * time.Millisecond
	cmd.Env = cleanGitEnv(os.Environ())

	var stdout, stderr limitedBuffer
	stdout.limit = gitOutputLimit
	stderr.limit = gitOutputLimit
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil || stdout.exceeded || stderr.exceeded {
		return "", false
	}
	out := stdout.Bytes()
	if !utf8.Valid(out) {
		return "", false
	}
	val := strings.TrimSuffix(string(out), "\n")
	val = strings.TrimSuffix(val, "\r")
	return strings.TrimSpace(val), true
}

func cleanGitEnv(environ []string) []string {
	cleaned := make([]string, 0, len(environ)+3)
	for _, entry := range environ {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "GIT_") && key != "GIT_EXEC_PATH" {
			// Scrub GIT_DIR, GIT_WORK_TREE, GIT_COMMON_DIR, GIT_INDEX_FILE, etc.
			continue
		}
		cleaned = append(cleaned, entry)
	}
	return append(cleaned,
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
	)
}

type limitedBuffer struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		b.exceeded = true
		return 0, errors.New("git output limit exceeded")
	}
	return b.Buffer.Write(p)
}

// NormalizeRemote normalizes a Git remote URL into a canonical display/hint form:
// lowercase host, preserved explicit port, and preserved repository path case.
// Local filesystem remotes return "". RemoteHint is strictly informational and
// must NEVER be used to auto-authorize a repository.
func NormalizeRemote(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if idx := strings.IndexAny(s, "?#"); idx >= 0 {
		s = s[:idx]
	}
	if strings.HasPrefix(s, "/") || strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") {
		return ""
	}

	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return ""
		}
		scheme := strings.ToLower(u.Scheme)
		if scheme == "file" || u.Host == "" {
			return ""
		}
		host := strings.ToLower(u.Hostname())
		if host == "" {
			return ""
		}
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		port := u.Port()
		authority := host
		if port != "" {
			authority = host + ":" + port
		}
		path := cleanRemotePath(u.Path)
		if path == "" {
			return ""
		}
		return authority + "/" + path
	}

	// SCP-like SSH syntax: [user@]host:path (colon before first slash).
	colon := strings.IndexByte(s, ':')
	slash := strings.IndexByte(s, '/')
	if colon > 0 && (slash < 0 || colon < slash) {
		authority := s[:colon]
		pathPart := s[colon+1:]
		if at := strings.LastIndexByte(authority, '@'); at >= 0 {
			authority = authority[at+1:]
		}
		host := strings.ToLower(strings.TrimSpace(authority))
		path := cleanRemotePath(pathPart)
		if host == "" || path == "" {
			return ""
		}
		return host + "/" + path
	}

	return ""
}

func cleanRemotePath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimLeft(p, "/")
	p = strings.TrimRight(p, "/")
	p = strings.TrimSuffix(p, ".git")
	p = strings.TrimRight(p, "/")
	if p == "" || strings.Contains(p, "..") {
		return ""
	}
	return p
}

// HasPathPrefix reports whether path is equal to prefix or a descendant under
// prefix on a strict path-segment boundary (so "/repo-old" does NOT match "/repo").
func HasPathPrefix(path, prefix string) bool {
	if path == "" || prefix == "" {
		return false
	}
	cleanPath := filepath.Clean(path)
	cleanPrefix := filepath.Clean(prefix)
	if !filepath.IsAbs(cleanPath) || !filepath.IsAbs(cleanPrefix) {
		return false
	}
	if cleanPrefix == string(filepath.Separator) {
		return true
	}
	if cleanPath == cleanPrefix {
		return true
	}
	return strings.HasPrefix(cleanPath, cleanPrefix+string(filepath.Separator))
}

// ValidateDirAnchor rejects overly broad directory anchors such as "/", "$HOME",
// "/tmp", "/private/tmp", and "/Users".
func ValidateDirAnchor(dir string) error {
	trimmed := strings.TrimSpace(dir)
	if trimmed == "" || strings.ContainsRune(trimmed, '\x00') {
		return errors.New("目录路径不能为空")
	}
	if !filepath.IsAbs(trimmed) {
		return errors.New("目录锚点必须是绝对路径")
	}
	cleaned := filepath.Clean(trimmed)
	candidates := []string{cleaned}
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		resolved = filepath.Clean(resolved)
		if resolved != cleaned {
			candidates = append(candidates, resolved)
		}
	}

	forbidden := map[string]bool{
		"/":                          true,
		"/tmp":                       true,
		"/private":                   true,
		"/private/tmp":               true,
		"/var":                       true,
		"/private/var":               true,
		"/Users":                     true,
		"/System/Volumes/Data/Users": true,
		"/home":                      true,
		"/etc":                       true,
		"/private/etc":               true,
		"/System":                    true,
		"/Applications":              true,
		"/Library":                   true,
	}

	for _, h := range homeCandidates() {
		forbidden[h] = true
	}

	for _, c := range candidates {
		if forbidden[c] {
			return fmt.Errorf("拒绝绑定过于宽泛的顶层目录: %s", cleaned)
		}
	}
	return nil
}

func homeCandidates() []string {
	var out []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || !filepath.IsAbs(p) {
			return
		}
		clean := filepath.Clean(p)
		out = append(out, clean)
		if eval, err := filepath.EvalSymlinks(clean); err == nil {
			out = append(out, filepath.Clean(eval))
		}
	}
	if envHome := os.Getenv("HOME"); envHome != "" {
		add(envHome)
	}
	if userHome, err := os.UserHomeDir(); err == nil {
		add(userHome)
	}
	return out
}

func friendlyGitName(commonDir, root string) string {
	if filepath.Base(commonDir) == ".git" {
		parent := filepath.Base(filepath.Dir(commonDir))
		if parent != "" && parent != "." && parent != string(filepath.Separator) {
			return parent
		}
	}
	base := strings.TrimSuffix(filepath.Base(commonDir), ".git")
	if base != "" && base != "." && base != string(filepath.Separator) {
		return base
	}
	return friendlyDirName(root)
}

func friendlyDirName(dir string) string {
	base := filepath.Base(filepath.Clean(dir))
	if base == "" || base == "." || base == string(filepath.Separator) {
		return dir
	}
	return base
}

func makeFingerprint(kind Kind, anchor string) string {
	sum := sha256.Sum256([]byte(string(kind) + ":" + anchor))
	return string(kind) + ":" + hex.EncodeToString(sum[:8])
}
