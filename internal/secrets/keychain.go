package secrets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Keychain uses macOS's system security tool. Secret data is sent on stdin in
// interactive mode, never in argv, environment variables or temporary files.
// The account is derived from both the build channel and canonical data path.
func Keychain(channel, dir string) (Backend, error) {
	if runtime.GOOS != "darwin" {
		return nil, errors.New("系统钥匙串仅支持 macOS；无界面部署请使用环境变量凭据")
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil, errors.New("无法确定凭据数据目录")
	}
	if canonical, err := filepath.EvalSymlinks(absolute); err == nil {
		absolute = canonical
	}
	sum := sha256.Sum256([]byte(channel + "\x00" + absolute))
	return &keychain{account: hex.EncodeToString(sum[:]), service: "mcp-gateway.credentials.v1"}, nil
}

type keychain struct{ account, service string }

func runKeychain(args []string, input string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/security", args...) //nolint:gosec // G204: fixed system tool; arguments are internal operations and namespace hashes, secret payload travels only on stdin
	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}
	// stderr may contain command text in interactive mode: never forward it.
	return cmd.Output()
}

func (k *keychain) Load() ([]byte, error) {
	data, err := runKeychain([]string{"find-generic-password", "-a", k.account, "-s", k.service, "-w"}, "")
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 44 {
			return nil, nil
		}
		return nil, errors.New("无法读取系统钥匙串凭据，请解锁登录钥匙串并允许 MCP Gateway 访问")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil {
		return nil, errors.New("系统钥匙串中的凭据格式无效")
	}
	return decoded, nil
}

func (k *keychain) Save(data []byte) error {
	// All interpolated fields contain only fixed text, hexadecimal or base64.
	input := fmt.Sprintf("add-generic-password -U -a %s -s %s -w %s\n", k.account, k.service, base64.StdEncoding.EncodeToString(data))
	if _, err := runKeychain([]string{"-i"}, input); err != nil {
		return errors.New("保存凭据失败，请解锁登录钥匙串并允许 MCP Gateway 访问")
	}
	// security -i can exit successfully after an individual command fails.
	got, err := k.Load()
	if err != nil {
		return err
	}
	if !bytes.Equal(got, data) {
		return errors.New("系统钥匙串未保存凭据，请检查访问权限后重试")
	}
	return nil
}
