package upstream

import (
	"bytes"
	"html/template"
	"net/url"
	"strings"
)

type oauthCallbackState string

const (
	oauthCallbackReceived oauthCallbackState = "received"
	oauthCallbackError    oauthCallbackState = "error"
	oauthCallbackInvalid  oauthCallbackState = "invalid"
)

type oauthCallbackPageData struct {
	State       oauthCallbackState
	TabTitle    string
	StatusLabel string
	Title       string
	Description string
	ServiceName string
	ServiceHost string
	ErrorDetail string
	ShowService bool
	ShowMeta    bool
}

var oauthCallbackPageTmpl = template.Must(template.New("oauth_callback").Parse(`<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>{{.TabTitle}}</title>
  <link rel="icon" type="image/svg+xml" href="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 180 180' fill='none'%3E%3Crect width='180' height='180' rx='36' fill='%23000'/%3E%3Cpath d='M23.6 85.25L86.2 22.65C94.85 14.01 108.86 14.01 117.5 22.65C126.15 31.29 126.15 45.31 117.5 53.95L70.23 101.23' stroke='white' stroke-width='12' stroke-linecap='round'/%3E%3Cpath d='M70.88 100.58L117.5 53.95C126.15 45.31 140.16 45.31 148.81 53.95L149.13 54.28C157.78 62.92 157.78 76.94 149.13 85.58L92.51 142.2C89.63 145.08 89.63 149.75 92.51 152.63L104.14 164.26' stroke='white' stroke-width='12' stroke-linecap='round'/%3E%3Cpath d='M101.85 38.3L55.55 84.6C46.91 93.24 46.91 107.26 55.55 115.9C64.2 124.55 78.21 124.55 86.85 115.9L133.15 69.6' stroke='white' stroke-width='12' stroke-linecap='round'/%3E%3C/svg%3E" />
  <style>
    :root {
      color-scheme: light dark;
      --bg-canvas: #ffffff;
      --bg-surface: #ffffff;
      --bg-hover: #f3f6fa;
      --ink-strong: #111827;
      --ink-body: #374151;
      --ink-muted: #6b7280;
      --ink-faint: #9ca3af;
      --divider: #edf1f6;
      --border-strong: #cbd5e1;
      --accent: #635bff;
      --dot-success: #16a34a;
      --danger-bg: #fee2e2;
      --danger-border: #fecaca;
      --danger-ink: #b91c1c;
      --warning-bg: #fef3c7;
      --warning-border: #fde68a;
      --warning-ink: #b45309;
      --lift-border: rgba(15, 23, 42, 0.065);
      --card-shadow: 0 3px 10px rgba(15, 23, 42, 0.04);
      --control-shadow: 0 2px 6px rgba(15, 23, 42, 0.07);
    }

    @media (prefers-color-scheme: dark) {
      :root {
        --bg-canvas: #1a1a1e;
        --bg-surface: #232327;
        --bg-hover: #2c2c33;
        --ink-strong: #f4f4f5;
        --ink-body: #d4d4d8;
        --ink-muted: #a1a1aa;
        --ink-faint: #71717a;
        --divider: #2b2b31;
        --border-strong: #43434c;
        --accent: #7a73ff;
        --dot-success: #4ade80;
        --danger-bg: #2e161a;
        --danger-border: #5c252c;
        --danger-ink: #f87171;
        --warning-bg: #2c2211;
        --warning-border: #564019;
        --warning-ink: #fbbf24;
        --lift-border: rgba(255, 255, 255, 0.08);
        --card-shadow: 0 3px 10px rgba(0, 0, 0, 0.16);
        --control-shadow: 0 2px 6px rgba(0, 0, 0, 0.22);
      }
    }

    * { box-sizing: border-box; margin: 0; padding: 0; }
    html, body { height: 100%; }

    body {
      background: var(--bg-canvas);
      color: var(--ink-strong);
      font: 13px/1.5 -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Hiragino Sans GB", sans-serif;
      -webkit-font-smoothing: antialiased;
      display: flex;
      align-items: center;
      justify-content: center;
      padding: 32px 24px;
    }

    .card {
      width: 100%;
      max-width: 440px;
      background: var(--bg-surface);
      border: 1px solid var(--lift-border);
      border-radius: 10px;
      box-shadow: var(--card-shadow);
      padding: 22px 24px;
      display: flex;
      flex-direction: column;
      gap: 18px;
    }

    .card-header {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 12px;
    }

    .brand {
      display: inline-flex;
      align-items: center;
      gap: 8px;
      min-width: 0;
    }

    .brand-mark {
      width: 22px;
      height: 22px;
      border-radius: 6px;
      background: #000000;
      color: #ffffff;
      display: inline-flex;
      align-items: center;
      justify-content: center;
      flex-shrink: 0;
      border: 1px solid rgba(255, 255, 255, 0.12);
    }

    .brand-name {
      font-size: 13px;
      font-weight: 600;
      letter-spacing: -0.01em;
      color: var(--ink-strong);
      white-space: nowrap;
    }

    .quiet-status {
      display: inline-flex;
      align-items: center;
      gap: 6px;
      font-size: 12px;
      color: var(--ink-muted);
      white-space: nowrap;
    }

    .quiet-dot {
      width: 6px;
      height: 6px;
      border-radius: 50%;
      background: var(--dot-success);
      flex-shrink: 0;
    }

    .status-pill {
      display: inline-flex;
      align-items: center;
      font-size: 11.5px;
      font-weight: 500;
      padding: 2px 8px;
      border-radius: 999px;
      border: 1px solid transparent;
      white-space: nowrap;
    }

    .status-pill.danger {
      background: var(--danger-bg);
      border-color: var(--danger-border);
      color: var(--danger-ink);
    }

    .status-pill.warning {
      background: var(--warning-bg);
      border-color: var(--warning-border);
      color: var(--warning-ink);
    }

    .card-body {
      display: flex;
      flex-direction: column;
      gap: 6px;
    }

    .card-title {
      font-size: 17px;
      font-weight: 600;
      letter-spacing: -0.02em;
      line-height: 1.35;
      color: var(--ink-strong);
    }

    .card-desc {
      font-size: 13px;
      line-height: 1.55;
      color: var(--ink-muted);
    }

    .meta-section {
      padding-top: 14px;
      border-top: 1px solid var(--divider);
      display: flex;
      flex-direction: column;
      gap: 12px;
    }

    .service-line {
      display: flex;
      align-items: baseline;
      justify-content: space-between;
      gap: 16px;
    }

    .meta-key {
      font-size: 12px;
      color: var(--ink-muted);
      flex-shrink: 0;
    }

    .service-val {
      display: flex;
      flex-direction: column;
      align-items: flex-end;
      gap: 2px;
      min-width: 0;
    }

    .service-name {
      font-size: 12.5px;
      font-weight: 500;
      color: var(--ink-strong);
      max-width: 100%;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }

    .service-host {
      font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
      font-size: 11.5px;
      color: var(--ink-faint);
      max-width: 100%;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }

    .error-block {
      display: flex;
      flex-direction: column;
      gap: 6px;
    }

    .error-head {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 12px;
    }

    .error-text {
      font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
      font-size: 12px;
      line-height: 1.5;
      color: var(--danger-ink);
      word-break: break-word;
      max-height: 120px;
      overflow-y: auto;
    }

    .btn-sm {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      height: 26px;
      padding: 0 9px;
      font: inherit;
      font-size: 11.5px;
      font-weight: 500;
      border-radius: 6px;
      border: 1px solid var(--lift-border);
      background: var(--bg-surface);
      color: var(--ink-body);
      box-shadow: var(--control-shadow);
      cursor: pointer;
      white-space: nowrap;
      flex-shrink: 0;
    }

    .btn-sm:hover {
      background: var(--bg-canvas);
      border-color: var(--border-strong);
      color: var(--ink-strong);
    }

    .btn-sm:active {
      box-shadow: none;
      background: var(--bg-hover);
    }

    .btn-sm:focus-visible {
      outline: 2px solid var(--accent);
      outline-offset: 2px;
    }

    @media (max-width: 520px) {
      body { padding: 20px 16px; }
      .card { padding: 20px; }
    }
  </style>
</head>
<body>
  <section class="card" role="status" aria-live="polite">
    <header class="card-header">
      <div class="brand">
        <span class="brand-mark" aria-hidden="true">
          <svg width="14" height="14" viewBox="0 0 180 180" fill="none">
            <path d="M23.6 85.25L86.2 22.65C94.85 14.01 108.86 14.01 117.5 22.65C126.15 31.29 126.15 45.31 117.5 53.95L70.23 101.23" stroke="currentColor" stroke-width="15" stroke-linecap="round"/>
            <path d="M70.88 100.58L117.5 53.95C126.15 45.31 140.16 45.31 148.81 53.95L149.13 54.28C157.78 62.92 157.78 76.94 149.13 85.58L92.51 142.2C89.63 145.08 89.63 149.75 92.51 152.63L104.14 164.26" stroke="currentColor" stroke-width="15" stroke-linecap="round"/>
            <path d="M101.85 38.3L55.55 84.6C46.91 93.24 46.91 107.26 55.55 115.9C64.2 124.55 78.21 124.55 86.85 115.9L133.15 69.6" stroke="currentColor" stroke-width="15" stroke-linecap="round"/>
          </svg>
        </span>
        <span class="brand-name">MCP Gateway</span>
      </div>
      {{if eq .State "received"}}
      <span class="quiet-status"><span class="quiet-dot"></span>{{.StatusLabel}}</span>
      {{else if eq .State "error"}}
      <span class="status-pill danger">{{.StatusLabel}}</span>
      {{else}}
      <span class="status-pill warning">{{.StatusLabel}}</span>
      {{end}}
    </header>

    <div class="card-body">
      <h1 class="card-title">{{.Title}}</h1>
      <p class="card-desc">{{.Description}}</p>
    </div>

    {{if .ShowMeta}}
    <div class="meta-section">
      {{if .ShowService}}
      <div class="service-line">
        <span class="meta-key">服务</span>
        <div class="service-val">
          {{if .ServiceName}}<span class="service-name" title="{{.ServiceName}}">{{.ServiceName}}</span>{{end}}
          {{if .ServiceHost}}<span class="service-host" title="{{.ServiceHost}}">{{.ServiceHost}}</span>{{end}}
        </div>
      </div>
      {{end}}
      {{if .ErrorDetail}}
      <div class="error-block">
        <div class="error-head">
          <span class="meta-key">原因</span>
          <button id="copyErrBtn" type="button" class="btn-sm">复制</button>
        </div>
        <div id="errorDetail" class="error-text">{{.ErrorDetail}}</div>
      </div>
      {{end}}
    </div>
    {{end}}
  </section>
  {{if .ErrorDetail}}
  <script>
    (function () {
      var btn = document.getElementById("copyErrBtn");
      var detail = document.getElementById("errorDetail");
      if (!btn || !detail || !navigator.clipboard) return;
      btn.addEventListener("click", function () {
        navigator.clipboard.writeText(detail.textContent).then(function () {
          btn.textContent = "已复制";
          setTimeout(function () { btn.textContent = "复制"; }, 1500);
        }).catch(function () {
          btn.textContent = "复制失败";
          setTimeout(function () { btn.textContent = "复制"; }, 1500);
        });
      });
    })();
  </script>
  {{end}}
</body>
</html>`))

