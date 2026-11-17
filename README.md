# Gateway Lab

一个可以直接运行的 MCP Gateway。Go 后端、MyGo 原生桌面端、SQLite 持久化；网关中的模型负责从可用工具里做选择，本地 Agent 继续负责规划和执行。支持 Repo × Agent 仓库级权限隔离（`gateway-hook`）、6 大主流客户端官方插件、MCP 市场、浏览器 OAuth 2.0、OpenAPI 原生 HTTP 参数映射与 Pi 后台自动维护。整体分层与运行时架构见 [`docs/architecture.md`](docs/architecture.md)。

## 启动

管理界面只有原生桌面端（MyGo，`cmd/gateway-desktop`）。桌面端在进程内启动网关并连接其 HTTP API，默认监听 `127.0.0.1:8789`（Dev）。

```sh
make setup    # 下载 Go 依赖
make dev      # 运行 Dev 原生桌面端
```

无界面部署使用 `cmd/gateway`，只提供 HTTP API 与 MCP 入口，没有网页：

```sh
./start.sh                          # 构建并启动 Dev 网关，默认 127.0.0.1:8789
./start.sh -addr 127.0.0.1:8888     # 更换端口
curl http://127.0.0.1:8789/api/health
```

MCP 入口为 `http://127.0.0.1:8789/mcp`，管理 API 位于 `/api/*`，`gatewayctl`、`routingeval`、`gateway-hook` 和桌面端都通过它们访问网关。访问根路径 `/` 只返回指向 `/mcp` 与 `/api/health` 的 404 JSON。

从源码构建需要 Go 1.27+；打包桌面端和发布脚本（`scripts/*.mjs`）需要 Node.js 22.12+。如果依赖下载受网络影响，可以使用 `GOPROXY=https://goproxy.cn,direct`。

## 桌面控制台（9 个页面）

MyGo 原生桌面端（`cmd/gateway-desktop`）完全通过 `/api/*` 与后端通信，提供 9 个顶层功能页面：

1. **运行大盘（`overview`）**：三步接入向导（接入服务、配置模型、连接 Agent）、近 12 小时运行脉冲（48 个 15 分钟请求/错误分桶）、运行健康度、最近 MCP 请求状态与快捷导航。
2. **上游服务（`services`）**：管理 MCP stdio、MCP Streamable HTTP（支持浏览器 OAuth）、手动 HTTP REST 与 OpenAPI 导入服务；支持白名单勾选启用、工具编辑与自动维护配置。新导入工具默认禁用，需手动启用后才进入路由候选。
3. **MCP 市场（`market`）**：浏览内置精选与在线 MCP Registry（`GET /api/market`），一键安装（`POST /api/market/install`）并按服务保存 Header/Env 凭据（macOS 桌面端使用系统 Keychain）。
4. **工具目录（`catalog`）**：按服务、只读/写属性跨服务浏览当前所有已启用工具的摘要与 Schema。
5. **仓库绑定（`repos`）**：管理仓库锚点（Git `common_dir` / 非 Git `root_paths`）、`(repo_key, agent_key)` 权限矩阵、预览各 Agent 可达工具、复制 Hook 片段并开关 `enforce` 仓库隔离模式。
6. **连接客户端（`clients`）**：一键向 Codex CLI、Claude Code、Cursor、OpenCode v2、Pi、Antigravity 安装/更新/卸载官方插件（含 `gateway-hook` 集成）或写入用户级 MCP 配置。
7. **访问与路由（`settings`）**：配置并测试 Decisions 模型、管理多组已保存的模型档案（`model_profiles`）、调整 `max_tools` / 超时与认证策略。
8. **请求日志（`logs`）**：查看最近请求、作用域归因（`scope`）、耗时与脱敏后详情（SQLite 保存全部历史）。
9. **路由沙盒（`playground`）**：输入自然语言意图（及可选 `sim_scope` 模拟仓库/Agent 视角）实时调试 Decisions 相关性概率、查看真实 Schema 并手动试调用工具（支持 `fields` 响应裁剪）。

内置的 Service Catalog（HTTP API）与 Observability（Go SDK 原生 MCP）示例服务实际经过对应适配器运行，响应带有 `demo: true`；演示工具默认不参与发现，在沙盒勾选「包含演示工具」时启用。

