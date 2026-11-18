package sandbox

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
	"time"

	"gatewaylab/internal/input"
	"gatewaylab/internal/procutil"
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

type CommandOptions struct {
	Name  string
	Args  []string
	Dir   string
	Input []byte
	Env   map[string]string
	Limit int
}

func RunCommand(ctx context.Context, opts CommandOptions) ([]byte, error) {
	cmd := exec.CommandContext(ctx, opts.Name, opts.Args...) //nolint:gosec // G204: command and arguments are fixed or come from the user's own local configuration; launching them is this component's purpose
	cmd.Dir = opts.Dir
	cmd.Env = procutil.ProcessEnvironment(opts.Env)
	cmd.Stdin = bytes.NewReader(opts.Input)
	out := &limitedOutput{limit: opts.Limit}
	cmd.Stdout = out
	cmd.Stderr = &limitedOutput{limit: 4096}
	procutil.ConfigureProcess(cmd)
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("run %s: %w", opts.Name, err)
	}
	if out.overflow {
		return nil, fmt.Errorf("process output exceeds %d bytes", opts.Limit)
	}
	return out.Bytes(), nil
}

type PiRunOptions struct {
	Policy   Policy
	Command  string
	Dir      string
	Model    PiOptions
	Progress func(string) error
}

type piSession struct {
	cmd     *exec.Cmd
	stdout  io.ReadCloser
	stdin   io.WriteCloser
	export  func() error
	cleanup func()
}

type piEventState struct {
	settled       bool
	accepted      bool
	assistantOK   bool
	protocolErr   error
	shutdownTimer *time.Timer
}

type piProtocolEvent struct {
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

func RunPi(ctx context.Context, opts PiRunOptions) error {
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	sess, err := startPiSession(ctx, childCtx, opts)
	if err != nil {
		return err
	}
	defer sess.cleanup()

	state := &piEventState{}
	prompt := map[string]any{"id": "maintain", "type": "prompt", "message": "Read CONTRACT.md and input.json. Maintain this server's complete tool catalog. Write output/manifest.json and any adapters/tests required by the contract. Use previous/ implementations when present. Complete the work now."}
	if sendErr := json.NewEncoder(sess.stdin).Encode(prompt); sendErr != nil {
		state.protocolErr = fmt.Errorf("send pi task: %w", sendErr)
		cancel()
	}

	consumePiEvents(sess.stdout, sess.stdin, opts.Progress, state, cancel)
	waitErr := sess.cmd.Wait()
	if state.shutdownTimer != nil {
		state.shutdownTimer.Stop()
	}
	if err := checkPiOutcome(ctx, state, waitErr); err != nil {
		return err
	}
	return sess.export()
}

func startPiSession(ctx, childCtx context.Context, opts PiRunOptions) (*piSession, error) {
	args := []string{"--mode", "rpc", "--session-dir", filepath.Join(opts.Dir, "sessions"), "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes", "--no-context-files", "--no-mcp", "--no-approve", "--offline"}
	sandboxArgs, sandboxEnv, export, cleanup, err := opts.Policy.preparePi(ctx, opts.Dir)
	if err != nil {
		return nil, err
	}
	args = append(args, sandboxArgs...)
	if opts.Model.Model != "" {
		args = append(args, "--model", opts.Model.Model)
	}
	if opts.Model.Provider != "" {
		args = append(args, "--provider", opts.Model.Provider)
	}
	cmd := exec.CommandContext(childCtx, opts.Command, args...) //nolint:gosec // G204: command and arguments are fixed or come from the user's own local configuration; launching them is this component's purpose
	cmd.Dir = opts.Dir
	// Pi owns its local provider configuration and credentials. Do not put them in
	// argv, the job's JSON artifacts, or event logs.
	cmd.Env = piEnvironment(sandboxEnv)
	cmd.Stderr = &limitedOutput{limit: 8192}
	procutil.ConfigureProcess(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("open pi output: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("open pi input: %w", err)
	}
	if err := cmd.Start(); err != nil {
		cleanup()
		return nil, fmt.Errorf("start pi (install pi 1.4+ or set GATEWAY_PI_COMMAND): %w", err)
	}
	return &piSession{cmd: cmd, stdout: stdout, stdin: stdin, export: export, cleanup: cleanup}, nil
}

func consumePiEvents(stdout io.Reader, stdin io.Closer, progress func(string) error, state *piEventState, cancel context.CancelFunc) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for scanner.Scan() {
		if state.protocolErr != nil {
			continue
		}
		var event piProtocolEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			state.protocolErr = fmt.Errorf("invalid pi protocol: %w", err)
			cancel()
			continue
		}
		handlePiEvent(event, stdin, progress, state, cancel)
	}
	if err := scanner.Err(); err != nil {
		state.protocolErr = fmt.Errorf("read pi events: %w", err)
		cancel()
	}
}

