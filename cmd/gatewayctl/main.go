package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gatewaylab/internal/buildinfo"
	"gatewaylab/internal/clientconfig"
	"gatewaylab/internal/clientplugin"
)

var allPluginClients = []string{
	clientplugin.ClientCodex,
	clientplugin.ClientClaude,
	clientplugin.ClientCursor,
	clientplugin.ClientOpenCode,
	clientplugin.ClientPi,
}

func run() error {
	return runWithArgs(os.Args[1:], os.Stdout)
}

func runWithArgs(rawArgs []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("gatewayctl", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	endpoint := fs.String("url", "http://"+buildinfo.DefaultAddr(), "Gateway URL")
	actor := fs.String("actor", "developer", "Simulated policy identity")
	args := fs.String("args", "{}", "Tool arguments as JSON")
	fields := fs.String("fields", "", "Comma-separated response projection paths")
	contextText := fs.String("context", "", "Context for discovery")
	summary := fs.Bool("summary", false, "Return capability summaries without schemas")
	includeDemo := fs.Bool("demo", false, "Include built-in demo tools in discovery")
	if err := fs.Parse(rawArgs); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: gatewayctl [flags] list | search <intent> | schema <tool_id> | call <tool_id> | plugin <action>")
	}
	cmdArgs := fs.Args()
	if cmdArgs[0] == "plugin" {
		return runPluginCommand(cmdArgs[1:], *endpoint, stdout)
	}
	return runGatewayCommand(cmdArgs, *endpoint, *actor, *args, *fields, *contextText, *summary, *includeDemo, stdout)
}

func runGatewayCommand(cmdArgs []string, endpoint, actor, rawJSONArgs, fields, contextText string, summary, includeDemo bool, stdout io.Writer) error {
	method, path := http.MethodGet, "/api/catalog?actor="+url.QueryEscape(actor)+"&include_demo="+fmt.Sprint(includeDemo)
	var payload any
	switch cmdArgs[0] {
	case "list":
	case "search":
		if len(cmdArgs) < 2 {
			return fmt.Errorf("search requires an intent")
		}
		method, path = http.MethodPost, "/api/discover"
		payload = map[string]any{"intent": strings.Join(cmdArgs[1:], " "), "actor": actor, "context": contextText, "include_demo": includeDemo, "summary_only": summary}
	case "schema":
		if len(cmdArgs) != 2 {
			return fmt.Errorf("schema requires one tool id")
		}
		path = "/api/tools/" + url.PathEscape(cmdArgs[1]) + "/schema?actor=" + url.QueryEscape(actor)
	case "call":
		if len(cmdArgs) != 2 {
			return fmt.Errorf("call requires one tool id")
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(rawJSONArgs), &parsed); err != nil {
			return fmt.Errorf("parse arguments: %w", err)
		}
		var projection []string
		if fields != "" {
			projection = strings.Split(fields, ",")
		}
		method, path = http.MethodPost, "/api/invoke"
		payload = map[string]any{"tool_id": cmdArgs[1], "arguments": parsed, "actor": actor, "fields": projection}
	default:
		return fmt.Errorf("unknown command %q", cmdArgs[0])
	}
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, strings.TrimRight(endpoint, "/")+path, body)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token := os.Getenv("GATEWAY_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 190 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("connect gateway: %w", err)
	}
	b, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if readErr != nil {
		return fmt.Errorf("read response: %w", readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close response: %w", closeErr)
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("http %d: %s", resp.StatusCode, b)
	}
	return writePrettyRawJSON(stdout, b)
}