## 模型配置

可以在桌面端「访问与路由」页填写并管理多组已保存的模型档案（`model_profiles`），也可以用环境变量启动：

```sh
export GATEWAY_MODEL_BASE_URL='https://api.openai.com/v1'
export GATEWAY_MODEL='gpt-6-luna'
export GATEWAY_MODEL_API_KEY='你的 OpenAI API Key'
./start.sh
```

支持三种 Decisions 协议，按模型 ID 与地址自动选择：

- **OpenAI Decisions**：默认协议，Base URL 自动补全 `/decisions`，使用 Bearer 认证（如 `gpt-6-luna`）。
- **Google Gemini Decisions**：模型 ID 以 `gemini-` 开头（如 `gemini-decision-1`），Base URL 自动补全 `/decisions`，通过 `x-goog-api-key` 认证。
- **SystemOne**：模型 ID 为 `jev` / `jev-*` / `decision-model-*` 或 Base URL 以 `/systemone` 结尾（如 `.../v1` 自动补全或直填 `.../v1/systemone`），按单次最多 16 题自动分片评估 `"noul"` / `"choice"` 问题。

桌面端「测试连通性」（`POST /api/model/ping`）校验通过后可将配置保存/切换为多组 `ModelProfile`（`PUT /api/model-profiles`、`POST /api/model-profiles/{id}/activate`）。macOS 桌面端将界面保存的 API Key 存入系统 Keychain，按模型档案隔离，重启后自动恢复；密钥不写入 SQLite。启动时的 `GATEWAY_MODEL_API_KEY` 临时覆盖当时激活的档案，界面显式更新或清除后以本次操作为准，该环境变量不会被自动持久化。路由只调用 Decisions/SystemOne 接口，不调用 Chat Completions 或 JSON Mode；更换配置、Key 或启用工具后，已有验证指纹会失效并提示重新测试。

路由分三步，默认全开；`GATEWAY_ROUTING=flat` 退回只有第二步的平铺路由，`GATEWAY_ROUTING=hierarchical` 或 `refine` 只开其中一步：

1. **按服务收窄**（`hierarchical`）：把每个服务的描述和工具名列表交给模型，按服务提问，只有被选中的服务（概率 ≥ 0.5，最多 3 个；都不够就取最高的一个）的工具进入下一步。单次请求的候选数由最大的服务决定，而不是全部工具之和；返回结果多一个 `servers` 字段。只有一个服务时不多发请求。
2. **逐工具是非题**：每个候选一道独立的问题，概率 ≥ 0.5 的过线。
3. **精选**（`refine`）：过线的候选（最多 8 个）再问一道 choice 题「哪一个最直接完成任务」，附带「都不是」选项。返回胜出的工具，以及分布占比不低于胜者一半的其他候选，所以两个同样直接的工具会一起返回，相邻但次要的（trace 对 errors、schema 对 query）不会。「都不是」胜出时：若第二步已有 ≥ 0.9 的候选（任务需要两个工具、或几个服务各有一个同样对的工具时常见），仍按分布返回；若过线候选来自多个服务，返回 `needs_context` 并在 `question` 里列出这些服务；否则 no_match。

`discover_tools` 的 `services` 参数可以限定在哪些服务里找；给了它就跳过第一步。策略全文见 `docs/routing.md`，评测数据见 `evals/routing/NOTES.md`。

每次请求包含一个路由状态 choice 问题，以及每个可用工具一个相关性 predicate 问题。网关校验回答数量、名称、顺序、类型和概率范围；相关性概率至少 0.5 的工具按概率降序选取，最多返回配置数量。该阈值是当前固定策略，仍需用实际项目验收路由质量。路由状态问题不否决高相关性的工具：只要有工具过阈值就返回它们，状态仅在没有工具过阈值时区分 `no_match` 与 `needs_context`（后者使用网关提供的补充上下文提示）；推荐理由展示概率，不由模型生成自由文本。任一问题拒答或响应不完整均报错，不执行工具或退回其他模型接口。

路由过程是：按启用状态和身份权限筛选 → 把所有可用工具的紧凑能力摘要连同任务意图发给 Decisions 模型 → 校验回答并映射为工具 ID → 从注册表按需返回真实 schema。没有本地预筛选，必须先配置模型和 Key。每个工具对应一个问题，成本和延迟随工具数量增长，工具很多时需要关注规模。

