package gateway

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed runtime/pi-sandbox.ts
var piSandboxExtension []byte

const defaultSandboxImage = "node@sha256:4b150b48b4116e2a22f2c39cc27025fbf4f153e2078fd3b14d159fdb5c7279da"

type executionPolicy struct {
	Mode  string `json:"mode"`
	Image string `json:"image"`
}

func loadExecutionPolicy() (executionPolicy, error) {
	p := executionPolicy{Mode: os.Getenv("GATEWAY_EXECUTION_MODE"), Image: os.Getenv("GATEWAY_SANDBOX_IMAGE")}
	if p.Mode == "" {
		p.Mode = "docker"
	}
	if p.Mode != "docker" && p.Mode != "local" {
		return p, invalid("GATEWAY_EXECUTION_MODE 必须为 docker 或 local")
	}
	if p.Mode == "local" && os.Getenv("GATEWAY_AUTH_FILE") != "" {
		return p, invalid("共享模式不允许 local 代码执行；请使用 Docker 隔离")
	}
	if p.Image == "" {
		p.Image = defaultSandboxImage
	}
	if strings.HasPrefix(p.Image, "-") || strings.ContainsAny(p.Image, " \t\n") {
		return p, invalid("沙箱镜像名称无效")
	}
	return p, nil
}

func containerName() string { return fmt.Sprintf("gateway-%x", rand.Text()) }
func removeContainer(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := runCommand(ctx, "docker", []string{"rm", "-f", name}, "", nil, nil, 4096); err != nil {
		slog.Warn("remove sandbox container", "container", name, "error", err)
	}
}

func (p executionPolicy) dockerArgs(name, dir string, writable, network bool) ([]string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve sandbox directory: %w", err)
	}
	if strings.ContainsAny(abs, ",\n") {
		return nil, invalid("沙箱路径不能包含逗号或换行")
	}
	destination := "/job"
	if writable {
		destination = "/input"
	}
	mount := "type=bind,src=" + abs + ",dst=" + destination + ",readonly"
	args := []string{"run", "--pull=never", "--name", name, "--label", "gateway.sandbox=true", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--pids-limit=64", "--memory=512m", "--cpus=1", "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), "--tmpfs", "/tmp:rw,nosuid,nodev,size=64m", "--mount", mount, "--workdir", "/job", "--env", "HOME=/tmp", "--env", "NODE_OPTIONS=--max-old-space-size=256"}
	if writable {
		args = append(args, "--tmpfs", "/job:rw,nosuid,nodev,size=128m,mode=1777")
	}
	if network {
		args = append(args, "--add-host=host.docker.internal:host-gateway")
	} else {
		args = append(args, "--network=none")
	}
	return args, nil
}

func (p executionPolicy) runNode(ctx context.Context, dir string, args []string, input []byte, network bool, limit int) ([]byte, error) {
	if p.Mode == "local" {
		return runCommand(ctx, "node", args, dir, input, nil, limit)
	}
	name := containerName()
	dockerArgs, err := p.dockerArgs(name, dir, false, network)
	if err != nil {
		return nil, err
	}
	defer removeContainer(name)
	dockerArgs = append(dockerArgs, "--rm", "-i", p.Image, "node")
	for _, arg := range args {
		if filepath.IsAbs(arg) {
			arg = "/job/" + filepath.Base(arg)
		}
		dockerArgs = append(dockerArgs, arg)
	}
	out, err := runCommand(ctx, "docker", dockerArgs, "", input, nil, limit)
	if err != nil {
		return nil, fmt.Errorf("sandbox execution failed (Docker and the configured image must be installed): %w", err)
	}
	return out, nil
}

