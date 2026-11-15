// Package buildinfo defines compile-time identity. Unconfigured Go builds are Dev.
package buildinfo

import (
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime/debug"
	"strings"
)

// Set only by the build entry point using linker flags, never runtime environment.
var channel = "dev"
var revision = ""

//go:embed profiles.json
var profilesJSON []byte

type Profile struct {
	Name       string `json:"name"`
	Identifier string `json:"identifier"`
	Slug       string `json:"slug"`
	Port       int    `json:"port"`
}
type Identity struct {
	Profile
	Channel string `json:"channel"`
	Version string `json:"version"`
}

var current = resolve()

func Current() Identity { return current }
func resolve() Identity {
	var profiles struct {
		Version     string `json:"version"`
		Stable, Dev Profile
	}
	if err := json.Unmarshal(profilesJSON, &profiles); err != nil {
		panic(err)
	}
	switch channel {
	case "stable":
		return Identity{profiles.Stable, channel, profiles.Version}
	case "dev":
		rev := revision
		if rev == "" {
			if info, ok := debug.ReadBuildInfo(); ok {
				dirty := false
				for _, s := range info.Settings {
					if s.Key == "vcs.revision" {
						rev = s.Value
						if len(rev) > 8 {
							rev = rev[:8]
						}
					}
					if s.Key == "vcs.modified" && s.Value == "true" {
						dirty = true
					}
				}
				if dirty && rev != "" {
					rev += ".dirty"
				}
			}
		}
		if rev == "" {
			rev = "local"
		}
		return Identity{profiles.Dev, channel, profiles.Version + "-dev." + rev}
	default:
		panic("unknown build channel: " + channel)
	}
}
func DefaultAddr() string { return fmt.Sprintf("127.0.0.1:%d", current.Port) }

// DataIdentity avoids advertising a user's filesystem path in health responses.
func DataIdentity(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	if real, e := filepath.EvalSymlinks(absolute); e == nil {
		absolute = real
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(absolute)))
}

// EnvironmentKey keeps production data/endpoint overrides out of Dev launches.
func EnvironmentKey(key string) string {
	if current.Channel == "dev" {
		return "GATEWAY_DEV_" + strings.TrimPrefix(key, "GATEWAY_")
	}
	return key
}
