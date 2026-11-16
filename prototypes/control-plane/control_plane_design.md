# MCP Gateway Control Plane 极简重构设计规范

配套交互原型：[control-plane-prototype.html](control-plane-prototype.html)  
原生主题底座：[ui_theme.go](../../cmd/gateway-desktop/ui_theme.go) · 应用外壳：[ui_app.go](../../cmd/gateway-desktop/ui_app.go)

---

## 1. 核心审美与信息减法准则（Linear / Raycast / Stripe Sail）

1. **砍掉 70% 说明书式废话文案**
   - 标题本身已自明（如 `运行大盘`、`上游服务`、`工具目录`、`仓库绑定`、`请求日志`、`路由沙盒`、`访问与路由`），`SailSectionHeader` 与 `SailCardTitle` 默认传 `subtitle = ""`；优化后的 `SailSectionHeader` 在 `subtitle == ""` 时不留空隙，直接渲染利落的标题与右侧操作区。
   - 删掉表单底部、弹窗顶部、Tab 顶部的常识性说明（如“点击保存后生效”、“语义路由模型将按相关性打分”等）。仅保留高价值护栏提醒或空状态引导。

2. **去套娃与 Hairline Divider 列表**
   - **严禁卡片套小卡片**：容器内列表统一使用无外框的底部分割线行（`SailFlushCard` 内的细分割线行 / `SailDivider`，`1px solid #e3e8ee`），替代层层描边 + 圆角 + 阴影的小卡片堆叠。
   - **单行最多 3 个信息锚点**：列表行聚焦「主标识（+ 弱化从属名） / 清洗后一句话摘要 / 状态或风险开关」，次要元数据（`Actors`、`Tags`、`Revision`、`Adapter`）全部收进右侧详情检视面板。

3. **徽标降噪（Badge De-noising）**
   - **常规/在线/只读状态降噪**：使用 `SailQuietStatus(c, label, dotColor, textColor)`（6–7px 状态圆点 + `12px` 柔和灰字 `#697386`），不再铺满高对比度绿/蓝彩色胶囊。
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

### 2.1 列表摘要：`CleanDescriptionSummary(raw string) string`

- 去除 Markdown 标记与文档分节噪声，提取简洁用途摘要。
- `SailDescSummary(c, raw, emptyFallback, maxLines)` 用于列表展示，限制行数并保留空描述的明确回退。

### 2.2 详情解析：`ParseStructuredDescription(raw string) StructuredDescription`

- `Lead`：首段用途；`Paragraphs`：补充段落。
- `Bullets`：要点；`Params`：参数名、类型与说明。
- `HasMoreRaw`：存在需展开查看的原文内容。

### 2.3 详情渲染：`SailDescriptionDetail(c, key, raw)`

保留结构化段落、行内代码/强调、要点和等宽参数名；长内容按需展开原文。上述名称对应 PR #6 合入后的原生实现，原型页面用于信息层级与交互参考。

---

## 3. 分页极简重构要点

| 页面 | 文件 | 核心减法与重构重点 |
| --- | --- | --- |
| 运行大盘 Overview | `ui_overview.go` | 去掉多余引导说明；4 张 KPI 卡片保持 3 行以内的紧凑对齐；异常盲区与热点工具改用 细分割线行 无框分割行；状态降噪为圆点 + 灰字 |
| 上游服务 Services | `ui_services.go` | 砍掉列表页、Subtab 与弹窗顶部 70% 说明废话；服务行与工具白名单行改为 细分割线行；列表调用 `SailDescSummary`，工具编辑/详情调用 `SailDescriptionDetail`；仅保留 `RW` 与错误告警胶囊 |
| MCP 市场 Market | `ui_market.go` | SaaS 优先精选与官方 Registry 搜索分栏；3 列简洁卡片展示图标、发布方、运行方式与 `需登录` / `需凭据` 标签，一键添加或打开凭据录入弹窗 |
| 工具目录 Catalog | `ui_catalog.go` | 筛选栏合并紧凑；列表行精简为「工具名 + 弱化服务名 / `SailDescSummary` / 右侧 `RW` 或灰字 `RO` + 开关」；`Actors`/`Tags` 全部收进右侧详情面板；右侧描述区使用 `SailDescriptionDetail` 彻底告别纯文本墙 |
| 仓库绑定 Repos | `ui_repos.go` | 拒绝 M×N 二维大矩阵，采用主从双栏布局：左栏固定 `全局默认`（开箱 0 服务）+ `已绑定仓库` + `最近请求 · 未绑定` 一键绑定；右栏聚焦 `仓库默认服务`（`RO`/`RW` 与工具子集）+ 按需 `客户端例外` + 5 端只读 `生效预览`（`↗` 跳路由沙盒）+ `手动配置 Hook` 片段检视 |
| 请求日志 Logs | `ui_logs.go` | 删掉导语；左侧时间线改用 细分割线行（状态圆点 + 意图/工具标题 + 弱化时间/耗时）；右侧追踪面板突出状态、延迟、Token、命中工具清洗摘要与 `scope` 仓库作用域追踪（支持未绑定仓库一键绑定与带作用域重放） |
| 路由沙盒 Playground | `ui_playground.go` | 精简左右工作台标题与提示文案；底部支持紧凑模拟 `仓库` 与 `客户端` 作用域（`sim_scope`）；命中工具列表改用分割线行 + `SailDescSummary` + 弱化命中理由；右侧 Inspector 结构化展示工具描述与 Schema |
| 访问与路由 Settings | `ui_settings.go` | 删掉护栏矩阵与策略卡片的冗余副标题；表格改用细分割线与等宽数字；仅在开放写工具或校验失败时保留告警块 |


