package cliexec

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"gatewaylab/internal/fault"
	"gatewaylab/internal/procutil"
	"gatewaylab/internal/registry"
)

const outputLimit = 512 << 10

func Run(ctx context.Context, dataDir string, s registry.Server, action string, args map[string]any, token string) (any, error) {
	argv, err := RenderGHArgs(action, args)
	if err != nil {
		return nil, err
	}
	if token == "" || strings.ContainsRune(token, 0) {
		return nil, MissingCredential()
	}
	dir, err := makeRunDir(dataDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	binDir, temp := filepath.Join(dir, "bin"), filepath.Join(dir, "tmp")
	config, ghConfig := filepath.Join(dir, "config"), filepath.Join(dir, "gh")
	for _, child := range []string{binDir, temp, config, ghConfig} {
		if err := os.Mkdir(child, 0700); err != nil {
			return nil, fault.New(fault.Internal, "Could not prepare the GitHub CLI working directory", nil)
		}
	}
	resolvedBin := filepath.Join(binDir, "gh")
	snapshot, original, err := snapshotBinary(s, resolvedBin)
	if err != nil {
		return nil, err
	}
	defer func() { _ = snapshot.Close() }()
	for _, path := range []string{binDir, dir} {
		restore, err := sealDirectory(path)
		if err != nil {
			return nil, fault.New(fault.Internal, "Could not seal the GitHub CLI working directory", nil)
		}
		defer restore()
	}

	cmd := exec.CommandContext(ctx, resolvedBin, argv...) //nolint:gosec // Verified private executable and arguments rendered from fixed templates.
	procutil.ConfigureProcess(cmd)
	cmd.Dir = dir
	cmd.Env = []string{
		"HOME=" + dir, "XDG_CONFIG_HOME=" + config, "GH_CONFIG_DIR=" + ghConfig, "TMPDIR=" + temp,
		"PATH=/usr/bin:/bin", "NO_COLOR=1", "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "GH_NO_EXTENSION_UPDATE_NOTIFIER=1",
		"GH_SPINNER_DISABLED=1", "GH_PAGER=", "PAGER=cat", "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		// The configured data directory can sit inside a repository. An explicit
		// absent Git directory prevents discovery of an ancestor's Git config.
		"GIT_DIR=" + filepath.Join(dir, ".no-repository"), "GH_TOKEN=" + token,
	}
	var output boundedOutput
	cmd.Stdout = &output
	// The check narrows the path-based exec window. A separate OS account
	// protects the path from other processes until exec opens it.
	if err := verifySnapshot(resolvedBin, original, s.CommandSHA256); err != nil {
		return nil, err
	}
	// Leave stderr on the null device. Diagnostics can contain credentials.
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return nil, fault.WithCode(fault.New(fault.Timeout, "GitHub CLI execution timed out or was cancelled", nil), "cli_timeout")
	}
	if output.exceeded {
		return nil, fault.WithCode(fault.New(fault.Upstream, "GitHub CLI output exceeded 512 KiB", nil), "cli_output_limit")
	}
	if runErr != nil {
		return nil, fault.WithCode(fault.New(fault.Upstream, "GitHub CLI execution failed", nil), "cli_execution_failed")
	}
	return resultForAction(action, output.buffer.Bytes(), token)
}

func MissingCredential() error {
	return fault.WithCode(fault.New(fault.Invalid, "Set GH_TOKEN in the gateway credential store for this CLI source", nil), "cli_credential_missing")
}

type boundedOutput struct {
	buffer   bytes.Buffer
	exceeded bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	room := outputLimit - b.buffer.Len()
	b.exceeded = b.exceeded || len(p) > room
	_, _ = b.buffer.Write(p[:min(len(p), room)])
	return len(p), nil
}

func resultForAction(action string, output []byte, token string) (any, error) {
	c, _ := findCapability(action)
	redact := secretReplacer(token)
	if c.fields == "" {
		return map[string]any{"output": strings.TrimSpace(redact.Replace(string(output)))}, nil
	}

	// Decode before redaction so that JSON-escaped credential strings are
	// recognized. Only the fields named by the fixed command can be returned.
	fields := strings.Split(c.fields, ",")
	if c.states != nil {
		var rows []map[string]any
		if err := json.Unmarshal(output, &rows); err == nil && rows != nil {
			values := make([]any, len(rows))
			for i, row := range rows {
				values[i] = projectRow(row, fields, redact)
			}
			return values, nil
		}
	} else {
		var row map[string]any
		if err := json.Unmarshal(output, &row); err == nil && row != nil {
			return projectRow(row, fields, redact), nil
		}
	}
	return nil, fault.WithCode(fault.New(fault.Upstream, "GitHub CLI did not return the expected JSON", nil), "cli_output_invalid")
}

func projectRow(row map[string]any, fields []string, redact *strings.Replacer) map[string]any {
	out := make(map[string]any, len(fields))
	for _, field := range fields {
		if value, exists := row[field]; exists {
			out[field] = redactJSON(value, redact)
		}
	}
	return out
}

func redactJSON(value any, redact *strings.Replacer) any {
	switch v := value.(type) {
	case string:
		return redact.Replace(v)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = redactJSON(item, redact)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[redact.Replace(key)] = redactJSON(item, redact)
		}
		return out
	default:
		return value
	}
}

// SecretVariants returns the spellings recognized by CLI output and audit redaction.
func SecretVariants(token string) []string {
	if token == "" {
		return nil
	}
	return []string{token, url.QueryEscape(token), url.PathEscape(token),
		base64.StdEncoding.EncodeToString([]byte(token)), base64.RawStdEncoding.EncodeToString([]byte(token)),
		base64.URLEncoding.EncodeToString([]byte(token)), base64.RawURLEncoding.EncodeToString([]byte(token))}
}

func secretReplacer(token string) *strings.Replacer {
	variants := SecretVariants(token)
	slices.SortFunc(variants, func(a, b string) int { return len(b) - len(a) })
	pairs := make([]string, 0, len(variants)*2)
	for _, value := range variants {
		pairs = append(pairs, value, "[redacted]")
	}
	return strings.NewReplacer(pairs...)
}
