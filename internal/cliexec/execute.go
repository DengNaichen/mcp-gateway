package cliexec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
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

// Pin resolves the configured executable at registration time. A supplied pin
// (either the saved pin or an explicit administrator update) must match.
func Pin(s registry.Server, previousPin string) (registry.Server, error) {
	if s.Command == "" || len(s.Args) != 0 || len(s.Headers) != 0 || s.Endpoint != "" || s.Transport != "" || s.OAuth ||
		len(s.Env) != 1 || s.Env["GH_TOKEN"] != "${GH_TOKEN}" {
		return s, fault.New(fault.Invalid, "CLI sources require a command and only the GH_TOKEN credential reference", nil)
	}
	path := s.Command
	if !filepath.IsAbs(path) {
		var err error
		path, err = exec.LookPath(path)
		if err != nil {
			return s, fault.New(fault.Invalid, "GitHub CLI executable could not be resolved", nil)
		}
	}
	path, err := filepath.EvalSymlinks(path)
	if err != nil || !filepath.IsAbs(path) {
		return s, fault.New(fault.Invalid, "GitHub CLI executable could not be resolved", nil)
	}
	sum, err := digest(path, io.Discard)
	if err != nil {
		return s, fault.New(fault.Invalid, "GitHub CLI executable could not be read or is not executable", nil)
	}
	pin := strings.ToLower(s.CommandSHA256)
	if pin == "" {
		pin = previousPin
	}
	if pin != "" && sum != pin {
		return s, binaryChanged()
	}
	s.Command, s.CommandSHA256 = path, sum
	return s, nil
}

func binaryChanged() error {
	return fault.WithCode(fault.New(fault.Conflict, "The pinned GitHub CLI executable is missing or has changed", nil), "cli_binary_changed")
}

func digest(path string, dst io.Writer) (string, error) {
	if info, err := os.Stat(path); err != nil || !executable(info) {
		return "", os.ErrInvalid
	}
	f, err := os.Open(path) //nolint:gosec // Administrator-pinned executable, revalidated before any execution.
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	if info, err := f.Stat(); err != nil || !executable(info) {
		return "", os.ErrInvalid
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(dst, h), f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func executable(info os.FileInfo) bool {
	return info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0
}

func sourcePath(s registry.Server) (string, error) {
	if !filepath.IsAbs(s.Command) || len(s.CommandSHA256) != sha256.Size*2 {
		return "", binaryChanged()
	}
	path, err := filepath.EvalSymlinks(s.Command)
	if err != nil || path != s.Command {
		return "", binaryChanged()
	}
	return path, nil
}

// VerifyBinary checks the current pin without starting the executable.
func VerifyBinary(s registry.Server) error {
	path, err := sourcePath(s)
	if err != nil {
		return err
	}
	sum, err := digest(path, io.Discard)
	if err != nil || sum != s.CommandSHA256 {
		return binaryChanged()
	}
	return nil
}

// snapshotBinary hashes the bytes written to a private executable, so replacing
// the configured file between hashing and exec cannot run unverified bytes.
func snapshotBinary(s registry.Server, dir string) (string, error) {
	path, err := sourcePath(s)
	if err != nil {
		return "", err
	}
	resolvedBin := filepath.Join(dir, "bin", "gh")
	file, err := os.OpenFile(resolvedBin, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0500) //nolint:gosec // Private temporary directory, fixed filename.
	if err != nil {
		return "", fault.New(fault.Internal, "Could not prepare the GitHub CLI executable", nil)
	}
	sum, copyErr := digest(path, file)
	closeErr := file.Close()
	if copyErr != nil || sum != s.CommandSHA256 {
		return "", binaryChanged()
	}
	if closeErr != nil {
		return "", fault.New(fault.Internal, "Could not prepare the GitHub CLI executable", nil)
	}
	return resolvedBin, nil
}

func Run(ctx context.Context, s registry.Server, action string, args map[string]any, token string) (any, error) {
	argv, err := RenderGHArgs(action, args)
	if err != nil {
		return nil, err
	}
	if token == "" || strings.ContainsRune(token, 0) {
		return nil, MissingCredential()
	}
	dir, err := os.MkdirTemp("", "gateway-gh-")
	if err != nil {
		return nil, fault.New(fault.Internal, "Could not create the GitHub CLI working directory", nil)
	}
	defer func(path string) { _ = os.RemoveAll(path) }(dir)
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, fault.New(fault.Internal, "Could not resolve the GitHub CLI working directory", nil)
	}
	temp := filepath.Join(dir, "tmp")
	for _, child := range []string{temp, filepath.Join(dir, "bin")} {
		if err := os.Mkdir(child, 0700); err != nil {
			return nil, fault.New(fault.Internal, "Could not create the GitHub CLI temporary directory", nil)
		}
	}
	resolvedBin, err := snapshotBinary(s, dir)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, resolvedBin, argv...) //nolint:gosec // Verified private executable and arguments rendered from fixed templates.
	procutil.ConfigureProcess(cmd)
	cmd.Dir = dir
	cmd.Env = []string{
		"HOME=" + dir, "XDG_CONFIG_HOME=" + filepath.Join(dir, "config"), "GH_CONFIG_DIR=" + filepath.Join(dir, "gh"), "TMPDIR=" + temp,
		"PATH=/usr/bin:/bin", "NO_COLOR=1", "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "GH_NO_EXTENSION_UPDATE_NOTIFIER=1",
		"GH_SPINNER_DISABLED=1", "GH_PAGER=", "PAGER=cat", "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GH_TOKEN=" + token,
	}
	var output boundedOutput
	cmd.Stdout = &output
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
		switch v := row[field].(type) {
		case string:
			out[field] = redact.Replace(v)
		case float64, bool:
			out[field] = v
		}
	}
	return out
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