func handlePiEvent(event piProtocolEvent, stdin io.Closer, progress func(string) error, state *piEventState, cancel context.CancelFunc) {
	switch event.Type {
	case "response":
		if event.ID != "maintain" {
			return
		}
		if !event.Success || event.Data.Disposition == "handled" {
			state.protocolErr = fmt.Errorf("pi did not start the maintenance task; check pi model/login configuration")
			cancel()
			return
		}
		state.accepted = true
	case "message_end":
		if event.Message.Role == "assistant" {
			state.assistantOK = event.Message.StopReason != "error" && event.Message.StopReason != "aborted" && event.Message.StopReason != "pending"
		}
	case "tool_execution_start":
		// Store bounded operation names, never tool arguments, output, or reasoning.
		name := event.ToolName
		if len(name) > 80 {
			name = name[:80]
		}
		if err := progress("Pi 执行工具：" + name); err != nil {
			state.protocolErr = err
			cancel()
		}
	case "extension_ui_request":
		state.protocolErr = fmt.Errorf("pi requested interactive input in an unattended task")
		cancel()
	case "agent_settled":
		state.settled = true
		if event.Aborted || !state.assistantOK {
			state.protocolErr = fmt.Errorf("pi run ended without a successful assistant response; check pi model/login configuration")
		}
		if err := stdin.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			state.protocolErr = fmt.Errorf("close pi input: %w", err)
		}
		// A malformed worker must not hang after announcing completion.
		state.shutdownTimer = time.AfterFunc(5*time.Second, cancel)
	}
}

func checkPiOutcome(ctx context.Context, state *piEventState, waitErr error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if state.protocolErr != nil {
		return state.protocolErr
	}
	if waitErr != nil {
		return fmt.Errorf("pi exited unsuccessfully: %w", waitErr)
	}
	if !state.accepted || !state.settled {
		return fmt.Errorf("pi exited before accepting and completing the task")
	}
	return nil
}

type AdapterCallOptions struct {
	Policy Policy
	Server registry.Server
	Tool   registry.Tool
	Args   map[string]any
	Path   string
	Lookup func(string) string
}

func CallAdapterFile(ctx context.Context, opts AdapterCallOptions) (any, error) {
	lookup := opts.Lookup
	if lookup == nil {
		lookup = os.Getenv
	}
	headers := map[string]string{}
	env := map[string]string{}
	for k, v := range opts.Server.Headers {
		headers[k] = os.Expand(v, lookup)
	}
	for k, v := range opts.Server.Env {
		env[k] = os.Expand(v, lookup)
	}
	payload, err := json.Marshal(map[string]any{"tool": opts.Tool.Name, "arguments": opts.Args, "endpoint": opts.Policy.AdapterEndpoint(opts.Server.Endpoint), "headers": headers, "env": env})
	if err != nil {
		return nil, fmt.Errorf("encode adapter input: %w", err)
	}
	b, err := opts.Policy.RunNode(ctx, NodeRunOptions{
		Dir:     filepath.Dir(opts.Path),
		Args:    []string{opts.Path},
		Input:   payload,
		Network: true,
		Limit:   8 << 20,
	})
	if err != nil {
		return nil, err
	}
	var out any
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("decode adapter result: %w", err)
	}
	if err := input.EnsureJSONEnd(dec, invalid); err != nil {
		return nil, err
	}
	return out, nil
}

// CallAdapterFileWithLookup invokes an adapter script, expanding header and env
// credential placeholders via lookup (defaulting to os.Getenv).
func CallAdapterFileWithLookup(ctx context.Context, policy Policy, s registry.Server, t registry.Tool, args map[string]any, p string, lookup func(string) string) (any, error) {
	return CallAdapterFile(ctx, AdapterCallOptions{
		Policy: policy,
		Server: s,
		Tool:   t,
		Args:   args,
		Path:   p,
		Lookup: lookup,
	})
}

// limitedOutput must keep draining the pipe even when its capture buffer is full.
var _ io.Writer = (*limitedOutput)(nil)

type PiOptions struct {
	Provider string
	Model    string
}
