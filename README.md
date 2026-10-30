# Gateway Lab

一个可以直接运行的 MCP Gateway 原型。Go 后端、React + TypeScript 管理界面、SQLite 持久化；网关中的模型负责从候选工具里做选择，本地 Agent 继续负责规划和执行。新增的 Pi coding agent 在后台维护接入定义和适配代码，使服务变化能持续同步到路由模型的候选工具集合。

## 启动

当前目录已经构建了本机可执行文件：

```sh
cd mcp-gateway
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

首页「开始接入」按三个步骤引导：

1. **接入工具**：填写 MCP 地址或本地 stdio 启动命令，点击「保存并读取工具」。也可以粘贴单个现有 `mcpServers` 配置。检查工具描述和读写属性，勾选后点击「保存工具选择」。新导入工具默认禁用，表单中的勾选只是待保存的选择。普通 HTTP API 则先检查地址，再填写具体工具的方法、路径和参数 schema。
2. **配置模型**：填写兼容 Chat Completions 的 Base URL、模型 ID 和可选 API Key。「仅保存配置」不发起请求；「保存并测试路由」会用自己的工具定义发起一次真实模型请求，校验决策 JSON 和工具 ID，不执行下游工具。需要先启用至少一个自己的工具。更换配置、Key 或工具后，上次检查会显示需要重新测试。
3. **连接 Agent**：选择 Codex、Claude Code 或通用 JSON，复制命令或配置，在本地客户端应用。重新打开客户端会话后，直接在 Agent 里提出任务。页面只读取状态，在网关收到 MCP 初始化、工具列表或工具调用请求后显示记录；不会通过网页自测模拟完成接入。

三个步骤都可以先查看，未接入时进度保持未完成。连接失败会保留配置，显示错误详情，方便修改重试。HTTP 地址可达、工具已启用、模型已响应和 Agent 已发来请求分别显示，不代表下游任务一定成功。

原来的工作台位于「调试」：查看关键词候选召回、模型推荐理由、真实 schema，或手动试调用工具。可选字段如 `logs.timestamp,logs.message` 会裁剪结果。「工具注册表」保留完整 JSON 编辑器和同步入口，「高级设置」保留路由参数。「调用记录」显示最近 100 条记录，SQLite 保存全部记录。

内置的 Service Catalog 是 HTTP API，Observability 是使用官方 Go SDK 实现的原生 MCP 服务。示例调用实际经过对应适配器和本机 HTTP 请求，响应明确带有 `demo: true`。演示工具不计入接入进度，默认不参与发现。调试页选择内置示例会开启「包含演示工具」并切到无需模型的检索预览。

## 模型配置

可以在网页填写，也可以用环境变量启动：

```sh
export GATEWAY_MODEL_BASE_URL='http://127.0.0.1:11434/v1'
export GATEWAY_MODEL='your-model-id'
# 需要认证的服务再设置 GATEWAY_MODEL_API_KEY
./start.sh
```

Base URL 自动补全 `/chat/completions`；也可直接填写完整 endpoint。Key 只保存在内存，不写数据库或浏览器存储，重启后重新填写或使用环境变量。设置中的 JSON Mode 控制是否发送 `response_format: {"type":"json_object"}`；不支持此字段的服务可关闭，返回内容仍会做 JSON 和候选工具 ID 校验。切换供应商时请同时更新或清除旧 Key。

路由过程是：按启用状态和身份权限筛选 → 关键词候选检索 → 用紧凑能力摘要供模型选择 → 校验返回 ID → 从注册表按需返回真实 schema。检索器采用英文词和中文双字切分；模型选择器与检索器独立，便于以后替换为向量检索。

模型可返回 `selected`、`no_match`、`needs_context`，不会自行执行工具。任务和工具描述作为不可信数据传入，模型不决定授权。

MCP 和 CLI 只收到选中的工具定义；完整候选诊断留在工作台、调用记录，以及显式使用 `?debug=1` 的 HTTP 发现请求中。

## 持续维护：确定性导入与 Pi

Go 负责来源快照、调度、验收、发布和回退；Pi 负责需要编程的适配。通过 Pi RPC 接入，参考 `go-backend/runtime/pi-worker` 的职责划分，不依赖其部署。

1. 保存一个非演示 HTTP / MCP 服务，凭据使用 `${ENV_VAR}` 引用。
2. 在「自动维护」选来源格式：**OpenAPI** 确定性导入 HTTP 定义；**原生 MCP** 同步 `tools/list`；只有自定义 CI / 日志转换才选 **Pi**。OpenAPI 支持 JSON/YAML 3.0/3.1、局部 `$ref`、路径/查询/请求头/cookie、JSON body 和 JSON/文本响应；不支持的序列化、外部引用、multipart 会明确失败。可通过 operation ID 筛选能力。
3. 选择指定本地文件、直接文档 URL，或 Git 仓库的 ref 和相对文件路径。OpenAPI 使用第一个文档，不合并多份规格。Git 在网关自己的 bare 缓存内 fetch，不修改用户 checkout；凭据由本机 Git credential helper / SSH 配置提供。URL 文档不跟随重定向。
4. 配置**独立验收**。负责人提供请求/响应契约和路由案例；用例存在独立表中，不发送给 Pi。每个工具需要成功契约覆盖，每个启用或新增工具需要路由覆盖。`examples/gitlab-acceptance.json` 是可复用示例；共享模式的 routing.actor 必须指定配置中的真实身份 ID。
5. 点击「保存并检查」。通过验收后：新增/删除能力、扩大读写或权限、破坏参数/结果契约、改变请求目标、执行方式或适配器代码，进入待确认；兼容变更在开启自动发布时直接发布。**新工具在草稿阶段禁用，只在负责人确认发布后启用。** 已有工具保留原来的开关和 ACL。
6. 发布同时原子更新注册表和任务记录。路由模型从这一个注册表获得候选工具集合，不另维护 decision 选项。用例、路由模型/配置、权限、其他候选集合或当前服务发生变化后，旧验收不能用于新发布。

### Pi 与代码隔离

需要 **Pi 1.4+**（已配置模型/登录）、Docker 和 Node 沙箱镜像：

```sh
docker pull node@sha256:4b150b48b4116e2a22f2c39cc27025fbf4f153e2078fd3b14d159fdb5c7279da
# 可选，默认从 PATH 查找 pi
export GATEWAY_PI_COMMAND='/absolute/path/to/pi'
./start.sh
```

默认 `GATEWAY_EXECUTION_MODE=docker`，不会在 Docker 不可用时退回本机执行。纯 OpenAPI / MCP 定义同步不启动 Pi 或代码容器。维护模型与路由模型独立，可按服务覆盖 provider/model。

- Pi 的模型会话运行在宿主；自动加载的扩展、skills、MCP、项目指令关闭。网关只加载自己的受信任工具桥，关闭内建文件/shell 工具。
- `read/write/edit/bash` 在无网络容器内执行，只读挂载任务输入，在 128 MB tmpfs 工作目录内编码；完成后通过有大小上限的 JSON 导出普通产物文件。独立验收、网关数据库、Git checkout、宿主 HOME 和 Docker socket 均不挂载。容器使用非 root 用户、只读根文件系统、移除 capabilities、进程/内存/CPU 限额及临时目录。
- 模型凭据仅交给宿主 Pi。标准 provider Key 和 `PI_CODING_AGENT_DIR` 可用；自定义 provider 所需的环境变量要列在 `GATEWAY_PI_ENV`（逗号分隔变量名），不会传给编码容器。
- 生成的 `verify.mjs` 在无网络、只读版本挂载的容器中运行，可访问同容器内的 mock 服务。独立验收另行校验实际请求、响应和路由结果。
- 已发布 `.mjs` 适配器运行在单独的短生命周期容器，版本文件只读；服务 endpoint、参数及该服务凭据通过 stdin 注入。它有上游网络访问能力，**目前没有按目标主机限制的出网代理**；应使用最小权限服务令牌。宿主 loopback endpoint 在 Docker Desktop 中映射为 `host.docker.internal`。
- 超时/取消会终止进程组并移除容器；编码容器自身还有截止时间，宿主崩溃后也不会永久存活。版本文件哈希在验收、发布、调用时检查。
- 仅可信本机调试可显式使用 `GATEWAY_EXECUTION_MODE=local`；共享认证模式拒绝该选项。`GATEWAY_SANDBOX_IMAGE` 可指定自备包含 Node 22、bash、timeout 的镜像，建议固定 digest。Docker daemon 应由专用账户/主机管理。

适配器协议：stdin 一个 `{tool, arguments, endpoint, headers, env}` JSON；stdout 一个结果 JSON，非零退出为失败。只使用 Node 内置模块。产物为完整 `manifest.json`、顶层 `.mjs` 文件和生成的 `verify.mjs`。Pi 自己的验证成功并不足以发布，仍须通过独立验收。

### 调度、事件与来源追踪

每个服务最多一个排队/执行任务，当前全局串行。定时检查每 15 秒扫描，配置周期最短 1 分钟；未变化的来源不重复调用 Pi。配置、任务、来源 commit 和各文档哈希保存在 SQLite；任务目录为 `data/maintenance/<run-id>`。产物和历史目前不自动清理，同一 data 目录只运行一个进程。

支持 GitHub push/workflow_run、GitLab push/pipeline 和通用 release/deploy 事件。来源必须启用 `repository.events_enabled`，配置准确的 project、ref 和 `webhook_secret_env`。回调地址：

```text
POST /api/maintenance/sources/{server_id}/webhook/{github|gitlab|generic}
```

GitHub 用 `X-Hub-Signature-256` 和 `X-GitHub-Delivery`；GitLab 用 `X-Gitlab-Token` 和 `X-Gitlab-Event-UUID`。通用事件 body：`{"event":"release","project_id":"team/api","ref":"main","commit":"40-character-sha"}`，带 `X-Gateway-Delivery` 和 `X-Gateway-Signature: sha256=<HMAC-SHA256(body,secret)>`。共享部署通过 HTTPS 暴露回调，再在仓库配置 webhook；本项目不会自动修改远端仓库设置。

只有认证且项目/ref 匹配的事件入持久队列，重复 delivery 不重复执行，忙碌期间的同服务事件合并并在当前任务后补跑。事件 commit 是触发证据；构建始终重新读取配置 ref 的最新 commit，并分别记录触发 commit 和实际来源 commit。无关文件提交不重新生成工具。

重启恢复排队事件，运行中的任务标记中断。回退恢复本次发布前的完整工具集合；如果已被后续人工修改或新发布替代，则拒绝覆盖。任务保存发布/回退身份与时间。

### 维护 API

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | /api/maintenance | 运行时、来源、最近 50 次任务 |
| PUT | /api/maintenance/sources | 保存维护配置 |
| GET / PUT | /api/maintenance/sources/{id}/acceptance | 读取/保存独立验收 |
| POST | /api/maintenance/sources/{id}/run | 排队检查，body `{"force":false}` |
| POST | /api/maintenance/sources/{id}/webhook/{provider} | 验签并持久记录来源事件 |
| GET | /api/maintenance/runs/{id} | 版本差异、验收和审核原因 |
| GET | /api/maintenance/runs/{id}/artifacts/{name} | 已验证文件 |
| POST | /api/maintenance/runs/{id}/cancel | 取消任务 |
| POST | /api/maintenance/runs/{id}/publish | 明确确认并发布，通过验收是前提 |
| POST | /api/maintenance/runs/{id}/rollback | 回退本次发布 |

来源示例：

```json
{
  "server_id": "gitlab", "format": "openapi",
  "files": ["/absolute/path/to/mcp-gateway/examples/gitlab-openapi.yaml"],
  "urls": [], "operation_ids": ["pipelines", "jobs", "job_trace"],
  "enabled": true, "auto_publish": true,
  "interval_minutes": 60, "timeout_seconds": 900
}
```

Git 来源可替换 files 为 `repository: {"url":"https://git.example/team/api.git","ref":"main","files":["openapi.yaml"],"project":"team/api","events_enabled":true,"webhook_secret_env":"SOURCE_WEBHOOK_SECRET"}`。

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

网关固定暴露三个 MCP 工具：

- `discover_tools({intent, context?, actor?, mode?, summary_only?})`：根据当前任务返回小范围真实工具定义。`mode` 可为 `model` 或 `retrieval`，省略时使用保存的配置。
- `invoke_tool({tool_id, arguments, fields?, actor?})`：再次检查工具开关、真实身份权限及 JSON Schema，然后执行调用。

- `get_tool_schema({tool_id, actor?})`：按权限获取单个工具完整 schema。`discover_tools` 传 `summary_only:true` 可先只拿摘要，降低上下文占用。

给 Agent 的建议指引：需要外部能力时先描述当前任务调用 discover_tools，按返回的 input_schema 填参数，再用 invoke_tool 执行；任务变化时重新发现。工具定义作为发现结果返回，通过固定 invoke_tool 执行，不依赖客户端动态注入新工具。

## 命令行 / Code Mode

```sh
./bin/gatewayctl list
./bin/gatewayctl -summary -mode retrieval search 'CI job log trace'
./bin/gatewayctl schema gitlab.job_trace
./bin/gatewayctl -demo -mode retrieval search '排查 billing 最近报错的原因'
./bin/gatewayctl -mode model -context '已知服务名为 billing' search '查错误日志'
./bin/gatewayctl -args '{"service":"billing","minutes":10}' \
  -fields 'logs.timestamp,logs.message' \
  call observability.search_error_logs > result.json
