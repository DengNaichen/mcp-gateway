package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func objectSchema(properties map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

func seedRegistry(base string) Registry {
	service := map[string]any{"type": "string", "description": "Service name, e.g. billing, checkout or identity"}
	serviceSchema := objectSchema(map[string]any{"service": service}, "service")
	readonly := []string{"developer", "observer"}
	return Registry{
		Servers: []Server{
			{ID: "catalog", Name: "Service Catalog", Description: "服务目录、负责人和运行手册", Kind: "http", Endpoint: base + "/demo/api", Enabled: true, Demo: true},
			{ID: "observability", Name: "Observability", Description: "日志、指标、部署和事件工具", Kind: "mcp", Endpoint: base + "/demo/mcp", Enabled: true, Demo: true},
		},
		Tools: []Tool{
			{ID: "catalog.list_services", ServerID: "catalog", Name: "list_services", Description: "列出内部服务，按名称或描述查找服务。Find services and resolve service names.", Tags: []string{"服务", "目录", "service", "catalog"}, InputSchema: objectSchema(map[string]any{"query": map[string]any{"type": "string", "description": "Optional service search"}}), Enabled: true, ReadOnly: true, Actors: readonly, Method: "GET", Path: "/services", Example: map[string]any{"query": "billing"}},
			{ID: "catalog.get_service_owner", ServerID: "catalog", Name: "get_service_owner", Description: "查询服务负责人、团队和联系渠道。Look up service ownership and team.", Tags: []string{"负责人", "团队", "owner", "service"}, InputSchema: serviceSchema, Enabled: true, ReadOnly: true, Actors: readonly, Method: "GET", Path: "/owner", Example: map[string]any{"service": "billing"}},
			{ID: "catalog.search_runbooks", ServerID: "catalog", Name: "search_runbooks", Description: "搜索故障处理手册、排查步骤及恢复流程。Search runbooks and troubleshooting guides.", Tags: []string{"手册", "排查", "故障", "runbook", "debug"}, InputSchema: objectSchema(map[string]any{"query": map[string]any{"type": "string"}}, "query"), Enabled: true, ReadOnly: true, Actors: readonly, Method: "GET", Path: "/runbooks", Example: map[string]any{"query": "billing timeout"}},
			{ID: "observability.search_error_logs", ServerID: "observability", Name: "search_error_logs", Description: "搜索服务最近的 ERROR 错误日志，用于排查报错、超时和异常原因。Search recent error logs, stack traces and timeouts.", Tags: []string{"错误", "日志", "报错", "超时", "排查", "logs", "error", "billing"}, InputSchema: objectSchema(map[string]any{"service": service, "minutes": map[string]any{"type": "integer", "minimum": 1, "maximum": 1440, "description": "Lookback window in minutes"}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100}}, "service"), Enabled: true, ReadOnly: true, Actors: []string{"developer"}, Example: map[string]any{"service": "billing", "minutes": 10, "limit": 5}},
			{ID: "observability.get_service_metrics", ServerID: "observability", Name: "get_service_metrics", Description: "查询服务错误率、请求量和 P99 延迟，判断故障影响。Get error rate, request volume and latency metrics.", Tags: []string{"指标", "监控", "延迟", "错误率", "metrics", "latency"}, InputSchema: serviceSchema, Enabled: true, ReadOnly: true, Actors: readonly, Example: map[string]any{"service": "billing"}},
			{ID: "observability.get_recent_deployments", ServerID: "observability", Name: "get_recent_deployments", Description: "查询服务近期部署及版本变更，关联报错与发布的时间。Find recent deployments and releases to investigate regressions.", Tags: []string{"发布", "部署", "版本", "报错", "deploy", "release"}, InputSchema: serviceSchema, Enabled: true, ReadOnly: true, Actors: []string{"developer"}, Example: map[string]any{"service": "billing"}},
			{ID: "observability.list_incidents", ServerID: "observability", Name: "list_incidents", Description: "查询服务当前告警和事故事件，了解是否有已知故障。List active incidents and alerts.", Tags: []string{"事件", "告警", "故障", "incident", "alert"}, InputSchema: serviceSchema, Enabled: true, ReadOnly: true, Actors: readonly, Example: map[string]any{"service": "billing"}},
			{ID: "observability.restart_service", ServerID: "observability", Name: "restart_service", Description: "模拟重启服务，演示写操作权限。Only for an explicit restart request; not a diagnostic tool.", Tags: []string{"重启", "restart"}, InputSchema: serviceSchema, Enabled: false, ReadOnly: false, Actors: []string{"developer"}, Example: map[string]any{"service": "billing"}},
		},
	}
}

