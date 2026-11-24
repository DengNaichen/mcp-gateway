package cliexec

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gatewaylab/internal/fault"
	"gatewaylab/internal/registry"
)

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
	sum, err := digestExecutable(path, io.Discard)
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

func openExecutable(path string) (*os.File, os.FileInfo, error) {
	f, err := openNoFollow(path)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		_ = f.Close()
		return nil, nil, os.ErrInvalid
	}
	return f, info, nil
}

func digestExecutable(path string, dst io.Writer) (string, error) {
	file, _, err := openExecutable(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	return digest(file, dst)
}

func digest(file *os.File, dst io.Writer) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(dst, h), file); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
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
	sum, err := digestExecutable(path, io.Discard)
	if err != nil || sum != s.CommandSHA256 {
		return binaryChanged()
	}
	return nil
}

// snapshotBinary hashes the copy and retains a read-only descriptor for its
// inode. Close the write descriptor before launching the binary.
func snapshotBinary(s registry.Server, destination string) (*os.File, os.FileInfo, error) {
	path, err := sourcePath(s)
	if err != nil {
		return nil, nil, err
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0500) //nolint:gosec // Private staging directory and a fixed filename.
	if err != nil {
		return nil, nil, fault.New(fault.Internal, "Could not prepare the GitHub CLI executable", nil)
	}
	defer func() { _ = file.Close() }()
	sum, err := digestExecutable(path, file)
	if err != nil || sum != s.CommandSHA256 {
		return nil, nil, binaryChanged()
	}
	written, err := file.Stat()
	if err != nil || file.Chmod(0500) != nil {
		return nil, nil, fault.New(fault.Internal, "Could not seal the GitHub CLI executable", nil)
	}
	anchor, info, err := openExecutable(destination)
	if err != nil {
		return nil, nil, binaryChanged()
	}
	if !os.SameFile(written, info) {
		_ = anchor.Close()
		return nil, nil, binaryChanged()
	}
	return anchor, written, nil
}

func verifySnapshot(path string, original os.FileInfo, pin string) error {
	file, info, err := openExecutable(path)
	if err != nil {
		return binaryChanged()
	}
	defer func() { _ = file.Close() }()
	// On Unix, SameFile compares device and inode from fstat on both descriptors.
	if !os.SameFile(original, info) || info.Mode().Perm() != 0500 {
		return binaryChanged()
	}
	sum, err := digest(file, io.Discard)
	if err != nil || sum != pin {
		return binaryChanged()
	}
	return nil
}
