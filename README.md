# Gateway Lab

一个可以直接运行的 MCP Gateway 原型。Go 后端、React + TypeScript 工作台、SQLite 持久化；网关中的模型负责从候选工具里做选择，本地 Agent 继续负责规划和执行。

## 启动

当前目录已经构建了本机可执行文件：

```sh
cd /Users/naichengdeng/Developer/mcp-gateway
./start.sh
```

打开 <http://127.0.0.1:8788>。前端已经内嵌到 Go 程序中，运行时只需启动一个进程。更换端口使用 `./start.sh -addr 127.0.0.1:8888`。

从源码首次准备和构建需要 Go 1.26+、Node.js 22.12+：

```sh
make setup
make build
./start.sh
```

如果依赖下载受网络影响，可以使用 `GOPROXY=https://goproxy.cn,direct` 和 `npm ci --registry=https://registry.npmmirror.com`。

## 使用流程

1. 打开「路由工作台」。默认「检索预览」只运行本地关键词检索，不会调用任何模型。
2. 在「模型设置」填写兼容 Chat Completions 的 Base URL、模型 ID 和可选 API Key。配置默认模式为「模型决策」，然后保存。
3. 在工作台描述当前任务，按「发现工具」。可以查看候选召回、模型推荐理由和返回的真实 schema。
4. 选择工具，进入检查器的「试运行」，填写参数并执行。可选字段如 `logs.timestamp,logs.message` 会裁剪结果。
5. 在「工具注册表」添加自己的服务和工具。原生 MCP 服务可通过「同步工具」读取下游 tools/list；新工具默认禁用，需手动启用。
6. 在「调用记录」查看发现和调用详情。页面显示最近 100 条记录，SQLite 保存全部记录。

内置的 Service Catalog 是 HTTP API，Observability 是使用官方 Go SDK 实现的原生 MCP 服务。示例调用实际经过对应适配器和本机 HTTP 请求，响应明确带有 `demo: true`。它们用于让原型开箱可操作。

## 模型配置

可以在网页填写，也可以用环境变量启动：

```sh
export GATEWAY_MODEL_BASE_URL='http://127.0.0.1:11434/v1'
export GATEWAY_MODEL='your-model-id'
# 需要认证的服务再设置 GATEWAY_MODEL_API_KEY
./start.sh
```

Base URL 自动补全 `/chat/completions`；也可直接填写完整 endpoint。Key 只保存在内存，不写数据库或浏览器存储，重启后重新填写或使用环境变量。设置中的 JSON Mode 控制是否发送 `response_format: {"type":"json_object"}`；不支持此字段的服务可关闭，返回内容仍会做 JSON 和候选工具 ID 校验。切换供应商时请同时更新或清除旧 Key。

路由过程是：按启用状态和模拟身份筛选 → 关键词候选检索 → 模型选择 → 校验返回 ID → 从注册表返回真实工具定义。检索器采用英文词和中文双字切分；模型选择器与检索器独立，便于以后替换为向量检索。

模型可返回 `selected`、`no_match`、`needs_context`，不会自行执行工具。任务和工具描述作为不可信数据传入，模型不决定授权。

MCP 和 CLI 只收到选中的工具定义；完整候选诊断留在工作台、调用记录，以及显式使用 `?debug=1` 的 HTTP 发现请求中。

## 接入本地 Agent

MCP Streamable HTTP endpoint：

```text
http://127.0.0.1:8788/mcp
```

客户端配置示例，具体字段按客户端调整：

```json
{
  "mcpServers": {
    "gateway-lab": {
      "type": "http",
      "url": "http://127.0.0.1:8788/mcp"
    }
  }
}
```

网关固定暴露两个 MCP 工具：

- `discover_tools({intent, context?, actor?, mode?})`：根据当前任务返回小范围真实工具定义。`mode` 可为 `model` 或 `retrieval`，省略时使用保存的配置。
- `invoke_tool({tool_id, arguments, fields?, actor?})`：再次检查工具开关、模拟身份及 JSON Schema，然后执行调用。

