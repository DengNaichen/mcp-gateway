package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// limitedOutput keeps draining a child after reaching the capture limit.
type limitedOutput struct {
	bytes.Buffer
	limit    int
	overflow bool
}

func (w *limitedOutput) Write(p []byte) (int, error) {
	n := len(p)
	left := w.limit - w.Len()
	if len(p) > left {
		p = p[:max(0, left)]
		w.overflow = true
	}
	if _, err := w.Buffer.Write(p); err != nil {
		return 0, err
	}
	return n, nil
}

func processEnvironment(extra map[string]string) []string {
	env := []string{}
	for _, key := range []string{"PATH", "HOME", "TMPDIR", "TMP", "TEMP", "LANG", "SYSTEMROOT"} {
		if v, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+v)
		}
	}
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}
func runCommand(ctx context.Context, name string, args []string, dir string, input []byte, env map[string]string, limit int) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = processEnvironment(env)
	cmd.Stdin = bytes.NewReader(input)
	out := &limitedOutput{limit: limit}
	cmd.Stdout = out
	cmd.Stderr = &limitedOutput{limit: 4096}
	configureProcess(cmd)
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("run %s: %w", name, err)
	}
	if out.overflow {
		return nil, fmt.Errorf("process output exceeds %d bytes", limit)
	}
	return out.Bytes(), nil
}
func runPi(ctx context.Context, policy executionPolicy, command, dir string, s MaintenanceSource, progress func(string) error) error {
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	args := []string{"--mode", "rpc", "--session-dir", filepath.Join(dir, "sessions"), "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes", "--no-context-files", "--no-mcp", "--no-approve", "--offline"}
	sandboxArgs, sandboxEnv, export, cleanup, err := policy.preparePi(ctx, dir)
	if err != nil {
		return err
	}
	defer cleanup()
	args = append(args, sandboxArgs...)
	if s.Model != "" {
		args = append(args, "--model", s.Model)
	}
	if s.Provider != "" {
		args = append(args, "--provider", s.Provider)
	}
	cmd := exec.CommandContext(childCtx, command, args...)
	cmd.Dir = dir
	// Pi owns its local provider configuration and credentials. Do not put them in
	// argv, the job's JSON artifacts, or event logs.
	cmd.Env = piEnvironment(sandboxEnv)
	cmd.Stderr = &limitedOutput{limit: 8192}
	configureProcess(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open pi output: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("open pi input: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start pi (install pi 1.4+ or set GATEWAY_PI_COMMAND): %w", err)
	}
	prompt := map[string]any{"id": "maintain", "type": "prompt", "message": "Read CONTRACT.md and input.json. Maintain this server's complete tool catalog. Write output/manifest.json and any adapters/tests required by the contract. Use previous/ implementations when present. Complete the work now."}
	sendErr := json.NewEncoder(stdin).Encode(prompt)
	settled := false
	accepted := false
	assistantOK := false
	var protocolErr error
	if sendErr != nil {
		protocolErr = fmt.Errorf("send pi task: %w", sendErr)
		cancel()
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	var shutdownTimer *time.Timer
	for scanner.Scan() {
		if protocolErr != nil {
			continue
		}
		var event struct {
			Type     string `json:"type"`
			ID       string `json:"id"`
			Success  bool   `json:"success"`
			Aborted  bool   `json:"aborted"`
			ToolName string `json:"toolName"`
			Data     struct {
				Disposition string `json:"disposition"`
			} `json:"data"`
			Message struct {
				Role       string `json:"role"`
				StopReason string `json:"stopReason"`
			} `json:"message"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			protocolErr = fmt.Errorf("invalid pi protocol: %w", err)
			cancel()
			continue
		}
		switch event.Type {
		case "response":
			if event.ID == "maintain" {
				if !event.Success || event.Data.Disposition == "handled" {
					protocolErr = fmt.Errorf("pi did not start the maintenance task; check pi model/login configuration")
					cancel()
				} else {
					accepted = true
				}
			}
		case "message_end":
			if event.Message.Role == "assistant" {
				assistantOK = event.Message.StopReason != "error" && event.Message.StopReason != "aborted" && event.Message.StopReason != "pending"
			}
		case "tool_execution_start":
			// Store bounded operation names, never tool arguments, output, or reasoning.
			name := event.ToolName
			if len(name) > 80 {
				name = name[:80]
			}
			if err := progress("Pi 执行工具：" + name); err != nil {
				protocolErr = err
				cancel()
			}
		case "extension_ui_request":
			protocolErr = fmt.Errorf("pi requested interactive input in an unattended task")
			cancel()
		case "agent_settled":
			settled = true
			if event.Aborted || !assistantOK {
				protocolErr = fmt.Errorf("pi run ended without a successful assistant response; check pi model/login configuration")
			}
			if err := stdin.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
				protocolErr = fmt.Errorf("close pi input: %w", err)
			}
			// A malformed worker must not hang after announcing completion.
			shutdownTimer = time.AfterFunc(5*time.Second, cancel)
		}
	}
	if err := scanner.Err(); err != nil {
		protocolErr = fmt.Errorf("read pi events: %w", err)
		cancel()
	}
	waitErr := cmd.Wait()
	if shutdownTimer != nil {
		shutdownTimer.Stop()
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if protocolErr != nil {
		return protocolErr
	}
	if waitErr != nil {
		return fmt.Errorf("pi exited unsuccessfully: %w", waitErr)
	}
	if !accepted || !settled {
		return fmt.Errorf("pi exited before accepting and completing the task")
	}
	return export()
}
func (g *Gateway) adapterPath(t Tool) (string, error) {
	if !validID(t.Revision) || t.Adapter == "" || filepath.Base(t.Adapter) != t.Adapter || !strings.HasSuffix(t.Adapter, ".mjs") {
		return "", invalid("适配器版本或路径无效")
	}
	dir, err := filepath.Abs(filepath.Join(g.dir, "maintenance", t.Revision, "revision"))
	if err != nil {
		return "", fmt.Errorf("resolve adapter: %w", err)
	}
	b, err := regularArtifact(dir, t.Adapter)
	if err != nil {
		return "", err
	}
	g.maintenance.mu.Lock()
	run, ok := g.maintenance.runs[t.Revision]
	want := run.Artifacts[t.Adapter]
	g.maintenance.mu.Unlock()
	if !ok || run.ServerID != t.ServerID || want == "" || maintenanceDigest(string(b)) != want {
		return "", invalid("适配器文件与已验证版本不一致")
	}
	return filepath.Join(dir, t.Adapter), nil
}
func (g *Gateway) callAdapter(ctx context.Context, s Server, t Tool, args map[string]any) (any, error) {
	if err := checkEnvironment(s); err != nil {
		return nil, err
	}
	p, err := g.adapterPath(t)
	if err != nil {
		return nil, err
	}
	return callAdapterFile(ctx, g.execution, s, t, args, p)
}
func callAdapterFile(ctx context.Context, policy executionPolicy, s Server, t Tool, args map[string]any, p string) (any, error) {
	headers := map[string]string{}
	env := map[string]string{}
	for k, v := range s.Headers {
		headers[k] = os.ExpandEnv(v)
	}
	for k, v := range s.Env {
		env[k] = os.ExpandEnv(v)
	}
	input, err := json.Marshal(map[string]any{"tool": t.Name, "arguments": args, "endpoint": policy.adapterEndpoint(s.Endpoint), "headers": headers, "env": env})
	if err != nil {
		return nil, fmt.Errorf("encode adapter input: %w", err)
	}
	b, err := policy.runNode(ctx, filepath.Dir(p), []string{p}, input, true, 8<<20)
	if err != nil {
		return nil, err
	}
	var out any
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("decode adapter result: %w", err)
	}
	if err := ensureJSONEnd(dec); err != nil {
		return nil, err
	}
	return out, nil
}

// limitedOutput must keep draining the pipe even when its capture buffer is full.
var _ io.Writer = (*limitedOutput)(nil)
