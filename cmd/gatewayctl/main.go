package main

import (
	"bytes"
	"context"
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
	return runGatewayCommand(cmdArgs, gatewayCommandOptions{
		Endpoint:    *endpoint,
		Actor:       *actor,
		RawJSONArgs: *args,
		Fields:      *fields,
		ContextText: *contextText,
		Summary:     *summary,
		IncludeDemo: *includeDemo,
	}, stdout)
}

type gatewayCommandOptions struct {
	Endpoint    string
	Actor       string
	RawJSONArgs string
	Fields      string
	ContextText string
	Summary     bool
	IncludeDemo bool
}

func runGatewayCommand(cmdArgs []string, opts gatewayCommandOptions, stdout io.Writer) error {
	method, path := http.MethodGet, "/api/catalog?actor="+url.QueryEscape(opts.Actor)+"&include_demo="+fmt.Sprint(opts.IncludeDemo)
	var payload any
	switch cmdArgs[0] {
	case "list":
	case "search":
		if len(cmdArgs) < 2 {
			return fmt.Errorf("search requires an intent")
		}
		method, path = http.MethodPost, "/api/discover"
		payload = map[string]any{"intent": strings.Join(cmdArgs[1:], " "), "actor": opts.Actor, "context": opts.ContextText, "include_demo": opts.IncludeDemo, "summary_only": opts.Summary}
	case "schema":
		if len(cmdArgs) != 2 {
			return fmt.Errorf("schema requires one tool id")
		}
		path = "/api/tools/" + url.PathEscape(cmdArgs[1]) + "/schema?actor=" + url.QueryEscape(opts.Actor)
	case "call":
		if len(cmdArgs) != 2 {
			return fmt.Errorf("call requires one tool id")
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(opts.RawJSONArgs), &parsed); err != nil {
			return fmt.Errorf("parse arguments: %w", err)
		}
		var projection []string
		if opts.Fields != "" {
			projection = strings.Split(opts.Fields, ",")
		}
		method, path = http.MethodPost, "/api/invoke"
		payload = map[string]any{"tool_id": cmdArgs[1], "arguments": parsed, "actor": opts.Actor, "fields": projection}
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
	req, err := http.NewRequest(method, strings.TrimRight(opts.Endpoint, "/")+path, body)
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
		return fmt.Errorf("usage: gatewayctl plugin <status|check|install|update|remove|verify-prompt> [flags]")
	}
	action := args[0]
	fs := flag.NewFlagSet("gatewayctl plugin "+action, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	clientList := strings.Join(clientconfig.ClientIDs(), "|")
	clientFlag := fs.String("client", "", "Target client ("+clientList+"|all)")
	urlFlag := fs.String("url", defaultURL, "Gateway base or MCP endpoint URL")
	homeFlag := fs.String("home", "", "Override user home directory")
	stateFlag := fs.String("state", "", "Override plugin state directory")
	dataFlag := fs.String("data", "", "Gateway data directory")
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
	if target == "" && (action == "status" || action == "check" || action == "verify-prompt") {
		target = "all"
	}
	if target == "" {
		return fmt.Errorf("plugin %s requires -client <%s|all>", action, clientList)
	}

	targets, isAll, err := resolvePluginTargets(target)
	if err != nil {
		return err
	}

	home, configEnv, err := resolvePluginHome(*homeFlag)
	if err != nil {
		return err
	}
	dataDir := strings.TrimSpace(*dataFlag)
	if dataDir != "" {
		if abs, absErr := filepath.Abs(dataDir); absErr == nil {
			dataDir = abs
		}
	} else if strings.TrimSpace(*homeFlag) == "" && strings.TrimSpace(os.Getenv("GATEWAY_CLIENT_CONFIG_HOME")) == "" {
		dataDir = clientconfig.ResolveClientDataDir("", configEnv)
	}
	slug := buildinfo.Current().Slug
	stateDir := strings.TrimSpace(*stateFlag)
	if stateDir == "" {
		stateDir = filepath.Join(home, "."+slug, "plugins")
	}
	mgr := clientplugin.New(clientplugin.Options{
		HomeDir:       home,
		StateDir:      stateDir,
		DataDir:       dataDir,
		BinPath:       strings.TrimSpace(*binFlag),
		Getenv:        configEnv,
		ClientConfigs: clientconfig.New(home, filepath.Join(home, "."+slug, "client-config"), configEnv),
	})
	mcpURL := resolveMCPEndpoint(*urlFlag)

	switch action {
	case "status":
		return printPluginStatuses(stdout, mgr.Statuses(mcpURL), targets, isAll)
	case "check":
		if _, err := mgr.CheckUpdates(context.Background()); err != nil {
			return err
		}
		return printPluginStatuses(stdout, mgr.Statuses(mcpURL), targets, isAll)
	case "verify-prompt":
		return printVerifyPrompts(stdout, mgr, mcpURL, targets, isAll)
	case "install":
		return applyPluginAction(stdout, pluginActionOptions{Mgr: mgr, MCPURL: mcpURL, Targets: targets, AllTargets: isAll})
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
	ids := clientconfig.ClientIDs()
	if norm == "all" {
		return ids, true, nil
	}
	if spec, ok := clientconfig.LookupClient(norm); ok {
		return []string{spec.ID}, false, nil
	}
	return nil, false, fmt.Errorf("unknown client %q (expected %s, or all)", target, strings.Join(ids, ", "))
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
	return clientconfig.ClientEndpoint(raw, "")
}

type pluginActionOptions struct {
	Mgr        *clientplugin.Manager
	MCPURL     string
	Targets    []string
	AllTargets bool
	Update     bool
}

func applyPluginAction(stdout io.Writer, opts pluginActionOptions) error {
	var errs []error
	for _, id := range opts.Targets {
		var err error
		if opts.Update {
			err = opts.Mgr.Update(id, opts.MCPURL)
		} else {
			err = opts.Mgr.Install(id, opts.MCPURL)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return printPluginStatuses(stdout, opts.Mgr.Statuses(opts.MCPURL), opts.Targets, opts.AllTargets)
}

func updatePluginTargets(stdout io.Writer, mgr *clientplugin.Manager, mcpURL string, targets []string, isAll bool) error {
	if !isAll {
		return applyPluginAction(stdout, pluginActionOptions{Mgr: mgr, MCPURL: mcpURL, Targets: targets, Update: true})
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
	return applyPluginAction(stdout, pluginActionOptions{Mgr: mgr, MCPURL: mcpURL, Targets: selected, AllTargets: true, Update: true})
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