模型可返回 `selected`、`no_match`、`needs_context`，不会自行执行工具。任务和工具描述作为不可信数据传入，模型不决定授权。

MCP 和 CLI 只收到选中的工具定义；完整诊断留在工作台、调用记录，以及显式使用 `?debug=1` 的 HTTP 发现请求中。

## 持续维护：确定性导入与 Pi

Go 负责来源快照、调度、验收、发布和回退；Pi 负责需要编程的适配。通过 Pi RPC 接入，参考 `go-backend/runtime/pi-worker` 的职责划分，不依赖其部署。

1. 保存一个非演示 HTTP / MCP 服务，凭据使用 `${ENV_VAR}` 引用。
2. 在「自动维护」选来源格式：**OpenAPI** 确定性导入 HTTP 定义；**原生 MCP** 同步 `tools/list`；只有自定义 CI / 日志转换才选 **Pi**。OpenAPI 支持 JSON/YAML 3.0/3.1、局部 `$ref`、路径/查询/请求头/cookie、JSON body 和 JSON/文本响应；不支持的序列化、外部引用、multipart 会明确失败。可通过 operation ID 筛选能力。
3. 选择指定本地文件、直接文档 URL，或 Git 仓库的 ref 和相对文件路径。OpenAPI 使用第一个文档，不合并多份规格。Git 在网关自己的 bare 缓存内 fetch，不修改用户 checkout；凭据由本机 Git credential helper / SSH 配置提供。URL 文档不跟随重定向。
4. 配置**独立验收**。负责人提供请求/响应契约和路由案例；用例存在独立表中，不发送给 Pi。每个工具需要成功契约覆盖，每个启用或新增工具需要路由覆盖。`examples/gitlab-acceptance.json` 是可复用示例；共享模式的 routing.actor 必须指定配置中的真实身份 ID。
5. 点击「保存并检查」。通过验收后：新增/删除能力、扩大读写或权限、破坏参数/结果契约、改变请求目标、执行方式或适配器代码，进入待确认；兼容变更在开启自动发布时直接发布。**新工具在草稿阶段禁用，只在负责人确认发布后启用。** 已有工具保留原来的开关和 ACL。
6. 发布同时原子更新注册表和任务记录。路由模型从这一个注册表获得可用工具集合，不另维护 decision 选项。用例、路由模型/配置、权限、其他可用工具集合或当前服务发生变化后，旧验收不能用于新发布。

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

## 桌面凭据保存

macOS 桌面端默认使用系统 Keychain。模型密钥按档案保存；上游服务在「连接与认证配置 → 服务凭据」中为 `${ENV_VAR}` 引用填写值，点击「保存凭据」。留空保留原值，清除按钮删除已保存值。市场添加服务时填写的密钥也按服务保存。输入框不回显已有密钥。

凭据命名空间由构建渠道和规范化数据目录决定；Dev、Stable、独立 worktree 与临时测试目录不会共用。更新应用但沿用相同渠道和数据目录时仍可恢复。历史环境变量引用不变；需要在服务凭据栏重新填写一次，已有进程环境不会被自动导入。移除引用、删除服务或模型档案会清理相应保存值。系统钥匙串不可访问时明确报错，不回退到明文文件。

无界面后端及非 macOS 默认使用内存与环境变量。`GATEWAY_CREDENTIAL_STORE=keychain` 可在 macOS 无界面部署启用钥匙串，`GATEWAY_CREDENTIAL_STORE=memory` 可显式使用内存模式（重启不保留），其他值会报错。远程 MCP 浏览器 OAuth 会话仍使用原有进程内 TokenSource；本节保存的是模型 Key 与服务 Header/Env 凭据。

## 接入本地 Agent

MCP Streamable HTTP endpoint：

```text
http://127.0.0.1:8789/mcp
```

客户端配置示例，具体字段按客户端调整：

```json
{
  "mcpServers": {
    "gateway-lab": {
      "type": "http",
      "url": "http://127.0.0.1:8789/mcp"
    }
  }
}
```

网关固定暴露三个 MCP 工具：

