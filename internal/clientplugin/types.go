// Official coding-agent plugin types and display names across Codex CLI,
// Claude Code, Cursor, OpenCode v2, Pi, and Antigravity.
package clientplugin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gatewaylab/internal/buildinfo"
	"gatewaylab/internal/clientconfig"
	"gatewaylab/internal/i18n"
)

const cliCommandTimeout = 45 * time.Second

// ClientID constants identify the six supported coding-agent hosts.
const (
	ClientCodex       = clientconfig.ClientCodex
	ClientClaude      = clientconfig.ClientClaude
	ClientCursor      = clientconfig.ClientCursor
	ClientOpenCode    = clientconfig.ClientOpenCode
	ClientPi          = clientconfig.ClientPi
	ClientAntigravity = clientconfig.ClientAntigravity
)

// CommandRunner abstracts external CLI invocations (claude, codex, pi) so unit
// tests can verify installation state machines deterministically.
type CommandRunner interface {
	LookPath(binary string) (string, error)
	Run(ctx context.Context, env []string, dir, binary string, args ...string) (stdout, stderr []byte, err error)
}

// Options configures a clientplugin.Manager.
type Options struct {
	HomeDir       string
	StateDir      string
	DataDir       string
	BinPath       string
	Getenv        func(string) string
	Runner        CommandRunner
	ClientConfigs *clientconfig.Manager
}

// Status describes the official plugin state for one coding-agent client.
type Status struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Path             string   `json:"path"`
	State            string   `json:"state"`
	Detail           string   `json:"detail,omitempty"`
	InstalledVersion string   `json:"installed_version,omitempty"`
	TargetVersion    string   `json:"target_version,omitempty"`
	CLIAvailable     bool     `json:"cli_available"`
	HasLegacyMCP     bool     `json:"has_legacy_mcp"`
	CanInstall       bool     `json:"can_install"`
	CanUpdate        bool     `json:"can_update"`
	CanRemove        bool     `json:"can_remove"`
	Warnings         []string `json:"warnings,omitempty"`
}

// Manager installs, inspects, updates, and removes official client plugins.
type Manager struct {
	homeDir       string
	stateDir      string
	dataDir       string
	binPath       string
	getenv        func(string) string
	runner        CommandRunner
	clientConfigs *clientconfig.Manager
	slug          string
	channel       string
	version       string
	appName       string
	mu            sync.Mutex
}

// New constructs a Manager rooted at the given home and state directories.
func New(opts Options) *Manager {
	info := buildinfo.Current()
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	home := strings.TrimSpace(opts.HomeDir)
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = h
		}
	}
	stateDir := strings.TrimSpace(opts.StateDir)
	if stateDir == "" && home != "" {
		stateDir = filepath.Join(home, "."+info.Slug, "plugins")
	}
	runner := opts.Runner
	if runner == nil {
		runner = execRunner{homeDir: home, getenv: getenv}
	}
	cc := opts.ClientConfigs
	if cc == nil && home != "" {
		cc = clientconfig.New(home, filepath.Join(home, "."+info.Slug, "client-config"), getenv)
	}
	return &Manager{
		homeDir:       home,
		stateDir:      stateDir,
		dataDir:       strings.TrimSpace(opts.DataDir),
		binPath:       strings.TrimSpace(opts.BinPath),
		getenv:        getenv,
		runner:        runner,
		clientConfigs: cc,
		slug:          info.Slug,
		channel:       info.Channel,
		version:       info.Version,
		appName:       info.Name,
	}
}

type execRunner struct {
	homeDir string
	getenv  func(string) string
}

func (r execRunner) LookPath(binary string) (string, error) {
	if p, err := exec.LookPath(binary); err == nil {
		return p, nil
	}
	searchDirs := []string{
		"/opt/homebrew/bin",
		"/usr/local/bin",
	}
	if r.homeDir != "" {
		searchDirs = append([]string{
			filepath.Join(r.homeDir, ".local", "bin"),
			filepath.Join(r.homeDir, ".bun", "bin"),
			filepath.Join(r.homeDir, ".volta", "bin"),
		}, searchDirs...)
	}
	for _, dir := range searchDirs {
		cand := filepath.Join(dir, binary)
		if info, err := os.Stat(cand); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			return cand, nil
		}
	}
	return "", exec.ErrNotFound
}

func (r execRunner) Run(ctx context.Context, env []string, dir, binary string, args ...string) ([]byte, []byte, error) {
	resolved, err := r.LookPath(binary)
	if err != nil {
		return nil, nil, fmt.Errorf(i18n.T("client.err-command-not-found"), binary, err)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithTimeout(ctx, cliCommandTimeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, resolved, args...) //nolint:gosec // G204: resolved CLI path and internal arguments for official client plugins
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = mergeEnv(os.Environ(), env)
	cmd.Stdin = nil
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg != "" {
			return stdout.Bytes(), stderr.Bytes(), fmt.Errorf(i18n.T("client.err-command-failed-detail"), binary, strings.Join(args, " "), msg, err)
		}
		return stdout.Bytes(), stderr.Bytes(), fmt.Errorf(i18n.T("client.err-command-failed"), binary, strings.Join(args, " "), err)
	}
	return stdout.Bytes(), stderr.Bytes(), nil
}

func mergeEnv(base, overrides []string) []string {
	if len(overrides) == 0 {
		return base
	}
	idx := make(map[string]int, len(base))
	out := append([]string(nil), base...)
	for i, kv := range out {
		if k, _, ok := strings.Cut(kv, "="); ok {
			idx[k] = i
		}
	}
	for _, kv := range overrides {
		k, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if pos, exists := idx[k]; exists {
			out[pos] = kv
		} else {
			idx[k] = len(out)
			out = append(out, kv)
		}
	}
	return out
}

func (m *Manager) hostConfigDir(id string) string {
	return clientconfig.HostConfigDir(id, m.homeDir, m.getenv)
}

var errClientUnknown = errors.New(i18n.T("client.err-unknown-client"))
