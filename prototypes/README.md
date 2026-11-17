# Prototypes

存放 MCP Gateway 控制面的 UI / 交互原型与设计规范文档。所有文档与链接使用仓库内相对路径。

校验文档链接：`python3 scripts/check-doc-links.py`（从仓库根目录运行）。

## 索引

| 名称 | 入口文件 | 状态 | 说明 |
| --- | --- | --- | --- |
| Control Plane 极简重构原型 | [control-plane-prototype.html](control-plane/control-plane-prototype.html) | 迭代中（v0.0.4 基线） | 全 9 页（概览 / 上游服务 / MCP 市场 / 工具目录 / 仓库绑定 / 连接客户端 / 请求日志 / 路由沙盒 / 访问与路由）可点击高保真原型，含 MCP 市场卡片与安装配置弹窗、「仓库绑定」（Repo × Coding Agent MCP 作用域收窄、主从双栏、五端生效预览与手动 Hook 片段）、「连接客户端」一键写入/更新/Hook 检视、真实超长/混排上游 MCP `description` 清洗前后对比与降噪列表规范；配套设计规范见 [control_plane_design.md](control-plane/control_plane_design.md) |