- `discover_tools({intent, context?, actor?, summary_only?})`：根据当前任务返回小范围真实工具定义，由配置的模型从全部可用工具中选择。
- `invoke_tool({tool_id, arguments, fields?, actor?})`：再次检查工具开关、真实身份权限及 JSON Schema，然后执行调用。

- `get_tool_schema({tool_id, actor?})`：按权限获取单个工具完整 schema。`discover_tools` 传 `summary_only:true` 可先只拿摘要，降低上下文占用。

给 Agent 的建议指引：需要外部能力时先描述当前任务调用 discover_tools，按返回的 input_schema 填参数，再用 invoke_tool 执行；任务变化时重新发现。工具定义作为发现结果返回，通过固定 invoke_tool 执行，不依赖客户端动态注入新工具。

## 命令行 / Code Mode 与官方客户端插件

```sh
./bin/dev/gatewayctl list
./bin/dev/gatewayctl -summary search 'CI job log trace'
./bin/dev/gatewayctl schema gitlab.job_trace
./bin/dev/gatewayctl -demo search '排查 billing 最近报错的原因'
./bin/dev/gatewayctl -context '已知服务名为 billing' search '查错误日志'
./bin/dev/gatewayctl -args '{"service":"billing","minutes":10}' \
  -fields 'logs.timestamp,logs.message' \
  call observability.search_error_logs > result.json
```

Flags 放在子命令之前。`-url` 可指定网关地址，本机无认证模式的 `-actor observer` 可切换模拟身份；共享模式用 `GATEWAY_TOKEN`，客户端 actor 不改变真实身份。CLI 的 stdout 只有 JSON，方便重定向文件或用 jq 处理。

`gatewayctl plugin` 与桌面端「连接客户端」共享 [`internal/clientplugin`](internal/clientplugin) 引擎（模板内嵌自 [`plugins/`](plugins)），支持为 **Codex CLI、Claude Code、Cursor、OpenCode v2、Pi、Antigravity** 6 个客户端管理官方插件与用户级 MCP 配置：

```sh
./bin/dev/gatewayctl plugin status
./bin/dev/gatewayctl plugin install -client claude
./bin/dev/gatewayctl plugin update -client all
./bin/dev/gatewayctl plugin remove -client cursor
```

## 仓库级工作区作用域（Repo × Agent Scoping）

网关支持按「本地代码仓库 × Coding Agent」细粒度限制可发现与可调用的下游工具集（详见 [`docs/architecture.md`](docs/architecture.md)）：

- **本机 Hook 签名（`./bin/dev/gateway-hook`）**：各客户端官方插件在工具调用前调用 `gateway-hook`（支持 `claude`、`codex`、`cursor`、`antigravity` 宿主模式及 `opencode`/`pi` 的 `sign` 子命令），识别当前会话 `cwd` 与 Git `common_dir`，使用 `<data>/scope.key` 对请求参数规范化哈希后签发 60 秒有效的 `v1` HMAC-SHA256 上下文凭证（注入渠道隔离字段 `_gw_ctx` 或 `_gw_ctx_mcp_gateway_dev`）。
- **服务端校验与优先级**：网关结合主体 RBAC 与 `(repo_key, agent_key)` 四级整条覆盖规则（`(R, agent) > (R, *) > (*, agent) > (*, *)`）求取严格交集，并通过 `jtiCache` 防止凭证重放。
- **Fail-open 与 Enforce 模式**：`gateway-hook` 遇到本机异常或未初始化密钥时默认 fail-open 以免卡死 Agent；桌面端「仓库绑定」或 `PUT /api/scopes/mode` 开启 `enforce: true` 后，服务端对缺失、多根歧义或无效凭证一律以 `403` 拒绝（`scope_required` / `ambiguous_workspace` / `scope_invalid`）。

## 接入 HTTP API 与 OpenAPI 原生映射

在注册表添加 `kind: "http"` 服务，Endpoint 作为工具路径的基地址。既可手动添加工具，也可通过「上游服务 -> OpenAPI 导入」或 `POST /api/servers/openapi` 解析 OpenAPI 3.0/3.1（JSON/YAML）文档自动生成服务与工具：

