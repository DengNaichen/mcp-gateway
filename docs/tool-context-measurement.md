# 工具上下文测量

测量日期：2026-11-15。数值来自本机运行中的 Gateway 注册表和 MCP `tools/list`。可复跑：

```sh
uv run scripts/measure-tool-context.py --output docs/tool-context-measurement.json
```

脚本不调用下游工具或路由模型。只读取工具元数据和已有历史记录。原始数值见 `tool-context-measurement.json`。

## 口径

- 直接接入：将每个工具的 name、description、input_schema 序列化为紧凑 function JSON。
- Gateway 固定开销：从真实 MCP `tools/list` 读取三个工具，使用相同 function JSON 格式。
- 按需返回：按当前 Gateway 返回的完整 Tool 结构（包括元数据）计数。投影使用固定的简短 reason，不含 discovery 外层；历史记录则计入完整 discovery 返回正文。
- 使用 tiktoken 的 o200k_base、cl100k_base 两种编码。它们是可复算的估算基准，不代表 GPT-6 或 Claude 的精确 tokenizer，也不是客户端最终请求的实测 input_tokens。
- 不包含客户端工具格式转换、命名空间、隐藏协议包装、额外 server instructions、系统提示、聊天历史、工具调用参数或执行结果。MCP 同时返回 text content 和 structuredContent；这里只计算正文一次，客户端若重复呈现，实际开销更高。
- 500 个工具是按当前真实工具平均长度外推；没有声称当前实际启用了 500 个工具。

## 结果

当前注册表共 655 个工具，其中 647 个非演示工具；14 个启用服务下共有 395 个启用的非演示工具。Gateway 对外固定提供 discover_tools、get_tool_schema、invoke_tool，当前 max_tools=4。

| 情形 | cl100k_base | o200k_base |
| --- | ---: | ---: |
| 当前 395 个启用工具全部加载定义 | 128,177 | 131,783 |
| 500 个工具：按启用工具的平均长度外推 | 162,247 | 166,811 |
| 500 个工具：按全部非演示库存平均长度外推 | 204,721 | 211,464 |
| Gateway 的 3 个固定入口 | 343 | 357 |
| Gateway + 3 个选中工具，按启用工具平均长度估算 | 1,422 | 1,470 |
| Gateway + 4 个选中工具，按启用工具平均长度估算 | 1,782 | 1,840 |

例如 o200k_base：395 个工具的逐工具计数总和是 131,781，平均 333.623；500 × 333.623 = 166,811。Gateway + 3 个选中工具约为 357 + 3 × 370.846 = 1,470。两者相差约 99.1%，但这个比例只适用于完整定义预加载与按需获取的对比。

若仅以 200,000-token 窗口举例：500 个工具预加载占约 81%–106%；Gateway 固定入口约占 0.17%–0.18%；返回 3–4 个平均大小的工具后约占 0.7%–0.9%，另加 discovery 外层和调用对话。不是对某个具体 Codex 模型窗口大小的声明。

## 历史记录交叉检查

只读 `data/gateway.db` 中最新 100 次成功 discovery（2026-11-14 21:37–21:50，非本次新调用）：96 次返回 1 个工具，4 次返回 2 个。

- 固定入口 + 完整 discovery 返回正文：中位数 757–769 tokens，P90 为 1,305–1,326，最大 3,357–3,396。
- 路由模型自己的日志 input usage：中位数 13,603，平均 22,397.9，P90 为 39,661，最大 64,536 tokens/次。这是另一模型的开销，不进入 coding agent 上下文；不能用以上上下文节省比例推断全系统费用节省。
- 历史流量和配置不必与当前启用集合一致，不能当作未来任务分布的保证。

## 适用边界

现代客户端已有延迟加载。Codex 的官方源码包含搜索 deferred tools 并在下一模型调用暴露匹配工具的实现；Claude Code 文档说明 tool search 会延迟加载定义。因此不能将“接入 500 个”直接等同于“当前上下文必占 16–21 万”。此测量没有抓取用户 Codex 会话的最终模型请求，不能给出该客户端实际使用模式的精确节省值。

- Codex 实现：https://github.com/openai/codex/blob/main/codex-rs/core/src/tools/handlers/tool_search_spec.rs
- Claude Code：https://code.claude.com/docs/en/mcp#scale-with-mcp-tool-search
- OpenAI API tool search：https://developers.openai.com/api/docs/guides/tools-tool-search

工具执行结果仍占上下文，反复 discovery 返回的定义也会随对话历史累积，除非客户端进行去重、压缩或卸载。缓存折扣影响费用或延迟，不能等同于不占上下文。以上数字也不包含 agent 自带工具、代码文件和对话。