```

Flags 放在子命令之前。`-url` 可指定网关地址，本机无认证模式的 `-actor observer` 可切换模拟身份；共享模式用 `GATEWAY_TOKEN`，客户端 actor 不改变真实身份。CLI 的 stdout 只有 JSON，方便重定向文件或用 jq 处理。

## 接入 HTTP API

在注册表添加 `kind: "http"` 服务，Endpoint 作为工具路径的基地址。添加工具时填写 `server_id`、`method`、`path` 和 JSON Schema。

- GET / DELETE：参数编码到 query string；复杂值编码为 JSON 字符串。
- POST / PUT / PATCH：参数作为 JSON 请求体。
- 普通配置映射不解析路径模板。可在「自动维护」提供 OpenAPI 或源码资料，让 Pi 生成工具定义；动态路径由生成的适配脚本处理。当前不支持交互式 OAuth。
- `headers` 只接受环境变量引用，例如 `{"Authorization":"Bearer ${SERVICE_TOKEN}"}`，实际密钥从 Go 进程环境读取。

## 接入本地 stdio MCP

首页选择「本地进程（stdio）」，分别填写命令和 JSON 字符串数组参数。由 Go 进程直接启动，不经过 shell；命令必须在网关进程的 PATH 中，或使用绝对路径。`npx`、`uvx` 等命令需要在本机预先可用，首次安装依赖时可能需要先在终端运行。

完整服务定义示例：

```json
{
  "id": "local-tools",
  "name": "本地工具",
  "kind": "mcp",
  "transport": "stdio",
  "command": "/absolute/path/to/mcp-server",
  "args": ["--workspace", "/absolute/path/to/workspace"],
  "work_dir": "/absolute/path/to/workspace",
  "env": {"API_KEY": "${MY_SERVICE_KEY}"},
  "enabled": true
}
```

`env` 的值和 HTTP `headers` 一样使用 `${ENV_VAR}` 引用；密钥由启动网关的 shell 提供，修改后需重启网关。stdio 进程会在导入和连续调用之间保留，配置改变后下次连接会重新启动，网关退出时关闭。进程断线后不自动重试工具调用，以免重复执行；可以重新读取工具后再试。

原生 MCP 下游支持 Streamable HTTP 和 stdio，使用官方 MCP Go SDK 处理协议、初始化、session 与响应。当前不支持旧 HTTP+SSE transport 或交互式 OAuth。

## SQLite

首次运行创建 `data/gateway.db`，启用 WAL 和外键。`-data` 可改变数据库所在目录。数据模型：

| 表 | 保存内容 |
| --- | --- |
| servers | 服务 ID 和 JSON 定义 |
| tools | 工具 ID、所属服务和 JSON 定义 |
| settings | 不含 API Key 的模型和路由配置 |
| activity | 请求时间、实际身份（本机模式为模拟身份）、状态、耗时及 JSON 详情 |
| connection_checks | 来源及模型的最近检查结果、时间和配置指纹，不保存凭据 |
| maintenance_sources | 每个服务的来源、调度、维护指引及发布设置 |
| maintenance_runs | 版本、来源 commit、验收报告、发布/回退身份和时间、产物哈希 |
| maintenance_acceptance | 独立的负责人契约与路由验收用例 |
| maintenance_deliveries | 签名来源事件、delivery 去重和任务分配 |

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
| GET | /api/setup/status | 三步接入状态、检查结果和最近 MCP 客户端请求 |
| POST | /api/setup/source | 保存来源并检查连接；MCP 同时读取工具 |
| POST | /api/setup/model | 保存模型配置并发起真实路由检查 |
| PUT | /api/settings | 保存模型配置；api_key 只更新内存 |
| PUT | /api/servers | 添加或更新完整服务定义 |
| PUT | /api/tools | 添加或更新完整工具定义 |
| POST | /api/servers/{id}/sync | 同步原生 MCP 工具 |
| POST | /api/servers/{id}/tools | 批量保存当前服务的启用工具 ID：`{"ids":[...]}` |
| POST | /api/discover | 工具发现 |
| POST | /api/invoke | 工具调用 |
| GET | /api/events | 最近 100 条记录 |
| GET | /api/catalog | 当前身份可见的能力摘要 |
| GET | /api/tools/{id}/schema | 授权后的单个 schema |
| POST | /api/auth/login | 管理令牌换取 HttpOnly 登录会话 |
| POST | /api/auth/logout | 注销当前会话 |
| GET | /api/auth/me | 当前身份 |

## 共享模式与访问控制

默认仅监听 loopback，无认证时 Developer / Observer 仍是本机模拟身份。非 loopback 监听必须配置 `GATEWAY_AUTH_FILE`，示例见 `examples/access.json`；配置每个主体的 ID、groups、服务/工具范围、是否允许写操作、管理权限，以及 `token_env` 或令牌的 SHA256（不能同时设置）。令牌至少 24 字节，建议随机生成。

```sh
export GATEWAY_AUTH_FILE=/absolute/path/to/access.json
# GATEWAY_ADMIN_TOKEN 和 GATEWAY_READER_TOKEN 由部署环境注入
export GATEWAY_PUBLIC_URL=https://gateway.example.com
./start.sh -addr 0.0.0.0:8788
```

在 HTTPS 反向代理后运行。网页通过管理员令牌建立 8 小时 HttpOnly / SameSiteStrict 会话，不用 localStorage 保存令牌。MCP / CLI 带 `Authorization: Bearer <token>`；CLI 可设置 `GATEWAY_TOKEN`。认证后忽略请求中的 actor，按主体 groups、servers、tools 和 allow_write 重新检查发现、schema 和执行；admin 也不绕过工具 ACL。管理 API 仅管理员可用。访问配置和令牌变更需要重启，会话随进程失效。

服务 headers/env 只保存环境变量引用；stdio 子进程只继承最小运行环境和明确声明的 env。返回结果默认遮盖凭据字段、当前服务凭据值，还可设置 `redact_fields:["user.email","logs.customer_id"]` 与 `redact_emails:true`。脱敏先于字段投影。它不自动识别所有业务 PII；由服务负责人配置字段。调用日志不保存返回数据或请求参数。共享模式默认不信任开发服务器 Origin，确需跨来源管理界面时显式设置 `GATEWAY_TRUSTED_ORIGINS`。

这是单机部署的最小共享认证层，尚未接入公司 SSO / OIDC、外部策略系统或多机任务调度。

## 验证

```sh
go test -race ./...
go vet ./...
# 本机 Pi + 本地模拟模型；不消耗云端模型额度
GATEWAY_TEST_REAL_PI=1 go test -race ./internal/gateway -run TestPiRealRPCContract -v
# 真实 Docker 文件/网络隔离、适配器验收及 Pi 工具执行
GATEWAY_TEST_DOCKER=1 GATEWAY_TEST_REAL_PI=1 go test -race ./internal/gateway -run 'TestDockerExecution|TestPiReal' -v
# 已运行的 go-backend：真实 OpenAPI + healthz/readyz（只读）
GATEWAY_TEST_BACKEND_URL=http://127.0.0.1:8080 go test ./internal/gateway -run TestLiveBackendOpenAPI -v
# 自己的 GitLab CI / 日志，所有网络操作均为 GET，令牌由环境注入
GATEWAY_TEST_GITLAB_URL=https://git.example/api/v4 GATEWAY_TEST_GITLAB_PROJECT=team/api \
  go test -race ./internal/gateway -run TestLiveGitLabMaintenanceChangeAndRollback -v
# 上一个命令另需 GATEWAY_TEST_GITLAB_TOKEN；测试不输出令牌或日志正文
make build
```

GitLab 测试使用临时数据目录：导入示例规格 → 负责人契约与路由验收 → 确认首次发布 → 查询真实流水线、任务和日志 → 修改描述 → 自动发布兼容变更 → 验证路由 → 回退。不修改实际项目、流水线或用户网关数据。默认测试另覆盖签名事件去重/重启恢复、配置漂移、发布竞争、身份伪造、脱敏、取消和失败路径。

## 开发

界面使用本地打包的 DM Sans 和 IBM Plex Mono 字体，运行时不依赖外部字体服务；OFL 许可证保存在 `web/public/fonts/`。界面动效遵循系统的减少动态效果偏好。

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