- **原生参数映射（`internal/upstream/http_mapping.go`）**：完整支持路径模板参数（path）、query 参数（含 `deepObject`）、请求头（header）、Cookie、JSON body 以及基于 OpenAPI 声明状态码与 JSON Schema 的响应校验（`application/json`、`text/plain`、`empty`）。
- **手动 HTTP 工具**：GET / DELETE 参数编码到 query string；POST / PUT / PATCH 参数作为 JSON 请求体。
- **`headers` 凭据占位符**：只接受环境变量引用，例如 `{"Authorization":"Bearer ${SERVICE_TOKEN}"}`，实际密钥在出站时从进程内存或环境解析。

## 接入本地与远程 MCP 服务

### 只读服务器容器日志

`make backend` 同时构建 `bin/dev/gateway-sshlogs`，它是一个本地 stdio MCP 服务，通过已有 SSH 身份读取指定远端容器的 stdout/stderr，不安装远端服务或开放新端口。在「上游服务」选择「本地进程（stdio）」，命令使用该程序的绝对路径，参数示例：

```json
["-host", "服务器 IP", "-user", "SSH 用户", "-identity", "/absolute/path/to/private-key", "-service", "backend=backend-container", "-service", "web=web-container"]
```

要求 SSH 密钥可免交互使用，主机密钥已通过可信渠道加入本机 known_hosts。每个 `-service` 指定一个固定的别名与容器名映射；工具参数不能改变主机、用户、容器映射或命令。容器重建后名称变化时更新映射并重新读取工具。此 MCP 只提供 `list_log_services` 和 `read_service_logs`，保存并读取后启用它们。

`read_service_logs({service, minutes?, limit?, query?})` 默认读取最近 15 分钟的最后 100 行，最多 24 小时、500 行、512 KiB；SSH 调用超时 20 秒。`query` 是本地大小写不敏感的字面子串筛选，只筛选这批 tail 日志，不扫描完整历史。返回结果标明范围、行数和字节截断；日志不存在、已轮转或在旧容器中时不能据此断言服务没有发生错误。JSON 敏感字段、常见文本凭据和邮箱会做基础脱敏，不能保证识别全部业务敏感信息。SSH 凭据不传给模型，远端账户本身的权限仍由部署方管理。

### 其他 stdio 服务

在「上游服务」选择「本地进程（stdio）」，分别填写命令和 JSON 字符串数组参数。由 Go 进程直接启动，不经过 shell；命令必须在网关进程的 PATH 中，或使用绝对路径。`npx`、`uvx` 等命令需要在本机预先可用，首次安装依赖时可能需要先在终端运行。

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

`env` 的值和 HTTP `headers` 一样使用 `${ENV_VAR}` 引用；密钥由启动网关的环境变量或按服务保存的凭据提供，环境变量优先。stdio 进程会在导入和连续调用之间保留，配置改变后下次连接会重新启动，网关退出时关闭。进程断线后不自动重试工具调用，以免重复执行；可以重新读取工具后再试。

### 远程 Streamable HTTP MCP 与浏览器 OAuth

原生 MCP 下游支持 Streamable HTTP 和 stdio，使用官方 MCP Go SDK 处理协议、初始化、session 与响应。对于开启 `oauth: true` 的远程 Streamable HTTP MCP 服务（包括通过「MCP 市场」一键安装的需要登录的远程服务），网关支持**交互式浏览器 OAuth 2.0 + PKCE 与动态客户端注册（DCR）**：

- 同步或调用触发未授权响应时，网关自动在本机 loopback 临时端口启动回调服务并拉起系统浏览器完成登录；
- 校验授权 URL 仅允许 `https`（或本机 `http` loopback），获取的 `oauth2.TokenSource` 仅驻留进程内存，不写入 SQLite；
- 当前不支持旧版 HTTP+SSE transport。

## SQLite

首次运行创建 `data/gateway.db`，启用 WAL 和外键。`-data` 可改变数据库所在目录。全部 14 张数据表如下：

