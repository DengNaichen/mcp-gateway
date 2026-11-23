# Prototypes

存放 MCP Gateway 控制面的 UI / 交互原型与设计规范文档。所有文档与链接使用仓库内相对路径；完整设计与交付流程见 [.agents/skills/prototype/SKILL.md](../.agents/skills/prototype/SKILL.md)。

- 启动本地预览：`python3 .agents/skills/prototype/scripts/serve-prototype.py --open`
- 校验原型与链接：`python3 .agents/skills/prototype/scripts/verify-prototype.py`（从仓库根目录运行）

## 索引

| 名称 | 入口文件 | 状态 | 说明 |
| --- | --- | --- | --- |
| 控件阴影对比 | [elevation.html](control-elevation/elevation.html) | 已实现 | 调整前 / 轻微浮起并排对比，覆盖导航、按钮和卡片，支持同步切换导航与浅深色主题；规范见 [elevation_design.md](control-elevation/elevation_design.md) |
| Control Plane 极简重构原型 | [control-plane-prototype.html](control-plane/control-plane-prototype.html) | 迭代中（v0.1.0 + 服务就绪度闭环 + 零信任 M1 增量） | 全 9 页（概览 / 上游服务 / MCP 市场 / 工具目录 / 仓库绑定 / 连接客户端 / 请求日志 / 路由沙盒 / 访问与路由）可点击高保真原型，含「概览」纯运行时信号与 15 分钟脉冲条、「上游服务」三层就绪度状态解耦与列表分诊（需处理优先分组排序、状态筛选、行内就地修复动作、启用开关 / 凭据与实时连接就绪度 / 历史检查追溯、缺失凭据首屏告警与一键钥匙串补齐复测）、MCP 市场卡片与安装配置弹窗、「连接客户端」（六端官方插件一键安装、旧版全局 MCP 自动迁移、校验 Prompt 复制、Codex 首次信任提示按安装成功记录自动隐藏与降噪 `⋯` 菜单）、「仓库绑定」（仓库 × MCP 默认授权矩阵、单仓库详情主从双栏、纯仓库维度 `具体仓库 > 全局默认 *` 权限收窄与手动 Hook 片段）、真实品牌 SVG 图标、浅/深色双主题及真实超长/混排上游 MCP `description` 清洗排版；零信任能力控制平面增量（M1–M3 基线，不新增一级菜单）：概览「待处理审批」横条、仓库绑定「基线授权矩阵 + 额外限制（Deny / Approve）」、请求日志统一审计账本与行内人机审批、上游服务受控 CLI（`gh`）与 Skill 准入白名单、路由沙盒 `sim_policy` 策略推演、访问与路由信任模式切换；配套设计规范见 [control_plane_design.md](control-plane/control_plane_design.md)（§7） |
| 服务与市场合并原型 | [services-market-merge.html](services-market-merge/services-market-merge.html) | 探索中 | 将「上游服务」与「MCP 市场」合并为单一一级菜单（支持顶部实时切换 `服务` / `外部服务` / `集成` 命名试看），页内以 `已接入` / `市场` 分段切换，含就绪度分诊、行内补齐凭据/测试连接、市场一键添加到已接入闭环及浅/深色主题 |
| 仓库绑定矩阵对齐对比 | [repo-matrix-alignment.html](repo-matrix-alignment/repo-matrix-alignment.html) | 已实现 | 对照原生桌面端仓库绑定矩阵视图的表头/复选框错位与右侧「详情」按钮裁切问题，支持切换当前现状、统一居中（推荐）、统一左对齐，含对齐参考线、4/7 服务滚动切换与浅/深色主题 |
| 桌面凭据持久化 | [credentials.html](credentials/credentials.html) | 已实现 | 模型与服务凭据的保存、恢复和清除，以及 OAuth 回调反馈；规范见 [credentials_design.md](credentials/credentials_design.md) |
| OAuth 浏览器回调页 | [oauth-callback.html](oauth-callback/oauth-callback.html) | 已实现 | 浏览器完成 OAuth 授权后跳转回 `127.0.0.1/callback` 的落地页，覆盖授权完成 / 授权未完成 / 链接无效三态、服务上下文、内联 Favicon 与浅/深色主题；规范见 [oauth_callback_design.md](oauth-callback/oauth_callback_design.md) |