func runPluginCommand(args []string, defaultURL string, stdout io.Writer) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: gatewayctl plugin <status|install|update|remove|verify-prompt> [flags]")
	}
	action := args[0]
	fs := flag.NewFlagSet("gatewayctl plugin "+action, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	clientFlag := fs.String("client", "", "Target client (codex|claude|cursor|opencode|pi|all)")
	urlFlag := fs.String("url", defaultURL, "Gateway base or MCP endpoint URL")
	homeFlag := fs.String("home", "", "Override user home directory")
	stateFlag := fs.String("state", "", "Override plugin state directory")
	dataFlag := fs.String("data", os.Getenv("GATEWAY_DATA_DIR"), "Gateway data directory")
	binFlag := fs.String("bin", "", "Override hook binary path")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	target := strings.TrimSpace(*clientFlag)
	if target == "" && fs.NArg() == 1 {
		target = strings.TrimSpace(fs.Arg(0))
	} else if fs.NArg() > 0 && !(target != "" && fs.NArg() == 0) {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if target == "" && (action == "status" || action == "verify-prompt") {
		target = "all"
	}
	if target == "" {
		return fmt.Errorf("plugin %s requires -client <codex|claude|cursor|opencode|pi|all>", action)
	}

	targets, isAll, err := resolvePluginTargets(target)
	if err != nil {
		return err
	}

	home, configEnv, err := resolvePluginHome(*homeFlag)
	if err != nil {
		return err
	}
	slug := buildinfo.Current().Slug
	stateDir := strings.TrimSpace(*stateFlag)
	if stateDir == "" {
		stateDir = filepath.Join(home, "."+slug, "plugins")
	}
	mgr := clientplugin.New(clientplugin.Options{
		HomeDir:       home,
		StateDir:      stateDir,
		DataDir:       strings.TrimSpace(*dataFlag),
		BinPath:       strings.TrimSpace(*binFlag),
		Getenv:        configEnv,
		ClientConfigs: clientconfig.New(home, filepath.Join(home, "."+slug, "client-config"), configEnv),
	})
	mcpURL := resolveMCPEndpoint(*urlFlag)

	switch action {
	case "status":
		return printPluginStatuses(stdout, mgr.Statuses(mcpURL), targets, isAll)
	case "verify-prompt":
		return printVerifyPrompts(stdout, mgr, mcpURL, targets, isAll)
	case "install":
		return applyPluginAction(stdout, mgr, mcpURL, targets, isAll, false)
	case "update":
		return updatePluginTargets(stdout, mgr, mcpURL, targets, isAll)
	case "remove":
		return removePluginTargets(stdout, mgr, mcpURL, targets, isAll)
	default:
		return fmt.Errorf("unknown plugin action %q", action)
	}
}

func resolvePluginTargets(target string) ([]string, bool, error) {
	norm := strings.ToLower(strings.TrimSpace(target))
	if norm == "all" {
		return append([]string(nil), allPluginClients...), true, nil
	}
	for _, id := range allPluginClients {
		if norm == id {
			return []string{id}, false, nil
		}
	}
	return nil, false, fmt.Errorf("unknown client %q (expected codex, claude, cursor, opencode, pi, or all)", target)
}

func resolvePluginHome(override string) (string, func(string) string, error) {
	if h := strings.TrimSpace(override); h != "" {
		return h, func(string) string { return "" }, nil
	}
	if h := strings.TrimSpace(os.Getenv("GATEWAY_CLIENT_CONFIG_HOME")); h != "" {
		return h, func(string) string { return "" }, nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", nil, fmt.Errorf("resolve home directory: %w", err)
	}
	return h, os.Getenv, nil
}

func resolveMCPEndpoint(raw string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	if trimmed == "" {
		return "http://" + buildinfo.DefaultAddr() + "/mcp"
	}
	if strings.HasSuffix(trimmed, "/mcp") {
		return trimmed
	}
	return trimmed + "/mcp"
}

func applyPluginAction(stdout io.Writer, mgr *clientplugin.Manager, mcpURL string, targets []string, isAll, update bool) error {
	var errs []error
	for _, id := range targets {
		var err error
		if update {
			err = mgr.Update(id, mcpURL)
		} else {
			err = mgr.Install(id, mcpURL)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return printPluginStatuses(stdout, mgr.Statuses(mcpURL), targets, isAll)
}

func updatePluginTargets(stdout io.Writer, mgr *clientplugin.Manager, mcpURL string, targets []string, isAll bool) error {
	if !isAll {
		return applyPluginAction(stdout, mgr, mcpURL, targets, false, true)
	}
	installed := installedMap(mgr.Statuses(mcpURL))
	var selected []string
	for _, id := range targets {
		st, ok := installed[id]
		if ok && (st.CanUpdate || st.CanRemove) {
			selected = append(selected, id)
		}
	}
	if len(selected) == 0 {
		return printPluginStatuses(stdout, mgr.Statuses(mcpURL), targets, true)
	}
	return applyPluginAction(stdout, mgr, mcpURL, selected, true, true)
}

func removePluginTargets(stdout io.Writer, mgr *clientplugin.Manager, mcpURL string, targets []string, isAll bool) error {
	installed := installedMap(mgr.Statuses(mcpURL))
	var errs []error
	for _, id := range targets {
		if isAll {
			st, ok := installed[id]
			if !ok || !st.CanRemove {
				continue
			}
		}
		if err := mgr.Remove(id); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return printPluginStatuses(stdout, mgr.Statuses(mcpURL), targets, isAll)
}

func installedMap(statuses []clientplugin.Status) map[string]clientplugin.Status {
	out := make(map[string]clientplugin.Status, len(statuses))
	for _, st := range statuses {
		out[st.ID] = st
	}
	return out
}

func printPluginStatuses(stdout io.Writer, statuses []clientplugin.Status, targets []string, isAll bool) error {
	if isAll {
		return writePrettyValueJSON(stdout, statuses)
	}
	for _, st := range statuses {
		if st.ID == targets[0] {
			return writePrettyValueJSON(stdout, st)
		}
	}
	return fmt.Errorf("client %q not found in status output", targets[0])
}

func printVerifyPrompts(stdout io.Writer, mgr *clientplugin.Manager, mcpURL string, targets []string, isAll bool) error {
	type promptEntry struct {
		Client   string `json:"client"`
		Endpoint string `json:"endpoint"`
		Prompt   string `json:"prompt"`
	}
	if !isAll {
		id := targets[0]
		return writePrettyValueJSON(stdout, promptEntry{
			Client:   id,
			Endpoint: clientconfig.ClientEndpoint(mcpURL, id),
			Prompt:   mgr.VerificationPrompt(id, mcpURL),
		})
	}
	entries := make([]promptEntry, 0, len(targets))
	for _, id := range targets {
		entries = append(entries, promptEntry{
			Client:   id,
			Endpoint: clientconfig.ClientEndpoint(mcpURL, id),
			Prompt:   mgr.VerificationPrompt(id, mcpURL),
		})
	}
	return writePrettyValueJSON(stdout, entries)
}

func writePrettyValueJSON(stdout io.Writer, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode output: %w", err)
	}
	return writePrettyRawJSON(stdout, raw)
}

func writePrettyRawJSON(stdout io.Writer, raw []byte) error {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		return fmt.Errorf("format response: %w", err)
	}
	if _, err := fmt.Fprintln(stdout, pretty.String()); err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		if _, writeErr := fmt.Fprintln(os.Stderr, err); writeErr != nil {
			os.Exit(2)
		}
		os.Exit(1)
	}
}
