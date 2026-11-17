---
name: routing-eval
description: "实验性（尚未完全跑通，谨慎使用）：管理 mcp-gateway 工具路由评测（cmd/routingeval、evals/routing/）与上下文 token 测量（measure-tool-context.py）。仅在修改路由策略、调整 prompt/阈值或明确要求做路由回归对比时按需谨慎使用。"
---

# Routing Eval（实验性 · 谨慎使用）

> **⚠️ 状态提示：本流程尚未完全跑通，属于实验性工作流，请谨慎使用。**
>
> 当前仓库缺少一键复现整套路由评测闭环的自动化环境/上游服务夹具，部分链路仍停留在手工实测阶段：
> - **依赖外部真实环境与付费模型**：`cmd/routingeval` 需要指向已启动且已配置 Decisions 模型凭据的 Gateway 实例；每次跑全量评测会消耗数万至数十万 input token 并触发模型速率限制（如 Gemini Tier 1 的 `1,000,000 input tokens/min`）。
> - **上游服务集合尚未自动编排**：`multi.json`（61 题）与 `vercel.json`（24 题）依赖特定 14 个上游 MCP 服务目录（部分历史实验使用占位凭据，Linear 需浏览器 OAuth 授权），当前没有自动导入/初始化脚本。
> - **覆盖面仍不完整**（详见 [NOTES.md](../../../evals/routing/NOTES.md)）：
>   - 现有题集主要由助手在读取工具列表后编写，**只宜作回归基线对比**，不能代表对未知真实任务的泛化准确率；
>   - OpenAI Decisions 模型及 100 工具上限尚未完成实测；超过 ~180–395 个工具或更多模型提供商的行为尚未充分验证；
>   - [measure-tool-context.py](../../../scripts/measure-tool-context.py) 依赖 `uv`（`tiktoken`）与运行中的 `/api/bootstrap`、`/mcp`（及可选只读 `data/gateway.db`），结果仅为 tokenizer 估算，不直接等于全系统实际账单或客户端真实 prompt 大小。
>
> **使用原则**：
> 1. **禁止**在未确认目标网关地址、模型配额/费用与数据隔离前直接执行全量在线评测；
> 2. **禁止**把 `routingeval` 当作 CI 门禁，或根据单次评测波动在未经人工确认的情况下自动修改路由阈值/策略；
> 3. 仅在明确修改路由代码、调整 prompt 或被要求复核某组基线时，先跑离线预检，再按需手动执行受控评测。

---

## 修改路由代码前必读基线

改动 `internal/routing/`、`internal/gateway/routing_strategy.go`、`internal/gateway/decisions.go`、`internal/gateway/discovery.go` 前，先阅读：
- 策略结论与三步流程：[docs/routing.md](../../../docs/routing.md)
- 历史实验与踩坑记录：[evals/routing/NOTES.md](../../../evals/routing/NOTES.md)

关键已验证约束（避免回退）：
- **禁止改回索引点名**：必须按工具 ID 点名提问，序号 `candidates[i]` 在大候选集下会严重掉精度；
- **保持输入字段顺序**：使用 struct 保证 `intent` 在工具列表之前（map 按键排序曾导致单个工具得分从 `0.93` 掉到 `0.46`）；
- **勿轻易合并每题规则文本**：跨题省去规则文本虽降约 26–30% token，但实测精度掉 8–12 个百分点（写操作负例最先退化）；
- **勿将工具参数名塞入候选摘要**：`params` 方案因共享参数名（如 `projectId`、`teamId`）引入大量相邻查询噪声，精度掉 10 个百分点。

---

## 离线预检（零模型费用，可随时运行）

运行 [preflight.py](scripts/preflight.py) 校验题集 JSON 格式与 `cmd/routingeval` 评分单测：

```sh
python3 .agents/skills/routing-eval/scripts/preflight.py
```

如需在跑在线评测前先只读探测网关是否可达（不触发任何模型调用）：

```sh
python3 .agents/skills/routing-eval/scripts/preflight.py --check-gateway http://127.0.0.1:8789
```

---

## 手动受控评测（需人工确认环境与配额）

确认本机已有运行中的 Gateway、模型已配置且上游服务与目标题集匹配后，再手动执行 [cmd/routingeval](../../../cmd/routingeval/main.go)：

```sh
# GitHub 单服务基线（相对轻量）
go run ./cmd/routingeval -url http://127.0.0.1:8789 -cases evals/routing/github.json

# Vercel 单服务（若未开启按服务收窄，需先停用其他服务）
go run ./cmd/routingeval -url http://127.0.0.1:8789 -cases evals/routing/vercel.json

# 14 服务全量跨服务基线（高 token 消耗，注意限流等待）
go run ./cmd/routingeval -url http://127.0.0.1:8789 -cases evals/routing/multi.json
```

若网关开启了访问鉴权，需在环境中提供 `GATEWAY_TOKEN`。

---

## 上下文 Token 测量（只读估算，不调用模型）

通过 [measure-tool-context.py](../../../scripts/measure-tool-context.py) 统计当前注册表与 MCP `tools/list` 的 compact JSON token 估算（说明见 [tool-context-measurement.md](../../../docs/tool-context-measurement.md)）：

```sh
uv run scripts/measure-tool-context.py --url http://127.0.0.1:8788 --output docs/tool-context-measurement.json
```

Dev 渠道默认端口为 `8789`（次级 worktree 为 `8801–8899`），按实际运行实例传入 `--url` 与 `--db`。