func (p executionPolicy) preparePi(ctx context.Context, dir string) ([]string, map[string]string, func() error, func(), error) {
	if p.Mode == "local" {
		return []string{"--tools", "read,write,edit,bash"}, nil, func() error { return nil }, func() {}, nil
	}
	name := containerName()
	args, err := p.dockerArgs(name, dir, true, false)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	ttl := 960
	if deadline, ok := ctx.Deadline(); ok {
		ttl = max(1, int(time.Until(deadline).Seconds())+60)
	}
	args = append(args, "--rm", "-d", p.Image, "sleep", fmt.Sprint(ttl))
	if _, err := runCommand(ctx, "docker", args, "", nil, nil, 4096); err != nil {
		removeContainer(name)
		return nil, nil, nil, nil, fmt.Errorf("start coding sandbox (install Docker and pull the configured image): %w", err)
	}
	if _, err := runCommand(ctx, "docker", []string{"exec", name, "cp", "-R", "/input/.", "/job/"}, "", nil, nil, 4096); err != nil {
		removeContainer(name)
		return nil, nil, nil, nil, fmt.Errorf("copy isolated job inputs: %w", err)
	}
	// Keep the trusted bridge outside the job mount. The coding tools cannot edit it.
	ext, err := os.CreateTemp("", "gateway-pi-sandbox-*.ts")
	if err != nil {
		removeContainer(name)
		return nil, nil, nil, nil, fmt.Errorf("create sandbox bridge: %w", err)
	}
	cleanup := func() {
		removeContainer(name)
		if err := os.Remove(ext.Name()); err != nil {
			slog.Warn("remove sandbox bridge", "error", err)
		}
	}
	_, writeErr := ext.Write(piSandboxExtension)
	closeErr := ext.Close()
	if writeErr != nil || closeErr != nil {
		cleanup()
		return nil, nil, nil, nil, fmt.Errorf("write sandbox bridge: %w", errors.Join(writeErr, closeErr))
	}
	export := func() error {
		// A bounded JSON transfer avoids extracting an untrusted tar archive or
		// resolving agent-created symlinks in the host filesystem.
		program := `const fs=require('node:fs');
if(!fs.lstatSync('/job/output').isDirectory()) throw Error('output must be a directory');
const names=fs.readdirSync('/job/output'); if(names.length>64) throw Error('too many artifacts');
const files={};let total=0;for(const name of names){const p='/job/output/'+name,s=fs.lstatSync(p);if(!s.isFile()||s.size>1048576)throw Error('invalid artifact');total+=s.size;if(total>8388608)throw Error('artifacts too large');files[name]=fs.readFileSync(p).toString('base64');} console.log(JSON.stringify(files));`
		b, err := runCommand(ctx, "docker", []string{"exec", name, "node", "-e", program}, "", nil, nil, 12<<20)
		if err != nil {
			return fmt.Errorf("export sandbox artifacts: %w", err)
		}
		var files map[string][]byte
		if err := json.Unmarshal(b, &files); err != nil {
			return fmt.Errorf("decode sandbox artifacts: %w", err)
		}
		for name, content := range files {
			if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
				return invalid("invalid sandbox artifact name")
			}
			if err := os.WriteFile(filepath.Join(dir, "output", name), content, 0600); err != nil {
				return fmt.Errorf("write exported artifact: %w", err)
			}
		}
		return nil
	}
	return []string{"--no-builtin-tools", "-e", ext.Name()}, map[string]string{"GATEWAY_SANDBOX_CONTAINER": name, "GATEWAY_SANDBOX_JOB": dir}, export, cleanup, nil
}

// Loopback in generated adapters means the gateway host, not the container.
func (p executionPolicy) adapterEndpoint(endpoint string) string {
	if p.Mode == "local" {
		return endpoint
	}
	u, err := url.Parse(endpoint)
	if err != nil || !loopbackHost(u.Hostname()) {
		return endpoint
	}
	port := u.Port()
	u.Host = "host.docker.internal"
	if port != "" {
		u.Host += ":" + port
	}
	return u.String()
}

func piEnvironment(extra map[string]string) []string {
	if extra == nil {
		extra = map[string]string{}
	}
	keys := []string{"PI_CODING_AGENT_DIR", "PI_TELEMETRY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "OPENROUTER_API_KEY", "AZURE_OPENAI_API_KEY"}
	keys = append(keys, strings.Split(os.Getenv("GATEWAY_PI_ENV"), ",")...)
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if secretEnvName.MatchString(key) {
			if value, ok := os.LookupEnv(key); ok {
				extra[key] = value
			}
		}
	}
	return processEnvironment(extra)
}