## 4. 0.0.2 整合补充

- 应用跟随系统浅色/暗色外观，主题切换不重置页面或数据。浅色保留极简重构的 token；暗色覆盖背景、文字、分割线、输入框与语义状态色。
- 「连接客户端」采用一行一个客户端：品牌图标、名称、定位配置的 ↗，以及按状态显示的写入/更新/已配置/检查操作；撤回放入更多菜单。
- Dev 与 Stable 的应用身份、数据目录、端口、客户端 MCP 条目及更新策略相互隔离。

## 5. 仓库绑定（Repo × Coding Agent 作用域）设计细则

1. **导航定位与顶部护栏**
   - 一级导航名固定为 **`仓库绑定`**（`repos`），位于 `工具目录` 与 `连接客户端` 之间，徽标显示已绑定仓库数。
   - 页面顶栏右侧保持克制：`启用仓库隔离拦截`（`enforce`）开关、`手动配置 Hook` 按钮与 `添加仓库` 主按钮。开启 `enforce` 后，缺少有效 `_gw_ctx` 凭证的请求返回 `403 scope_required`，严禁回退到全局读写池。

2. **主从双栏结构（拒绝二维矩阵）**
   - **左栏（280px `SailFlushCard`）**：
     - 首行固定 `全局默认`（`repo_key = "*"`）：未绑定新仓库命中此层，开箱默认 `grants: []`（`默认无服务`）。
     - `已绑定仓库` 分组：每行严格控制在 3 个锚点以内（仓库名、`RemoteHint` 短标识或 `本地仓库`、绑定服务数），按本机 Git `CommonDir` 自动归并所有 `git worktree` 与子目录。
     - `最近请求 · 未绑定` 分组：仅展示携带有效 Hook 签名凭证的观测仓库，行尾提供一键 `绑定`。
   - **右栏（选中作用域详情）**：
     - **锚点头部**：展示仓库名、`CommonDir` / 目录根路径与移除操作（Web 端支持从已观测列表一键绑定或手动录入名称与 `git_common_dirs` / `root_paths`，不依赖原生系统目录选择器）。
     - **仓库默认服务 `(repo, *)`**：细分割线列出授权服务，服务描述经 `CleanDescriptionSummary` 降噪；右侧紧凑切换 `只读 RO` / `读写 RW`（仅 `RW` 渲染琥珀胶囊），支持按需展开勾选 `tool_ids` 工具子集。
     - **客户端例外 `(repo, agent)`**：仅渲染存在显式覆盖规则的客户端行，点击 `+ 添加客户端例外` 按需为 `Codex CLI / Claude Code / Cursor / OpenCode v2 / Pi` 单独收窄或放开权限，随时 `恢复继承`。
     - **五端生效预览（5 行只读卡片）**：按 `(R, agent) > (R, *) > (*, agent) > (*, *)` 整层替换优先级实时展示 5 个客户端最终命中的规则来源、服务/工具数与读写边界，行尾 `↗` 一键带入 `路由沙盒`。

3. **手动配置 Hook 与三页闭环**
   - `手动配置 Hook` 检视器支持切换 5 个客户端，展示目标配置路径、`gateway-hook` 二进制路径及可一键粘贴的配置/插件代码片段。
   - `路由沙盒` 提供模拟仓库（`管理员全量` / `未绑定新仓库 (*)` / 已绑定仓库）与模拟客户端下拉项，请求携带 `sim_scope` 并联动刷新候选池计数。
   - `请求日志` 检视器结构化展示 `detail.scope`（客户端、仓库、根路径、命中规则、验证状态），未绑定仓库可就地一键绑定，「在沙盒中重放」自动携带原请求的 `(repo, agent)` 作用域。
