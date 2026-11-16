package sandbox

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gatewaylab/internal/procutil"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"gatewaylab/internal/registry"
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

func RunCommand(ctx context.Context, name string, args []string, dir string, input []byte, env map[string]string, limit int) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = procutil.ProcessEnvironment(env)
	cmd.Stdin = bytes.NewReader(input)
	out := &limitedOutput{limit: limit}
	cmd.Stdout = out
	cmd.Stderr = &limitedOutput{limit: 4096}
	procutil.ConfigureProcess(cmd)
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
func RunPi(ctx context.Context, policy Policy, command, dir string, s PiOptions, progress func(string) error) error {
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
	procutil.ConfigureProcess(cmd)
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
func CallAdapterFile(ctx context.Context, policy Policy, s registry.Server, t registry.Tool, args map[string]any, p string) (any, error) {
	headers := map[string]string{}
	env := map[string]string{}
	for k, v := range s.Headers {
		headers[k] = os.ExpandEnv(v)
	}
	for k, v := range s.Env {
		env[k] = os.ExpandEnv(v)
	}
	input, err := json.Marshal(map[string]any{"tool": t.Name, "arguments": args, "endpoint": policy.AdapterEndpoint(s.Endpoint), "headers": headers, "env": env})
	if err != nil {
		return nil, fmt.Errorf("encode adapter input: %w", err)
	}
	b, err := policy.RunNode(ctx, filepath.Dir(p), []string{p}, input, true, 8<<20)
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

type PiOptions struct {
	Provider string
	Model    string
}

func ensureJSONEnd(dec *json.Decoder) error {
	var more any
	if err := dec.Decode(&more); !errors.Is(err, io.EOF) {
		return invalid("产物必须是单个 JSON 对象")
	}
	return nil
}
