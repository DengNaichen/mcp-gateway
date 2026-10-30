// sshlogs exposes only bounded Docker log reads over an existing SSH identity.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const maxLogBytes = 512 << 10

var safeName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)
var bearer = regexp.MustCompile(`(?i)\bBearer\s+[a-zA-Z0-9._~+/=-]+`)
var secretPair = regexp.MustCompile(`(?i)((?:password|passwd|secret|api[_-]?key|access[_-]?token|refresh[_-]?token|token|authorization|cookie)\s*[=:]\s*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;&]+)`)
var email = regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`)

type serviceFlags map[string]string

func (s serviceFlags) String() string { return fmt.Sprint(map[string]string(s)) }
func (s serviceFlags) Set(value string) error {
	alias, container, ok := strings.Cut(value, "=")
	if !ok || !safeName.MatchString(alias) || !safeName.MatchString(container) {
		return fmt.Errorf("service must be a safe alias=container pair")
	}
	if _, exists := s[alias]; exists {
		return fmt.Errorf("duplicate service alias %q", alias)
	}
	s[alias] = container
	return nil
}

type logArgs struct {
	Service string `json:"service"`
	Minutes int    `json:"minutes,omitempty"`
	Limit   int    `json:"limit,omitempty"`
	Query   string `json:"query,omitempty"`
}

func (a *logArgs) validate(services serviceFlags) error {
	if _, ok := services[a.Service]; !ok {
		return fmt.Errorf("unknown log service")
	}
	if a.Minutes == 0 {
		a.Minutes = 15
	}
	if a.Limit == 0 {
		a.Limit = 100
	}
	if a.Minutes < 1 || a.Minutes > 1440 || a.Limit < 1 || a.Limit > 500 || len(a.Query) > 256 {
		return fmt.Errorf("minutes must be 1–1440, limit 1–500, query at most 256 bytes")
	}
	return nil
}

// These are the only remote command arguments; all dynamic values are validated
// or formatted integers. SSH executes no user-supplied command or search text.
func remoteCommand(container string, a logArgs) string {
	return "docker logs --timestamps --since " + strconv.Itoa(a.Minutes) + "m --tail " + strconv.Itoa(a.Limit) + " " + container
}

type boundedBuffer struct {
	mu sync.Mutex
	bytes.Buffer
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	remaining := maxLogBytes - b.Len()
	if len(p) > remaining {
		b.truncated = true
		p = p[:remaining]
	}
	if _, err := b.Buffer.Write(p); err != nil {
		return 0, fmt.Errorf("buffer logs: %w", err)
	}
	return n, nil
}

func redactText(s string) string {
	s = bearer.ReplaceAllString(s, "Bearer [redacted]")
	s = secretPair.ReplaceAllString(s, "${1}[redacted]")
	return email.ReplaceAllString(s, "[redacted-email]")
}

func redact(v any) any {
	switch value := v.(type) {
	case map[string]any:
		for key, child := range value {
			normal := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
			switch normal {
			case "password", "passwd", "secret", "authorization", "apikey", "accesstoken", "refreshtoken", "token", "cookie", "setcookie":
				value[key] = "[redacted]"
			default:
				value[key] = redact(child)
			}
		}
		return value
	case []any:
		for i := range value {
			value[i] = redact(value[i])
		}
		return value
	case string:
		return redactText(value)
	default:
		return v
	}
}

func logResult(raw string, a logArgs, truncated bool) map[string]any {
	logs := []any{}
	lines := strings.Split(strings.TrimSuffix(strings.ToValidUTF8(raw, "�"), "\n"), "\n")
	// A partial final line can contain a truncated JSON secret: omit it entirely.
	if truncated && len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}
	for _, line := range lines {
		if line == "" || (a.Query != "" && !strings.Contains(strings.ToLower(line), strings.ToLower(a.Query))) {
			continue
		}
		stamp, message, ok := strings.Cut(line, " ")
		if _, err := time.Parse(time.RFC3339Nano, stamp); err != nil || !ok {
			message = line
			stamp = ""
		}
		var data any
		if err := json.Unmarshal([]byte(message), &data); err != nil {
			data = message
		}
		logs = append(logs, map[string]any{"timestamp": stamp, "data": redact(data)})
	}
	return map[string]any{"service": a.Service, "minutes": a.Minutes, "tail_limit": a.Limit, "query": redactText(a.Query), "logs": logs, "count": len(logs), "truncated": truncated, "scope": "Recent container stdout/stderr only; keyword filter applies within this tail, not full log history."}
}

func result(data any) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("encode logs result: %w", err)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}, StructuredContent: data}, nil
}

func run() error {
	host := flag.String("host", "", "SSH server IP")
	user := flag.String("user", "root", "SSH user")
	identity := flag.String("identity", "", "Absolute path to existing SSH identity")
	services := serviceFlags{}
	flag.Var(services, "service", "Allowed alias=container (repeatable)")
	flag.Parse()
	if net.ParseIP(*host) == nil || !safeName.MatchString(*user) || !filepath.IsAbs(*identity) || len(services) == 0 {
		return fmt.Errorf("provide an IP host, safe user, absolute identity path and at least one service")
	}
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)
	s := mcp.NewServer(&mcp.Implementation{Name: "ssh-container-logs", Version: "0.1.0"}, nil)
	s.AddTool(&mcp.Tool{Name: "list_log_services", Description: "列出此服务器已配置可查询日志的服务名。List configured service names for log queries; this is an allowlist, not a live health check.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return result(map[string]any{"services": names})
	})
	s.AddTool(&mcp.Tool{Name: "read_service_logs", Description: "只读查询服务器服务最近的容器日志，排查错误、报错、超时和异常。Read recent Docker stdout/stderr logs by service, lookback minutes and tail limit; optional case-insensitive keyword filters only those tail lines. Does not execute arbitrary commands or search full history.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{
		"service": map[string]any{"type": "string", "enum": names},
		"minutes": map[string]any{"type": "integer", "minimum": 1, "maximum": 1440, "description": "Lookback minutes; default 15"},
		"limit":   map[string]any{"type": "integer", "minimum": 1, "maximum": 500, "description": "Recent lines scanned; default 100"},
		"query":   map[string]any{"type": "string", "maxLength": 256, "description": "Case-insensitive literal substring in the recent tail"},
	}, "required": []string{"service"}, "additionalProperties": false}, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var a logArgs
		if err := json.Unmarshal(req.Params.Arguments, &a); err != nil {
			return nil, fmt.Errorf("decode log query: %w", err)
		}
		if err := a.validate(services); err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "/usr/bin/ssh", "-F", "/dev/null", "-T", "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=8", "-o", "ServerAliveInterval=5", "-o", "ServerAliveCountMax=2", "-i", *identity, *user+"@"+*host, remoteCommand(services[a.Service], a))
		var output boundedBuffer
		cmd.Stdout = &output
		cmd.Stderr = &output
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("read service logs over ssh failed (check SSH access and configured container): %w", err)
		}
		return result(logResult(output.String(), a, output.truncated))
	})
	if err := s.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		return fmt.Errorf("run log MCP server: %w", err)
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		slog.Error("log MCP stopped", "error", err)
		os.Exit(1)
	}
}
