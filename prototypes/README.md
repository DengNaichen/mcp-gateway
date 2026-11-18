# Prototypes

存放 MCP Gateway 控制面的 UI / 交互原型与设计规范文档。所有文档与链接使用仓库内相对路径；完整设计与交付流程见 [.agents/skills/prototype/SKILL.md](../.agents/skills/prototype/SKILL.md)。

- 启动本地预览：`python3 .agents/skills/prototype/scripts/serve-prototype.py --open`
- 校验原型与链接：`python3 .agents/skills/prototype/scripts/verify-prototype.py`（从仓库根目录运行）

## 索引

| 名称 | 入口文件 | 状态 | 说明 |
| --- | --- | --- | --- |
| Control Plane 极简重构原型 | [control-plane-prototype.html](control-plane/control-plane-prototype.html) | 迭代中（v0.1.0 + 服务就绪度闭环） | 全 9 页（概览 / 上游服务 / MCP 市场 / 工具目录 / 仓库绑定 / 连接客户端 / 请求日志 / 路由沙盒 / 访问与路由）可点击高保真原型，含「概览」纯运行时信号与 15 分钟脉冲条、「上游服务」三层就绪度状态解耦（启用开关 / 凭据与实时连接就绪度 / 历史检查追溯、缺失凭据首屏告警与一键钥匙串补齐复测）、MCP 市场卡片与安装配置弹窗、「连接客户端」（六端官方插件一键安装、旧版全局 MCP 自动迁移、校验 Prompt 复制、Codex 首次信任提示按安装成功记录自动隐藏与降噪 `⋯` 菜单）、「仓库绑定」（仓库 × MCP 默认授权矩阵、单仓库详情主从双栏、Repo × Coding Agent MCP 作用域收窄、六端生效预览与手动 Hook 片段）、真实品牌 SVG 图标、浅/深色双主题及真实超长/混排上游 MCP `description` 清洗排版；配套设计规范见 [control_plane_design.md](control-plane/control_plane_design.md) |
| 桌面凭据持久化 | [credentials.html](credentials/credentials.html) | 已实现 | 模型与服务凭据的保存、恢复和清除；规范见 [credentials_design.md](credentials/credentials_design.md) |