| 表 | 保存内容 |
| --- | --- |
| servers | 服务 ID、排序 `position` 和 JSON 定义 |
| tools | 工具 ID、所属服务、排序 `position` 和 JSON 定义 |
| settings | 不含 API Key 的当前激活模型和路由配置 |
| model_profiles | 已保存的多组 Decisions 模型配置档案（不含 API Key） |
| activity | 请求时间、实际身份（本机模式为模拟身份）、状态、耗时及脱敏后的 JSON 详情 |
| connection_checks | 来源及模型的最近检查结果、时间和配置指纹，不保存凭据 |
| maintenance_sources | 每个服务的来源、调度、维护指引及发布设置 |
| maintenance_runs | 版本、来源 commit、验收报告、发布/回退身份和时间、产物哈希 |
| maintenance_acceptance | 独立的负责人契约与路由验收用例 |
| maintenance_deliveries | 签名来源事件、delivery 去重和任务分配 |
| scope_state | 全局仓库隔离拦截开关（`enforce`） |
| repositories | 已绑定仓库实体及本地 Git `common_dir` / `root_paths` 锚点 |
| scope_rules | `(repo_key, agent_key)` 二维作用域授权矩阵规则 |
| scope_observations | 来自已验签 Hook 的未绑定工作区观测记录 |

```sh
sqlite3 data/gateway.db '.tables'
sqlite3 data/gateway.db 'SELECT id, server_id FROM tools;'
sqlite3 data/gateway.db 'SELECT time, kind, status, title FROM activity ORDER BY time DESC LIMIT 10;'
```

注册表在运行时有内存快照；通过桌面端或 API 修改会事务写入 SQLite。直接编辑数据库后需重启进程。数据库及运行时文件均由 Git 忽略。

## HTTP API

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | /api/health | 运行状态 |
| GET | /api/bootstrap | Registry、脱敏配置、最近记录、12h 运行脉冲和 MCP 地址 |
| GET | /api/setup/status | 三步接入状态、检查结果和最近 MCP 客户端请求 |
| POST | /api/setup/source | 保存来源并检查连接；MCP 同时读取工具 |
| POST | /api/setup/model | 保存模型配置并发起真实路由检查 |
| PUT | /api/settings | 保存模型配置与密钥（桌面端使用系统 Keychain） |
| PUT | /api/servers | 添加或更新完整服务定义 |
| PUT | /api/tools | 添加或更新完整工具定义 |
| POST | /api/servers/{id}/sync | 同步原生 MCP 工具 |
| POST | /api/servers/{id}/tools | 批量保存当前服务的启用工具 ID：`{"ids":[...]}` |
| POST | /api/discover | 工具发现 |
| POST | /api/invoke | 工具调用 |
| GET | /api/events | 最近 100 条记录 |
| GET | /api/catalog | 当前身份可见的能力摘要 |
| GET | /api/tools/{id}/schema | 授权后的单个 schema |
| POST | /api/model/ping | 测试模型连通性（不保存配置） |
| PUT | /api/model-profiles | 保存或更新模型档案（需先通过连通性测试） |
| POST | /api/model-profiles/{id}/activate | 激活指定模型档案 |
| DELETE | /api/model-profiles/{id} | 删除模型档案 |
| GET | /api/market | 检索可安装的 MCP 市场服务 |
| GET / PUT | /api/servers/{id}/credentials | 查看凭据状态（不返回值）/ 更新或清除凭据 |
| DELETE | /api/servers/{id} | 删除服务、工具和已保存凭据 |
| POST | /api/market/install | 安装市场服务并按服务保存凭据 |
| GET | /api/scopes | 仓库绑定、作用域规则与观测记录 |
| PUT | /api/scopes/mode | 开关 `enforce` 仓库隔离拦截模式 |
| PUT / DELETE | /api/scopes/repositories | 新增、更新或删除仓库锚点 |
| PUT / DELETE | /api/scopes/rules | 新增、替换或删除 `(repo_key, agent_key)` 规则 |
| POST | /api/scopes/preview | 预览仓库下各 Agent 可达工具 |
| POST | /api/auth/login | 管理令牌换取 HttpOnly 登录会话 |
| POST | /api/auth/logout | 注销当前会话 |
| GET | /api/auth/me | 当前身份 |
| POST | /api/servers/openapi | 由服务端解析 OpenAPI 文档（内容或 URL），注册 HTTP 服务及其工具 |

上表只列常用接口。完整契约（全部路由、请求/响应结构、错误格式、鉴权要求）见 [docs/api](docs/api/README.md) 下的 `openapi.json`，由契约测试守护。

