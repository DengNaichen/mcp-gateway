package clientplugin

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"gatewaylab/internal/buildinfo"
	"gatewaylab/internal/clientconfig"
)

// Plugin releases share the app's publisher identity. The signed message has a
// distinct prefix from the app updater's signed archive digest.
const releasePublicKey = "p72p1f7TV6sw5nqnf1Sx3eZAXWUGdbWWz12TXqNpGp0="
const releaseSignaturePrefix = "mcp-gateway/plugin-release/v1\n"
const maxReleaseFile = 1 << 20
const maxReleaseHook = 32 << 20

var pluginVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)
var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type releaseManifest struct {
	Version   string                    `json:"version"`
	Channel   string                    `json:"channel"`
	Platform  string                    `json:"platform"`
	HookHash  string                    `json:"hook_sha256"`
	HookSize  int                       `json:"hook_size"`
	Templates map[string][]renderedFile `json:"templates"`
}

type signedRelease struct {
	Manifest  json.RawMessage `json:"manifest"`
	Signature []byte          `json:"signature"`
}

func releaseMessage(raw []byte) []byte {
	return append([]byte(releaseSignaturePrefix), raw...)
}

func parseRelease(raw []byte, key ed25519.PublicKey, channel, platform string) (*releaseManifest, error) {
	var signed signedRelease
	if err := json.Unmarshal(raw, &signed); err != nil {
		return nil, fmt.Errorf("invalid plugin release file: %w", err)
	}
	if !ed25519.Verify(key, releaseMessage(signed.Manifest), signed.Signature) {
		return nil, errors.New("invalid plugin release signature")
	}
	var release releaseManifest
	if err := json.Unmarshal(signed.Manifest, &release); err != nil {
		return nil, fmt.Errorf("invalid plugin release manifest: %w", err)
	}
	if release.Channel != channel || release.Platform != platform ||
		!pluginVersionPattern.MatchString(release.Version) || !sha256Pattern.MatchString(release.HookHash) ||
		release.HookSize <= 0 || release.HookSize > maxReleaseHook {
		return nil, errors.New("invalid plugin release version, channel, platform or Hook metadata")
	}
	for _, id := range clientconfig.ClientIDs() {
		files := release.Templates[id]
		if len(files) == 0 {
			return nil, fmt.Errorf("missing plugin template for %s", id)
		}
		seen := make(map[string]bool, len(files))
		for _, file := range files {
			if file.RelPath == "." || !fs.ValidPath(file.RelPath) || strings.Contains(file.RelPath, `\`) ||
				seen[file.RelPath] || (file.Perm != 0o644 && file.Perm != 0o755) {
				return nil, fmt.Errorf("invalid file in %s plugin template", id)
			}
			seen[file.RelPath] = true
		}
	}
	return &release, nil
}

// releaseIsNewer compares release tags against installed versions, which can
// carry a dev revision in the app's bundled copy.
func releaseIsNewer(candidate, installed string) bool {
	if !pluginVersionPattern.MatchString(candidate) {
		return false
	}
	base, _, prerelease := strings.Cut(installed, "-")
	if !pluginVersionPattern.MatchString(base) {
		return false
	}
	current := strings.Split(base, ".")
	for i, n := range strings.Split(candidate, ".") {
		if len(n) != len(current[i]) {
			return len(n) > len(current[i])
		}
		if n != current[i] {
			return n > current[i]
		}
	}
	return prerelease
}

// BuildRelease signs the templates and digest of the corresponding platform
// Hook. This entry point is used only by the plugin release workflow.
func BuildRelease(version, platform string, hook []byte, privateKey string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(privateKey))
	if err != nil || len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid plugin release private key")
	}
	private := ed25519.PrivateKey(key)
	public := private.Public().(ed25519.PublicKey)
	if base64.StdEncoding.EncodeToString(public) != releasePublicKey {
		return nil, errors.New("plugin signing key does not match the publisher's key")
	}
	return buildRelease(version, platform, hook, private)
}

func buildRelease(version, platform string, hook []byte, key ed25519.PrivateKey) ([]byte, error) {
	info := buildinfo.Current()
	m := &Manager{version: version, slug: info.Slug, appName: info.Name}
	templates, err := m.releaseTemplates()
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(releaseManifest{
		Version: version, Channel: info.Channel, Platform: platform,
		HookHash: sha256Hex(hook), HookSize: len(hook), Templates: templates,
	})
	if err != nil {
		return nil, err
	}
	signed, err := json.Marshal(signedRelease{
		Manifest: raw, Signature: ed25519.Sign(key, releaseMessage(raw)),
	})
	if err != nil {
		return nil, err
	}
	if len(signed) > maxReleaseFile {
		return nil, errors.New("plugin release manifest exceeds download limit")
	}
	if _, err := parseRelease(signed, key.Public().(ed25519.PublicKey), info.Channel, platform); err != nil {
		return nil, err
	}
	return signed, nil
}
