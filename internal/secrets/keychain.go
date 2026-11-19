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
	"strconv"
	"strings"
	"time"
)

const (
	keychainServiceName = "mcp-gateway.credentials.v1"
	// macOS `/usr/bin/security -i` truncates interactive stdin lines at 4096 bytes.
	// Keep every chunk well under that limit so command prefix + payload never truncates.
	keychainMaxChunkEncodedLen = 2048
	keychainChunkHeaderPrefix  = "chunks:"
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
	return &keychain{
		account: hex.EncodeToString(sum[:]),
		service: keychainServiceName,
		run:     runKeychain,
	}, nil
}

type keychain struct {
	account string
	service string
	run     func(args []string, input string) ([]byte, error)
}

func (k *keychain) exec(args []string, input string) ([]byte, error) {
	if k.run != nil {
		return k.run(args, input)
	}
	return runKeychain(args, input)
}

func (k *keychain) chunkAccount(index int) string {
	return fmt.Sprintf("%s.%d", k.account, index)
}

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

var errKeychainItemNotFound = errors.New("keychain item not found")

func isKeychainNotFound(err error) bool {
	if errors.Is(err, errKeychainItemNotFound) {
		return true
	}
	var exit *exec.ExitError
	return errors.As(err, &exit) && exit.ExitCode() == 44
}

func (k *keychain) loadItem(account string) (string, bool, error) {
	data, err := k.exec([]string{"find-generic-password", "-a", account, "-s", k.service, "-w"}, "")
	if err != nil {
		if isKeychainNotFound(err) {
			return "", false, nil
		}
		return "", false, &Error{Message: "无法读取系统钥匙串凭据，请解锁登录钥匙串并允许 MCP Gateway 访问"}
	}
	return strings.TrimSpace(string(data)), true, nil
}

func (k *keychain) deleteChunkRange(start int) {
	for i := start; ; i++ {
		if _, err := k.exec([]string{"delete-generic-password", "-a", k.chunkAccount(i), "-s", k.service}, ""); err != nil {
			break
		}
	}
}

func parseChunkManifest(raw string) (count int, checksum string, ok bool) {
	if !strings.HasPrefix(raw, keychainChunkHeaderPrefix) {
		return 0, "", false
	}
	rest := strings.TrimPrefix(raw, keychainChunkHeaderPrefix)
	parts := strings.SplitN(rest, ":", 2)
	if len(parts) != 2 {
		return 0, "", false
	}
	n, err := strconv.Atoi(parts[0])
	if err != nil || n <= 0 || len(parts[1]) != 64 {
		return 0, "", false
	}
	return n, parts[1], true
}

func (k *keychain) Load() ([]byte, error) {
	raw, found, err := k.loadItem(k.account)
	if err != nil || !found {
		return nil, err
	}
	if strings.HasPrefix(raw, keychainChunkHeaderPrefix) {
		count, expectedSum, ok := parseChunkManifest(raw)
		if !ok {
			return nil, &Error{Message: "系统钥匙串中的凭据索引已损坏，请在「钥匙串访问」中删除对应 mcp-gateway.credentials.v1 条目后重启"}
		}
		var joined strings.Builder
		for i := range count {
			chunk, chunkFound, chunkErr := k.loadItem(k.chunkAccount(i))
			if chunkErr != nil {
				return nil, chunkErr
			}
			if !chunkFound {
				return nil, &Error{Message: "系统钥匙串中的凭据分片缺失，请在「钥匙串访问」中删除对应 mcp-gateway.credentials.v1 条目后重启"}
			}
			joined.WriteString(chunk)
		}
		decoded, err := base64.StdEncoding.DecodeString(joined.String())
		if err != nil {
			return nil, &Error{Message: "系统钥匙串中的凭据格式已损坏，请在「钥匙串访问」中删除对应 mcp-gateway.credentials.v1 条目后重启"}
		}
		sum := sha256.Sum256(decoded)
		if hex.EncodeToString(sum[:]) != expectedSum {
			return nil, &Error{Message: "系统钥匙串中的凭据校验失败，请在「钥匙串访问」中删除对应 mcp-gateway.credentials.v1 条目后重启"}
		}
		return decoded, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, &Error{Message: "系统钥匙串中的凭据格式已损坏（可能是旧版本超长写入被截断），请在「钥匙串访问」中删除对应 mcp-gateway.credentials.v1 条目后重启"}
	}
	return decoded, nil
}

func (k *keychain) Save(data []byte) error {
	encoded := base64.StdEncoding.EncodeToString(data)
	// All interpolated fields contain only fixed text, hexadecimal, colons or base64.
	var batch strings.Builder
	chunkCount := 0
	if len(encoded) <= keychainMaxChunkEncodedLen {
		fmt.Fprintf(&batch, "add-generic-password -U -a %s -s %s -w %s\n", k.account, k.service, encoded)
	} else {
		for offset := 0; offset < len(encoded); offset += keychainMaxChunkEncodedLen {
			end := min(offset+keychainMaxChunkEncodedLen, len(encoded))
			fmt.Fprintf(&batch, "add-generic-password -U -a %s -s %s -w %s\n", k.chunkAccount(chunkCount), k.service, encoded[offset:end])
			chunkCount++
		}
		sum := sha256.Sum256(data)
		manifest := fmt.Sprintf("%s%d:%s", keychainChunkHeaderPrefix, chunkCount, hex.EncodeToString(sum[:]))
		fmt.Fprintf(&batch, "add-generic-password -U -a %s -s %s -w %s\n", k.account, k.service, manifest)
	}
	if _, err := k.exec([]string{"-i"}, batch.String()); err != nil {
		return &Error{Message: "保存凭据失败，请解锁登录钥匙串并允许 MCP Gateway 访问"}
	}
	// security -i can exit successfully after an individual command fails.
	got, err := k.Load()
	if err != nil {
		return err
	}
	if !bytes.Equal(got, data) {
		return &Error{Message: "系统钥匙串未保存凭据，请检查访问权限后重试"}
	}
	k.deleteChunkRange(chunkCount)
	return nil
}
