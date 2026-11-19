# OAuth 浏览器回调页设计

严格沿用 [控制面规范](../control-plane/control_plane_design.md)、[控件阴影规范](../control-elevation/elevation_design.md) 与 `cmd/gateway-desktop/ui_theme.go` 的原生视觉语言，替换 `internal/upstream/oauth.go` 当前的无样式纯文本回调页。

- **视觉一致性**：
  - 纯净画布（浅色 `#ffffff` / 深色 `#1a1a1e`），不使用装饰性背景渐变。
  - 单层 `SailCard` 容器（`10px` 圆角、`rgba(15,23,42,.065)` 轻边框、`0 3px 10px` 轻浮投影），左对齐排版，卡片内最多仅保留 1 条细分割线分隔正文与服务/错误上下文，无顶栏分割线与多余底栏按钮。
  - 顶部左侧复用桌面端侧栏品牌标识（`mcp.svg` + `MCP Gateway`），右侧遵循徽标降噪规范：正常态仅用 `SailQuietStatus`（`6px` 绿点 + `12px` 柔和灰字），高饱和胶囊仅保留给错误（`danger`）与链接无效（`warning`）。
- **克制且清晰的产品文案**：
  - 去掉底层技术术语（如「回调」「上游服务」「参数缺失」），同时保持克制的产品语态，不提前宣称连接已成功：
    - **授权完成**（`code != ""`）：状态 `正在连接`，正文「请返回 MCP Gateway 查看连接结果，可关闭此标签页。」
    - **授权未完成**（`error != ""`）：状态 `未完成`，正文「请返回 MCP Gateway 重新发起授权，可关闭此标签页。」（附等宽原始原因与一键复制）
    - **授权链接无效**（缺少 `code` 与 `error`）：状态 `链接无效`，正文「请返回 MCP Gateway 重新发起授权，可关闭此标签页。」
- **浏览器标签页与离线自包含**：内联 MCP SVG Favicon 与清晰 `<title>`，全部样式与交互零外链，落地代码通过 `@media (prefers-color-scheme: dark)` 自动跟随系统外观。
- **设计预览**：[oauth-callback.html](oauth-callback.html)。
