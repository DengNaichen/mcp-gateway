package cliexec

import (
	"os"
	"path/filepath"

	"gatewaylab/internal/fault"
)

func makeRunDir(dataDir string) (string, error) {
	path, err := filepath.Abs(dataDir)
	if err == nil {
		path, err = filepath.EvalSymlinks(path)
	}
	if err != nil || dataDir == "" {
		return "", fault.New(fault.Internal, "Could not resolve the gateway data directory", nil)
	}
	file, err := openNoFollow(path)
	if err != nil {
		return "", fault.New(fault.Internal, "Could not open the gateway data directory", nil)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 || !ownedByGateway(info) {
		return "", fault.New(fault.Internal, "GitHub CLI requires a gateway-owned data directory with mode 0700", nil)
	}

	runDir, err := os.MkdirTemp(path, "cliexec-run-")
	if err != nil {
		return "", fault.New(fault.Internal, "Could not create the GitHub CLI working directory", nil)
	}
	return runDir, nil
}

// Keep a directory descriptor for cleanup so restoring permissions cannot
// affect an unrelated directory if the staged path is renamed.
func sealDirectory(path string) (func(), error) {
	dir, err := openNoFollow(path)
	if err != nil {
		return nil, err
	}
	info, err := dir.Stat()
	if err == nil && !info.IsDir() {
		err = os.ErrInvalid
	}
	if err == nil {
		err = dir.Chmod(0500)
	}
	if err != nil {
		_ = dir.Close()
		return nil, err
	}
	return func() {
		_ = dir.Chmod(0700)
		_ = dir.Close()
	}, nil
}
