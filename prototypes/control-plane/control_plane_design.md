# MCP Gateway Control Plane 极简重构设计规范

配套交互原型：[control-plane-prototype.html](control-plane-prototype.html)  
原生主题底座：[ui_theme.go](../../cmd/gateway-desktop/ui_theme.go) · 应用外壳：[ui_app.go](../../cmd/gateway-desktop/ui_app.go)

---

## 1. 核心审美与信息减法准则（Linear / Raycast / Stripe Sail）

1. **砍掉 70% 说明书式废话文案**
   - 标题本身已自明（如 `运行大盘`、`上游服务`、`工具目录`、`请求日志`、`路由沙盒`、`访问与路由`），`SailSectionHeader` 与 `SailCardTitle` 默认传 `subtitle = ""`；优化后的 `SailSectionHeader` 在 `subtitle == ""` 时不留空隙，直接渲染利落的标题与右侧操作区。
   - 删掉表单底部、弹窗顶部、Tab 顶部的常识性说明（如“点击保存后生效”、“语义路由模型将按相关性打分”等）。仅保留高价值护栏提醒或空状态引导。

2. **去套娃与 Hairline Divider 列表**
   - **严禁卡片套小卡片**：容器内列表统一使用无外框的底部分割线行（`SailHairlineRow` / `SailDivider`，`1px solid #e3e8ee`），替代层层描边 + 圆角 + 阴影的小卡片堆叠。
   - **单行最多 3 个信息锚点**：列表行聚焦「主标识（+ 弱化从属名） / 清洗后一句话摘要 / 状态或风险开关」，次要元数据（`Actors`、`Tags`、`Revision`、`Adapter`）全部收进右侧详情检视面板。

3. **徽标降噪（Badge De-noising）**
   - **常规/在线/只读状态降噪**：使用 `SailQuietMeta(c, text, dotColor...)`（6–7px 状态圆点 + `12px` 柔和灰字 `#697386`），不再铺满高对比度绿/蓝彩色胶囊。
   - **高饱和彩色仅留给关键风险与异常**：
     - 写操作风险：`RW` 保留柔和琥珀告警胶囊（`ToneWarning`）；
     - 错误与故障：连接失败、路由异常保留红/琥珀提示（`ToneDanger` / `ToneWarning`）。

4. **呼吸感与留白基线**
   - 页面级区块间距（Section Gap）：`24px`（主视口 `Padding(28, 32)`，`Gap(24)`）。
   - 卡片内边距（Card Padding）：`20px`（`SailCard` 默认 `Padding(20)`、`Gap(14)`）。
   - 正文行高：多行描述统一 `LineHeight(1.5)` ~ `1.55`，兼顾中英文混排清晰度。

---

## 2. 上游 MCP `Description` 解析清洗与排版标准

真实上游 MCP / OpenAPI 工具的 `description` 常包含大量换行符、`###` 标题、`**粗体**`、`` `code` ``、`- ` / `* ` / `1.` 列表以及 `Args:` / `Parameters:` / `Returns:` 文档段，直接纯文本输出会糊成一团。

`ui_theme.go` 提供统一的解析与双场景渲染组件：

### 2.1 解析器：`ParseRichDescription(raw string) RichDescription`

- 剥离首尾空白、统一换行符，去除生硬 Markdown 符号（`###` 标题前缀、`**bold**`、`__bold__`、`` `code` ``、Markdown 行内链接保留文本），跳过围栏代码块。
- 结构化拆分为：
  - `Summary string`：提取首段第一句核心用途摘要（自动截断 `Args:` / `Parameters:` 与多余换行，按 rune 限长 120 字符），专供列表行展示。
  - `Lead string`：清洗后的首段完整引导说明。
  - `Bullets []string`：自动提取 `- ` / `* ` / `1.` 要点行及 `Args:` / `Parameters:` 下的参数说明行。
  - `HasMore bool`：原文是否包含额外段落、`Returns:` / `Example:` 尾部章节、代码块或超长条目的折叠标记。

### 2.2 列表行渲染：`SailDescSummary(c, raw, maxLines)`

- 仅渲染 `Summary`，使用 `SailInkMuted`（`#697386`）、`12.5px`、`MaxLines(maxLines)` 自动省略号截断；`maxLines <= 1` 时单行截断。
- 若 `raw` 为空则返回空 `ui.Element{}`（调用方按需显示“暂无描述”或缺失提示）。

### 2.3 详情抽屉渲染：`SailDescStructured(c, keyPrefix, raw)`

- 首段 `Lead` 使用 `LineHeight(1.55)`、`SailInkBody` 可选中文本。
- `Bullets` 渲染为带柔和圆点缩进的清晰条目；若条目符合 `paramName: desc` 格式，参数名自动以等宽强对比字体（`monospace` + `SailInkStrong`）高亮，解释文字用 `SailInkBody`。
- 若 `HasMore == true`（或条目超过 6 条），底部提供轻量 `"查看完整原文"` / `"收起原文"` 切换，展开后在柔灰底框内展示可复制原文。

---

## 3. 分页极简重构要点

| 页面 | 文件 | 核心减法与重构重点 |
| --- | --- | --- |
| 运行大盘 Overview | `ui_overview.go` | 去掉多余引导说明；4 张 KPI 卡片保持 3 行以内的紧凑对齐；异常盲区与热点工具改用 `SailHairlineRow` 无框分割行；状态降噪为圆点 + 灰字 |
| 上游服务 Services | `ui_services.go` | 砍掉列表页、Subtab 与弹窗顶部 70% 说明废话；服务行与工具白名单行改为 `SailHairlineRow`；列表调用 `SailDescSummary`，工具编辑/详情调用 `SailDescStructured`；仅保留 `RW` 与错误告警胶囊 |
| 工具目录 Catalog | `ui_catalog.go` | 筛选栏合并紧凑；列表行精简为「工具名 + 弱化服务名 / `SailDescSummary` / 右侧 `RW` 或灰字 `RO` + 开关」；`Actors`/`Tags` 全部收进右侧详情面板；右侧描述区使用 `SailDescStructured` 彻底告别纯文本墙 |
| 请求日志 Logs | `ui_logs.go` | 删掉导语；左侧时间线改用 `SailHairlineRow`（状态圆点 + 意图/工具标题 + 弱化时间/耗时）；右侧追踪面板突出状态、延迟、Token 与命中工具清洗摘要 |
| 路由沙盒 Playground | `ui_playground.go` | 精简左右工作台标题与提示文案；命中工具列表改用分割线行 + `SailDescSummary` + 弱化命中理由；右侧 Inspector 结构化展示工具描述与 Schema |
| 访问与路由 Settings | `ui_settings.go` | 删掉护栏矩阵与策略卡片的冗余副标题；表格改用细分割线与等宽数字；仅在开放写工具或校验失败时保留告警块 |
