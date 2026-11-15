package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	appDataDirName  = "mcp-gateway"
	pathBeginMarker = "__MCP_GW_PATH_BEGIN__"
	pathEndMarker   = "__MCP_GW_PATH_END__"
)

// filterLaunchArgs removes macOS LaunchServices process serial number arguments
// (-psn_0_...) injected when an .app bundle is launched from Finder.
func filterLaunchArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		if strings.HasPrefix(arg, "-psn_") {
			continue
		}
		out = append(out, arg)
	}
	return out
}

// resolveDataDir determines the persistent directory for SQLite and maintenance
// artifacts. When launched from Finder, cwd is often "/", so relative defaults
// are unsafe; we default to <UserConfigDir>/mcp-gateway unless overridden.
func resolveDataDir(explicit string) (string, error) {
	if trimmed := strings.TrimSpace(explicit); trimmed != "" {
		return filepath.Abs(trimmed)
	}
	if fromEnv := strings.TrimSpace(os.Getenv("GATEWAY_DATA_DIR")); fromEnv != "" {
		return filepath.Abs(fromEnv)
	}
	if configDir, err := os.UserConfigDir(); err == nil && configDir != "" {
		return filepath.Join(configDir, appDataDirName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(home, "."+appDataDirName), nil
}

// enrichProcessEnvironment ensures HOME, LANG, and PATH are populated for GUI
// .app launches on macOS/Linux where launchd provides a minimal environment
// (/usr/bin:/bin:/usr/sbin:/sbin), allowing stdio MCP servers (npx, uvx, node,
// python3, Homebrew tools, Docker) to resolve commands reliably.
func enrichProcessEnvironment() {
	home, _ := os.UserHomeDir()
	if os.Getenv("HOME") == "" && home != "" {
		_ = os.Setenv("HOME", home)
	}
	if runtime.GOOS != "windows" && os.Getenv("LANG") == "" && os.Getenv("LC_ALL") == "" {
		_ = os.Setenv("LANG", "en_US.UTF-8")
	}
	if runtime.GOOS == "windows" {
		return
	}
	shellPath := readLoginShellPATH(1500 * time.Millisecond)
	merged := mergePATH(os.Getenv("PATH"), shellPath, strings.Join(commonUnixBinDirs(home), string(os.PathListSeparator)))
	if merged != "" {
		_ = os.Setenv("PATH", merged)
	}
}

func commonUnixBinDirs(home string) []string {
	dirs := []string{
		"/opt/homebrew/bin",
		"/opt/homebrew/sbin",
		"/usr/local/bin",
		"/usr/local/sbin",
		"/usr/local/go/bin",
	}
	if home != "" {
		dirs = append(dirs,
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, ".bun", "bin"),
			filepath.Join(home, ".cargo", "bin"),
			filepath.Join(home, ".volta", "bin"),
			filepath.Join(home, ".local", "share", "mise", "shims"),
			filepath.Join(home, ".asdf", "shims"),
			filepath.Join(home, ".orbstack", "bin"),
		)
	}
	if runtime.GOOS == "darwin" {
		dirs = append(dirs, "/Applications/Docker.app/Contents/Resources/bin")
	}
	dirs = append(dirs, "/usr/bin", "/bin", "/usr/sbin", "/sbin")
	return dirs
}

// mergePATH concatenates PATH-formatted lists while preserving order and
// removing empty or duplicate directory entries.
func mergePATH(parts ...string) string {
	seen := make(map[string]struct{}, 24)
	out := make([]string, 0, 24)
	for _, list := range parts {
		for _, entry := range filepath.SplitList(list) {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			cleaned := filepath.Clean(entry)
			if _, ok := seen[cleaned]; ok {
				continue
			}
			seen[cleaned] = struct{}{}
			out = append(out, entry)
		}
	}
	return strings.Join(out, string(os.PathListSeparator))
}

func readLoginShellPATH(timeout time.Duration) string {
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	if shell == "" {
		if runtime.GOOS == "darwin" {
			shell = "/bin/zsh"
		} else {
			shell = "/bin/sh"
		}
	}
	probeCmd := fmt.Sprintf("printf '%s%%s%s' \"$PATH\"", pathBeginMarker, pathEndMarker)
	for _, flags := range [][]string{{"-ilc", probeCmd}, {"-lc", probeCmd}} {
		if p := runShellProbe(shell, flags, timeout); p != "" {
			return p
		}
	}
	return ""
}

func runShellProbe(shell string, args []string, timeout time.Duration) string {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, args...)
	cmd.Env = append(os.Environ(), "TERM=dumb")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil && stdout.Len() == 0 {
		return ""
	}
	return extractMarkedValue(stdout.String(), pathBeginMarker, pathEndMarker)
}

func extractMarkedValue(raw, startMarker, endMarker string) string {
	start := strings.LastIndex(raw, startMarker)
	if start < 0 {
		return ""
	}
	rest := raw[start+len(startMarker):]
	end := strings.Index(rest, endMarker)
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}