## 共享模式与访问控制

默认仅监听 loopback，无认证时 Developer / Observer 仍是本机模拟身份。非 loopback 监听必须配置 `GATEWAY_AUTH_FILE`，示例见 `examples/access.json`；配置每个主体的 ID、groups、服务/工具范围、是否允许写操作、管理权限，以及 `token_env` 或令牌的 SHA256（不能同时设置）。令牌至少 24 字节，建议随机生成。

```sh
export GATEWAY_AUTH_FILE=/absolute/path/to/access.json
# GATEWAY_ADMIN_TOKEN 和 GATEWAY_READER_TOKEN 由部署环境注入
export GATEWAY_PUBLIC_URL=https://gateway.example.com
./start.sh -addr 0.0.0.0:8788
```

在 HTTPS 反向代理后运行。管理 API 支持用管理员令牌调用 `/api/auth/login` 建立 8 小时 HttpOnly / SameSiteStrict 会话 Cookie。MCP / CLI 带 `Authorization: Bearer <token>`；CLI 可设置 `GATEWAY_TOKEN`。认证后忽略请求中的 actor，按主体 groups、servers、tools 和 allow_write 重新检查发现、schema 和执行；admin 也不绕过工具 ACL。管理 API 仅管理员可用。访问配置和令牌变更需要重启，会话随进程失效。

服务 headers/env 只保存环境变量引用；stdio 子进程只继承最小运行环境和明确声明的 env。返回结果默认遮盖凭据字段、当前服务凭据值，还可设置 `redact_fields:["user.email","logs.customer_id"]` 与 `redact_emails:true`。脱敏先于字段投影。它不自动识别所有业务 PII；由服务负责人配置字段。调用日志不保存返回数据或请求参数。共享模式默认不信任开发服务器 Origin，确需跨来源调用管理 API 时显式设置 `GATEWAY_TRUSTED_ORIGINS`。

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

## 路由评测

`evals/routing/github.json` 是一组固定的路由题：正例要求返回全部 `expected` 工具，`acceptable` 是可以接受的额外工具，其余返回的工具记为噪声；`no_match` 用例要求什么都不返回。对着正在运行、已配置模型的网关执行：

```sh
go run ./cmd/routingeval                      # 默认读 evals/routing/github.json
go run ./cmd/routingeval -url http://127.0.0.1:8789 -cases evals/routing/github.json
```

`evals/routing/vercel.json` 是只针对 Vercel 的另一组题，运行前先在网关里停用其他服务，让候选只有 Vercel 的工具（默认的按服务收窄开着时不必停用）。`evals/routing/multi.json` 是 14 个服务、61 题的跨服务题，含用词在多个服务间通用的 `cross` 题和写操作 `no_match` 题。题目可标 `needs_context: true`，表示意图在几个服务间同样成立，网关要求补充上下文也算对；`no_match` 题可带 `acceptable`，表示启用了某些工具（如网页搜索）时返回它们不算错。题目可带 `tag` 分组统计，`any_of` 表示返回其中任意一个即算对。每次调用约数万 input token，而模型服务按每分钟 token 限速（Gemini Tier 1 为 100 万/分钟），连续跑多组时评测脚本会在遇到错误后等待重试。

输出逐题结果，以及召回、无噪声占比、负例正确率、精度和平均耗时/输入 token。它不是通过/失败的门槛，而是调阈值、改提示词或换模型前后用来对比的基线；题目由人标注，`acceptable` 的划分带主观性。

## 开发

```sh
make run       # 无界面 Go 后端（go run ./cmd/gateway）
make dev       # 原生桌面端（Dev）
make build     # 构建后端与命令行工具
```

