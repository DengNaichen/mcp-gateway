# 客户端配置验证

2026-11-16，macOS arm64。

## 实现与边界

MyGo「连接客户端」页面在桌面进程调用 `internal/clientconfig`，不向远程 HTTP 管理面开放本机文件写入。三个原有接入入口统一进入此页。全局配置只新增 `mcp-gateway`；遇到同名条目拒绝覆盖，因此无需恢复被替换条目。撤回只删除有本机写入记录且内容仍匹配的条目，保留其他配置和后来编辑。写入记录位于 `~/.mcp-gateway/client-config`。

JSON/JSONC 使用保留注释的语法树，拒绝重复键和损坏文件。Codex 追加独立 TOML 表，撤回要求原文本片段仍存在。文件通过同目录临时文件原子替换，并保留已有权限；写入前重新比较原文件，写入后读取核对。进程内操作串行；外部程序不遵守同一锁，最后一次比较与 rename 之间仍存在文件竞争窗口。

配置状态不是连接状态。Gateway 需保持运行；项目配置可覆盖全局配置。共享模式需要在客户端补充身份凭据，页面明确提示。

## 格式依据

- Codex CLI 0.169.0 的 `mcp --help`、`mcp get`：`~/.codex/config.toml`，`mcp_servers.mcp-gateway.url`。
- Claude Code 2.1.316 的 `mcp add/get`：`~/.claude.json`，`mcpServers`，HTTP 类型。
- [Cursor 官方说明](https://prod.cursor.com/help/customization/mcp)：`~/.cursor/mcp.json`，`mcpServers`，URL。
- [OpenCode v2 官方说明](https://opencode.ai/docs/mcp-servers)：`~/.config/opencode/opencode.json(c)`，`mcp.servers`，remote 类型，禁用本地端点不需要的 OAuth。
- 本机 Pi 1.5.2 的 `docs/mcp.md` 与 `pi mcp --help`：`~/.pi/agent/mcp.json`，`mcpServers`。使用 direct exposure 暴露三个工具。

尊重 Codex、Claude、Pi 的配置目录环境变量及 OpenCode 的 XDG_CONFIG_HOME；已有 OpenCode JSONC 时选择 JSONC。

## 已执行验证

- `go test ./...` 通过。
- 新增 clientconfig 与原生页面测试通过 race 检查，涵盖五客户端写入/撤回、注释、无关编辑、同名冲突、外部修改、损坏文件、重复键、权限及重启后撤回。
- `CGO_ENABLED=0 go tool mygo build -skip-dmg` 成功生成 macOS app。
- 打包后的 MyGo 应用中通过真实 UI 点击 Codex「撤回配置」「写入配置」，状态分别转为未配置/已配置；五张卡片均显示实际路径与操作按钮。
- Codex 0.169.0：真实全局配置写入后，`codex mcp get mcp-gateway --json` 识别 Streamable HTTP；撤回后原文件 SHA 校验一致。
- Claude Code 2.1.316：读取隔离用户配置并报告 Connected；撤回后 get 报不存在。
- Pi 1.5.2：读取隔离用户配置，报告 connected，列出 discover_tools、get_tool_schema、invoke_tool；撤回后服务器列表为空。
- OpenCode v2.3.0：配置解析成功；初始化会话后报告 connected；撤回后只剩验证用的其他服务器，证明保留无关配置。其裸 mcp list 在会话初始化前可能返回空列表。
- Cursor 3.30.14：真实全局配置被读取，MCP 日志报告 Successfully connected to streamableHttp server；撤回后日志记录 config_server_removed。

全量桌面 `-race` 另发现原有 services/maintenance 无窗口测试异步更新的竞争（ui_services.go / updateUI），不在本功能变更范围；新增功能的专门 race 测试通过。

## 重跑

```sh
go test ./...
go test -race ./internal/clientconfig ./cmd/gateway-desktop -run 'TestLifecycle|TestProtect|TestMalformed|TestDuplicate|TestPermissions|TestPathOverrides|TestClientPage'
CGO_ENABLED=0 go tool mygo build -skip-dmg
```

`go run ./scripts/client-config-check.go -home <显式测试用户目录> -state <记录目录> -client pi -action write -endpoint http://127.0.0.1:8788/mcp` 使用与桌面相同的实现；action 支持 status/write/revoke。

原生端到端验证可设置 `GATEWAY_CLIENT_CONFIG_HOME` 指向隔离目录，`GATEWAY_DESKTOP_CONTROL` 指向本地 Unix socket；GET /clients、POST /clients（ID/Action）与 UI 共用管理器。该 socket 默认关闭，权限为 0600。

最终生成的 app 再次完成全部五客户端 write → 重复 write → revoke，状态及文件核对通过。最终构建和运行证据保留在 `/tmp/gateway-client-verification/`：`final-build/`、`final-tests.log`、`new-race.log`、`final-runtime.log`、`final-clients.png`。测试服务器已退出；本机 Codex 配置恢复到原始校验和，Cursor 测试条目已撤回，且在已登录的 MCP 页面确认不存在。

2026-11-16 客户端页面补充：嵌入五客户端品牌图标，路径改为名称后的 ↗ 按钮，完整路径保留在 tooltip。已有文件通过 MyGo ShowItemInFolder 定位；未创建文件时打开最近存在的上级目录，不创建额外文件。新增路径回退及可访问按钮/隐藏长路径测试通过，实际打包页面截图位于 `/tmp/gateway-client-icons/page.png`。未设置测试配置目录覆盖时，实际读取的路径已核对为 `/Users/nd/.codex/config.toml` 等真实用户目录。系统屏幕捕获返回 SCStreamError -3811，因此未完成 Finder 实际点击验证；已核对 MyGo 原生 API 接线、运行页面和路径解析。