func oauthCallbackServiceName(server Server) string {
	if name := strings.TrimSpace(server.Name); name != "" {
		return name
	}
	return strings.TrimSpace(server.ID)
}

func oauthCallbackHost(endpoint string) string {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return ""
	}
	return u.Host
}

func renderOAuthCallbackPage(state oauthCallbackState, server Server, errDetail string) []byte {
	data := oauthCallbackPageData{
		State:       state,
		ErrorDetail: strings.TrimSpace(errDetail),
	}

	switch state {
	case oauthCallbackReceived:
		data.TabTitle = "授权完成 · MCP Gateway"
		data.StatusLabel = "正在连接"
		data.Title = "授权完成"
		data.Description = "请返回 MCP Gateway 查看连接结果，可关闭此标签页。"
		data.ServiceName = oauthCallbackServiceName(server)
		data.ServiceHost = oauthCallbackHost(server.Endpoint)
	case oauthCallbackError:
		data.TabTitle = "授权未完成 · MCP Gateway"
		data.StatusLabel = "未完成"
		data.Title = "授权未完成"
		data.Description = "请返回 MCP Gateway 重新发起授权，可关闭此标签页。"
		data.ServiceName = oauthCallbackServiceName(server)
		data.ServiceHost = oauthCallbackHost(server.Endpoint)
	default:
		data.State = oauthCallbackInvalid
		data.TabTitle = "授权链接无效 · MCP Gateway"
		data.StatusLabel = "链接无效"
		data.Title = "授权链接无效"
		data.Description = "请返回 MCP Gateway 重新发起授权，可关闭此标签页。"
	}

	data.ShowService = data.ServiceName != "" || data.ServiceHost != ""
	data.ShowMeta = data.ShowService || data.ErrorDetail != ""

	var buf bytes.Buffer
	_ = oauthCallbackPageTmpl.Execute(&buf, data)
	return buf.Bytes()
}