```text
cmd/gateway           Go 网关入口（无界面）
cmd/gateway-desktop   MyGo 原生桌面端（9 页控制台，仅通过 /api HTTP 通信）
cmd/gatewayctl        CLI 与官方客户端插件管理（gatewayctl plugin）
cmd/gateway-hook      本机 Coding Agent 工作区作用域 HMAC 签名 Hook
cmd/routingeval       工具路由评测工具
cmd/sshlogs           只读远端容器日志 stdio MCP 服务
internal/api          HTTP / MCP / Webhook 传输适配、OpenAPI 生成与契约测试
internal/apiclient    基于 apitypes 的类型化 /api HTTP 客户端
internal/apitypes     控制面 /api 线缆契约 DTO
internal/gateway      应用外观与跨域协调根（含 publicationConfigMu）
internal/routing      纯零 I/O 三步路由策略引擎
internal/model        OpenAI / Gemini / SystemOne Decisions 客户端与模型档案
internal/store        SQLite DDL/SQL 与迁移唯一拥有者（14 张表）
internal/scope        Repo × Agent 作用域评估与 JTI 防重放
internal/workspace    工作区锚点识别与 HMAC scope token 签发/验签
internal/upstream     stdio / Streamable HTTP MCP、浏览器 OAuth、OpenAPI HTTP 映射与脱敏
internal/maintenance  自动维护编排、Webhook 队列、独立验收、Docker/Pi 沙箱
internal/market       MCP 市场检索与安装
internal/access       共享模式主体 RBAC 与登录会话
internal/clientconfig 本机 6 大客户端用户级 MCP 配置管理
internal/clientplugin 本机 6 大客户端官方插件生命周期管理（内嵌 plugins/）
```

详细包分层、自动化护栏与核心运行机制见 [`docs/architecture.md`](docs/architecture.md)。

参考：[Uber MCP Gateway](https://www.uber.com/pr/en/blog/designing-mcp-gateway/)、[官方 MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk)、[OpenAI Decisions API](https://developers.openai.com/api/reference/resources/decisions/methods/create)。

## 原生桌面端：连接客户端与官方插件

MyGo 桌面端侧栏「连接客户端」与 `./bin/dev/gatewayctl plugin` 共同管理 Codex CLI、Claude Code、Cursor、OpenCode v2、Pi 和 Antigravity 的官方插件安装与用户级 MCP 配置写入，并支持升级和撤回。客户端通过该入口发现 `discover_tools`、`get_tool_schema` 和 `invoke_tool`，并在启用官方插件后自动通过 `gateway-hook` 携带仓库作用域凭证。

页面显示实际配置路径和文件状态。写入保留其他配置；同名冲突不会覆盖；撤回只移除本应用写入且未被外部修改的条目。配置后需重载客户端并保持 Gateway 运行，Pi 可使用 `/reload`。共享鉴权模式还需在客户端补充 Bearer Token。


## Dev 与 Stable 构建

本地默认运行开发版，正式版的应用标识、数据目录和更新源保持不变。两版可同时安装运行。

| 项目 | Dev | Stable |
| --- | --- | --- |
| 应用 | MCP Gateway Dev（DEV 图标） | MCP Gateway |
| 默认端口 | 8789 | 8788 |
| 用户数据目录名 | mcp-gateway-dev | mcp-gateway |
| 客户端 MCP 条目 | mcp-gateway-dev | mcp-gateway |
| 自动更新 | 关闭 | 正式发布源 |
| 产物 | build/dev/ | build/stable/ |

```sh
make dev      # 运行 Dev 原生桌面端
make package  # 打包 Dev .app
make release  # 显式打包 Stable；不上传、不安装
```

版本号统一维护在 `internal/buildinfo/profiles.json`；Dev 自动附加 Git 短 SHA 和 dirty 标记。身份编译进程序，Finder 启动不依赖渠道环境变量。Dev 使用 `GATEWAY_DEV_DATA_DIR` / `GATEWAY_DEV_PUBLIC_URL`，Stable 继续使用原来的 `GATEWAY_DATA_DIR` / `GATEWAY_PUBLIC_URL`；显式 `-data`、`-addr` 仍优先。

正式上传通过 `.agents/skills/release/scripts/release-macos-local.sh --upload` 或 tag 发布工作流执行，要求干净工作区、HEAD 对应版本 tag，且该版本尚未正式发布。已发布版本必须升版，不能覆盖上传。日常 CI 全部在 Linux 执行，并交叉生成 macOS arm64/amd64 的 Dev 与 Stable `.app`；校验包身份、Mach-O 架构和配套 CLI 后，仅保留未签名 Dev `.tar.gz` 测试包（保留执行权限）。Developer ID 签名、Apple 公证、DMG 与正式发布仅在 Release 工作流的 macOS runner 上执行。
