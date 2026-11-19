package clientplugin

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/gofrs/flock"

	"gatewaylab/internal/clientplugin/github"
)

const releaseRepository = "DengNaichen/mcp-gateway"
const releaseTagPrefix = "plugin-v"
const pluginCheckInterval = 6 * time.Hour

type pluginUpdates struct {
	stateDir string
	channel  string
	platform string
	key      ed25519.PublicKey
	feed     github.Client
}

func newPluginUpdates(stateDir, channel string) *pluginUpdates {
	if stateDir == "" || runtime.GOOS != "darwin" || (runtime.GOARCH != "arm64" && runtime.GOARCH != "amd64") {
		return nil
	}
	key, _ := base64.StdEncoding.DecodeString(releasePublicKey)
	return &pluginUpdates{
		stateDir: filepath.Join(stateDir, "releases"), channel: channel, platform: runtime.GOOS + "-" + runtime.GOARCH, key: key,
		feed: github.Client{
			APIURL: "https://api.github.com/repos/" + releaseRepository,
			Assets: "https://github.com/" + releaseRepository + "/releases/download/",
			HTTP:   &http.Client{Timeout: 2 * time.Minute},
		},
	}
}

type updateState struct {
	LastAttempt time.Time       `json:"last_attempt,omitempty"`
	LastSuccess time.Time       `json:"last_success,omitempty"`
	RetryAt     time.Time       `json:"retry_at,omitempty"`
	Failures    int             `json:"failures,omitempty"`
	Release     json.RawMessage `json:"release,omitempty"`
}

func (u *pluginUpdates) statePath() string { return filepath.Join(u.stateDir, "check.json") }

func (u *pluginUpdates) readState() updateState {
	var state updateState
	if raw, err := readOptionalFile(u.statePath()); err == nil && len(raw) > 0 && json.Unmarshal(raw, &state) == nil {
		return state
	}
	return updateState{}
}

func (u *pluginUpdates) parse(raw []byte) (*releaseManifest, error) {
	return parseRelease(raw, u.key, u.channel, u.platform)
}

func (s updateState) nextCheck() time.Time {
	if !s.RetryAt.IsZero() {
		return s.RetryAt
	}
	return s.LastSuccess.Add(pluginCheckInterval)
}

func (u *pluginUpdates) check(ctx context.Context, force bool) (state updateState, checked bool, err error) {
	err = withPluginLock(ctx, filepath.Join(u.stateDir, ".check.lock"), func() error {
		state = u.readState()
		now := time.Now()
		if !force && now.Before(state.nextCheck()) {
			return nil
		}
		checked = true
		ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		raw, retryAt, checkErr := u.fetchLatest(ctx)
		state.LastAttempt = now
		if checkErr != nil {
			backoff := []time.Duration{15 * time.Minute, time.Hour, pluginCheckInterval}
			if state.Failures < 0 || state.Failures >= len(backoff) {
				state.Failures = len(backoff) - 1
			}
			state.RetryAt = time.Now().Add(backoff[state.Failures])
			if retryAt.After(state.RetryAt) {
				state.RetryAt = retryAt
			}
			state.Failures++
		} else {
			state.LastSuccess = time.Now()
			state.RetryAt = time.Time{}
			state.Failures = 0
			state.Release = raw
		}
		data, err := json.Marshal(state)
		if err != nil {
			return err
		}
		if err := atomicWriteFile(u.statePath(), data, 0o600); err != nil {
			return err
		}
		return checkErr
	})
	return state, checked, err
}

func withPluginLock(ctx context.Context, path string, op func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	lock := flock.New(path)
	defer func() { _ = lock.Close() }()
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ok, err := lock.TryLockContext(ctx, 50*time.Millisecond)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("another process is updating Gateway plugins")
	}
	return op()
}

func (u *pluginUpdates) assetBase() string {
	return "plugin-" + u.channel + "-" + u.platform
}

func (u *pluginUpdates) fetchLatest(ctx context.Context) (json.RawMessage, time.Time, error) {
	releases, retryAt, err := u.feed.Releases(ctx)
	if err != nil {
		return nil, retryAt, err
	}

	// Plugin releases do not replace GitHub's latest-app release.
	var newest string
	for _, release := range releases {
		version, plugin := strings.CutPrefix(release.Tag, releaseTagPrefix)
		if !plugin || release.Draft || release.Prerelease ||
			!pluginVersionPattern.MatchString(version) || (newest != "" && !releaseIsNewer(version, newest)) {
			continue
		}
		for _, asset := range release.Assets {
			if asset.Name == u.assetBase()+".json" {
				newest = version
				break
			}
		}
	}
	if newest == "" {
		return nil, time.Time{}, nil
	}
	raw, retry, err := u.feed.Download(ctx, releaseTagPrefix+newest, u.assetBase()+".json", maxReleaseFile)
	if err != nil {
		return nil, retry, err
	}
	release, err := u.parse(raw)
	if err != nil {
		return nil, time.Time{}, err
	}
	if release.Version != newest {
		return nil, time.Time{}, fmt.Errorf("plugin manifest version does not match GitHub release")
	}
	return raw, time.Time{}, nil
}

func (u *pluginUpdates) cachedManifest(version string) string {
	return filepath.Join(u.stateDir, version, "manifest.json")
}

func (u *pluginUpdates) hookPath(version string) string {
	return filepath.Join(u.stateDir, version, "gateway-hook")
}

func (u *pluginUpdates) cachedRelease(version string) (*releaseManifest, error) {
	if !pluginVersionPattern.MatchString(version) {
		return nil, fmt.Errorf("invalid installed plugin release version")
	}
	raw, err := readOptionalFile(u.cachedManifest(version))
	if err != nil {
		return nil, err
	}
	release, err := u.parse(raw)
	if err != nil {
		return nil, fmt.Errorf("cannot load installed plugin release %s: %w", version, err)
	}
	if release.Version != version {
		return nil, fmt.Errorf("cached plugin manifest version does not match its directory")
	}
	return release, nil
}

func (u *pluginUpdates) stage(ctx context.Context, release *releaseManifest, manifest []byte) error {
	manifestPath := u.cachedManifest(release.Version)
	existing, err := readOptionalFile(manifestPath)
	if err != nil {
		return err
	}
	if len(existing) > 0 && !bytes.Equal(bytes.TrimSpace(existing), bytes.TrimSpace(manifest)) {
		return fmt.Errorf("plugin release %s has changed since it was cached", release.Version)
	}

	hookPath := u.hookPath(release.Version)
	hook, err := readOptionalFile(hookPath)
	if err != nil {
		return err
	}
	if len(hook) != release.HookSize || sha256Hex(hook) != release.HookHash {
		hook, _, err = u.feed.Download(ctx, releaseTagPrefix+release.Version, u.assetBase()+"-hook", release.HookSize)
		if err != nil {
			return err
		}
		if len(hook) != release.HookSize || sha256Hex(hook) != release.HookHash {
			return fmt.Errorf("downloaded Hook does not match the signed release")
		}
		if err := atomicWriteFile(hookPath, hook, 0o700); err != nil {
			return err
		}
	}
	return atomicWriteFile(manifestPath, manifest, 0o600)
}