func demoData(name string, args map[string]any) any {
	service, _ := args["service"].(string)
	if service == "" {
		service = "billing"
	}
	now := time.Now()
	stamp := func(minutes int) string {
		return now.Add(-time.Duration(minutes) * time.Minute).UTC().Format(time.RFC3339)
	}
	switch name {
	case "list_services":
		services := []any{}
		query, _ := args["query"].(string)
		for _, s := range []map[string]any{{"name": "billing", "description": "Payments and invoicing · 支付账单", "team": "Payments"}, {"name": "checkout", "description": "Checkout orchestration · 结算", "team": "Commerce"}, {"name": "identity", "description": "Authentication and users · 认证", "team": "Platform"}} {
			if query == "" || strings.Contains(strings.ToLower(fmt.Sprint(s)), strings.ToLower(query)) {
				services = append(services, s)
			}
		}
		return map[string]any{"demo": true, "services": services}
	case "get_service_owner":
		return map[string]any{"demo": true, "service": service, "owner": map[string]any{"team": "Payments Platform", "oncall": "payments-oncall", "channel": "#team-payments"}}
	case "search_runbooks":
		return map[string]any{"demo": true, "runbooks": []any{map[string]any{"title": "Investigate payment-provider timeouts", "steps": []string{"Check error rate and latency", "Correlate recent deployments", "Inspect provider connection pool"}}}}
	case "search_error_logs":
		logs := []any{}
		count := 5
		if limit, ok := args["limit"].(float64); ok && int(limit) < count {
			count = int(limit)
		}
		minutes := 10
		if window, ok := args["minutes"].(float64); ok {
			minutes = int(window)
		}
		messages := []string{"payment provider request timed out after 3000ms", "connection pool exhausted: active=100 waiting=42", "retry budget exceeded for payment authorization", "payment provider request timed out after 3000ms", "upstream deadline exceeded: authorize_payment"}
		for i := 0; i < count; i++ {
			if i+1 > minutes {
				break
			}
			logs = append(logs, map[string]any{"timestamp": stamp(i + 1), "message": messages[i], "service": service, "level": "ERROR", "trace_id": fmt.Sprintf("demo-trace-%04d", i+1), "host": "billing-demo-03"})
		}
		return map[string]any{"demo": true, "service": service, "logs": logs}
	case "get_service_metrics":
		return map[string]any{"demo": true, "service": service, "window": "10m", "error_rate": 0.081, "requests_per_minute": 12480, "p99_latency_ms": 3120, "baseline_error_rate": 0.002, "timeseries": []float64{0.002, 0.003, 0.004, 0.012, 0.042, 0.081}}
	case "get_recent_deployments":
		return map[string]any{"demo": true, "service": service, "deployments": []any{map[string]any{"version": "v2.18.4", "deployed_at": stamp(17), "change": "Update payment provider connection-pool settings", "status": "completed"}, map[string]any{"version": "v2.18.3", "deployed_at": stamp(480), "change": "Invoice formatting fixes", "status": "completed"}}}
	case "list_incidents":
		return map[string]any{"demo": true, "service": service, "incidents": []any{map[string]any{"id": "INC-DEMO-042", "title": "Elevated payment authorization latency", "severity": "SEV-2", "status": "investigating", "started_at": stamp(12)}}}
	case "restart_service":
		return map[string]any{"demo": true, "service": service, "status": "simulated", "message": "演示结果，未操作任何实际服务"}
	}
	return map[string]any{"demo": true, "error": "unknown demo tool"}
}

func (g *Gateway) demoServer() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "observability-demo", Version: "0.1.0"}, nil)
	for _, tool := range seedRegistry(g.baseURL).Tools {
		if tool.ServerID != "observability" {
			continue
		}
		s.AddTool(&mcp.Tool{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: tool.ReadOnly}}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := map[string]any{}
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return nil, fmt.Errorf("decode demo arguments: %w", err)
			}
			return toolResult(demoData(tool.Name, args))
		})
	}
	return s
}

func (g *Gateway) demoHTTP(w http.ResponseWriter, r *http.Request) {
	names := map[string]string{"services": "list_services", "owner": "get_service_owner", "runbooks": "search_runbooks"}
	name, ok := names[r.PathValue("name")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	args := map[string]any{}
	for k, v := range r.URL.Query() {
		if len(v) > 0 {
			args[k] = v[0]
		}
	}
	respond(w, 200, demoData(name, args))
}
