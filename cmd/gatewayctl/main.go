package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func run() error {
	endpoint := flag.String("url", "http://127.0.0.1:8788", "Gateway URL")
	actor := flag.String("actor", "developer", "Simulated policy identity")
	mode := flag.String("mode", "", "model or retrieval; empty uses saved gateway setting")
	args := flag.String("args", "{}", "Tool arguments as JSON")
	fields := flag.String("fields", "", "Comma-separated response projection paths")
	contextText := flag.String("context", "", "Context for discovery")
	summary := flag.Bool("summary", false, "Return capability summaries without schemas")
	includeDemo := flag.Bool("demo", false, "Include built-in demo tools in discovery")
	flag.Parse()
	if flag.NArg() < 1 {
		return fmt.Errorf("usage: gatewayctl [flags] list | search <intent> | schema <tool_id> | call <tool_id>")
	}
	method, path := http.MethodGet, "/api/catalog?actor="+url.QueryEscape(*actor)+"&include_demo="+fmt.Sprint(*includeDemo)
	var payload any
	switch flag.Arg(0) {
	case "list":
	case "search":
		if flag.NArg() < 2 {
			return fmt.Errorf("search requires an intent")
		}
		method, path = http.MethodPost, "/api/discover"
		payload = map[string]any{"intent": strings.Join(flag.Args()[1:], " "), "actor": *actor, "mode": *mode, "context": *contextText, "include_demo": *includeDemo, "summary_only": *summary}
	case "schema":
		if flag.NArg() != 2 {
			return fmt.Errorf("schema requires one tool id")
		}
		path = "/api/tools/" + url.PathEscape(flag.Arg(1)) + "/schema?actor=" + url.QueryEscape(*actor)
	case "call":
		if flag.NArg() != 2 {
			return fmt.Errorf("call requires one tool id")
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(*args), &parsed); err != nil {
			return fmt.Errorf("parse arguments: %w", err)
		}
		var projection []string
		if *fields != "" {
			projection = strings.Split(*fields, ",")
		}
		method, path = http.MethodPost, "/api/invoke"
		payload = map[string]any{"tool_id": flag.Arg(1), "arguments": parsed, "actor": *actor, "fields": projection}
	default:
		return fmt.Errorf("unknown command %q", flag.Arg(0))
	}
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, strings.TrimRight(*endpoint, "/")+path, body)
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
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, b, "", "  "); err != nil {
		return fmt.Errorf("format response: %w", err)
	}
	if _, err := fmt.Fprintln(os.Stdout, pretty.String()); err != nil {
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