给 Agent 的建议指引：需要外部能力时先描述当前任务调用 discover_tools，按返回的 input_schema 填参数，再用 invoke_tool 执行；任务变化时重新发现。工具定义作为发现结果返回，通过固定 invoke_tool 执行，不依赖客户端动态注入新工具。

## 命令行 / Code Mode

```sh
./bin/gatewayctl list
./bin/gatewayctl -mode retrieval search '排查 billing 最近报错的原因'
./bin/gatewayctl -mode model -context '已知服务名为 billing' search '查错误日志'
./bin/gatewayctl -args '{"service":"billing","minutes":10}' \
  -fields 'logs.timestamp,logs.message' \
  call observability.search_error_logs > result.json
```

Flags 放在子命令之前。`-url` 可指定网关地址，`-actor observer` 可切换模拟身份。CLI 的 stdout 只有 JSON，方便重定向文件或用 jq 处理。

## 接入 HTTP API

在注册表添加 `kind: "http"` 服务，Endpoint 作为工具路径的基地址。添加工具时填写 `server_id`、`method`、`path` 和 JSON Schema。

- GET / DELETE：参数编码到 query string；复杂值编码为 JSON 字符串。
- POST / PUT / PATCH：参数作为 JSON 请求体。
- 当前没有自动导入 OpenAPI、路径参数模板或 OAuth；这些可以后续扩展。
- `headers` 只接受环境变量引用，例如 `{"Authorization":"Bearer ${SERVICE_TOKEN}"}`，实际密钥从 Go 进程环境读取。

原生 MCP 下游支持 Streamable HTTP，使用官方 MCP Go SDK 处理协议、初始化、session 与响应。当前不支持 stdio 下游、旧 HTTP+SSE transport 或交互式 OAuth。

## SQLite

首次运行创建 `data/gateway.db`，启用 WAL 和外键。`-data` 可改变数据库所在目录。数据模型：

| 表 | 保存内容 |
| --- | --- |
| servers | 服务 ID 和 JSON 定义 |
| tools | 工具 ID、所属服务和 JSON 定义 |
| settings | 不含 API Key 的模型和路由配置 |
| activity | 请求时间、模拟身份、状态、耗时及 JSON 详情 |

```sh
sqlite3 data/gateway.db '.tables'
sqlite3 data/gateway.db 'SELECT id, server_id FROM tools;'
sqlite3 data/gateway.db 'SELECT time, kind, status, title FROM activity ORDER BY time DESC LIMIT 10;'
```

注册表在运行时有内存快照；通过网页或 API 修改会事务写入 SQLite。直接编辑数据库后需重启进程。数据库及运行时文件均由 Git 忽略。

## HTTP API

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | /api/health | 运行状态 |
| GET | /api/bootstrap | Registry、脱敏配置、最近记录和 MCP 地址 |
| PUT | /api/settings | 保存模型配置；api_key 只更新内存 |
| PUT | /api/servers | 添加或更新完整服务定义 |
| PUT | /api/tools | 添加或更新完整工具定义 |
| POST | /api/servers/{id}/sync | 同步原生 MCP 工具 |
| POST | /api/discover | 工具发现 |
| POST | /api/invoke | 工具调用 |
| GET | /api/events | 最近 100 条记录 |

默认仅监听 `127.0.0.1`。「Developer / Observer」是方便实验的模拟身份，可以由请求参数指定；它不是登录认证。当前原型适用于可信本机使用。返回字段投影用于控制数据量，不是敏感信息脱敏或权限机制。

## 开发

```sh
# 终端 1：Go 后端，使用已有 web/dist
make run
# 终端 2：React 热更新，API 代理至 8788
cd web && npm run dev
# 完成修改后重新内嵌前端并生成两个程序
make build
```

```text
cmd/gateway        Go 网关入口
cmd/gatewayctl     CLI
internal/gateway  SQLite、Registry、检索、模型选择、协议适配与 API
web/src           React 工作台
web/dist          可直接内嵌的前端构建产物
```

参考：[Uber MCP Gateway](https://www.uber.com/pr/en/blog/designing-mcp-gateway/)、[官方 MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk)、[Chat Completions API](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create)。
