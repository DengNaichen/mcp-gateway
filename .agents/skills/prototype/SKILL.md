---
name: prototype
description: "管理 mcp-gateway 的 UI / 交互原型设计、本地预览启动与校验流程。在开发新界面、调整布局层级、精简文案、修改交互流程或新增/验证 prototypes/ 下的原型与设计规范时使用。"
---

# Prototype

本 skill 拥有 `mcp-gateway` 前端控制面的高保真原型迭代、本地预览与校验流程。所有原型资产统一沉淀在根目录 `prototypes/`（索引见 [prototypes/README.md](../../../prototypes/README.md)），定稿后再按 [desktop-dev](../desktop-dev/SKILL.md) 落到原生桌面端 `cmd/gateway-desktop/`。

## 何时先改原型

以下改动优先在 `prototypes/` 中完成设计推演与交互验证，再修改 `cmd/gateway-desktop/` 原生代码：

- 新增一级页面、子视图、弹窗或抽屉；
- 调整现有页面的信息层级、列表行密度、状态呈现或主要操作入口；
- 修改跨页面跳转、筛选联动、作用域切换或空/异常态反馈流程；
- 精简文案、验证真实超长或 Markdown 混排上游数据的展示降噪效果。

## 存放与设计基线

1. **目录与路径规范**：
   - 所有 UI / 交互原型（`.html`）与配套设计规范（`*_design.md`）统一存放在 `prototypes/<topic>/` 下，并同步登记到 [prototypes/README.md](../../../prototypes/README.md)。
   - 文档与原型内所有仓库资源引用必须使用仓库相对路径，**严禁**将原型或设计稿留在 `~/.gemini`、`/tmp` 或 artifact 目录中。
2. **设计准则（Stripe Sail / Linear 极简规范）**：
   - 继承 [control_plane_design.md](../../../prototypes/control-plane/control_plane_design.md) 与 [ui_theme.go](../../../cmd/gateway-desktop/ui_theme.go) 的设计语言：
     - **砍掉冗余说明文案**：自明标题不堆砌副标题与常识性解释；
     - **Hairline Divider 替代卡片套娃**：列表行统一使用细分割线行，单行聚焦最多 3 个信息锚点，次要元数据收进右侧检视面板；
     - **徽标降噪**：常规在线/只读状态使用状态圆点 + 柔和灰字，高饱和彩色仅留给写操作风险（`RW`）与错误告警；
     - **真实脏数据压测**：原型中的示例数据须覆盖超长文本、Markdown 混排 `description` 等边界场景，对应 `CleanDescriptionSummary` / `ParseStructuredDescription` 展示标准。
3. **自包含 HTML 约定**：
   - 原型使用单文件自包含 HTML/CSS/JS（不引入额外前端打包构建链），保证可离线通过静态 HTTP 服务直接运行并完成全页面与弹窗交互切换。

## 启动本地预览

使用 [serve-prototype.py](scripts/serve-prototype.py) 启动本地静态 HTTP 服务（绑定 `127.0.0.1`，根目录为 `prototypes/`）：

```sh
# 列出当前所有可预览原型
python3 .agents/skills/prototype/scripts/serve-prototype.py --list

# 启动预览服务（默认随机可用端口；可用 --port 指定端口，--open 自动在浏览器打开）
python3 .agents/skills/prototype/scripts/serve-prototype.py --target control-plane --port 4173

# 直接拉起默认/唯一原型并在浏览器打开
python3 .agents/skills/prototype/scripts/serve-prototype.py --open
```

- 相比直接打开 `file://`，通过 `http://127.0.0.1:<port>/...` 预览可保证剪贴板、相对资源及浏览器自动化工具正常运作。
- 若由 agent 在后台启动该服务用于浏览器截图或交互走查，命令会输出 `URL` 与 `PID`，验收完成后按该 PID 精准停止进程，不留孤儿进程。

## 原型校验

每次新增或修改 `prototypes/` 下的原型、设计文档或索引后，提交前运行 [verify-prototype.py](scripts/verify-prototype.py)：

```sh
python3 .agents/skills/prototype/scripts/verify-prototype.py
```

脚本按顺序执行以下校验（全部无外部第三方依赖）：
1. **索引完整性**：核对 `prototypes/` 下所有 `.html` 原型与 `*.md` 设计文档均已在 [prototypes/README.md](../../../prototypes/README.md) 中登记；
2. **文档链接检查**：调用 [check-doc-links.py](../../../scripts/check-doc-links.py) 校验仓库内 Markdown 相对路径链接有效性；
3. **HTML 结构与资源引用检查**：确认存在基础文档结构（`<!doctype html>`、`<html>`、`<body>`），且 `src`/`href` 不指向 `file://`、`~/.gemini` 或失效本地路径；
4. **内联 JS 语法校验**：提取原型 HTML 内所有内联 `<script>` 块并调用 `node --check` 检查语法错误（跳过时可传 `--skip-js`）；
5. **本地 HTTP 冒烟**：通过内建静态服务对每个原型 HTML 发起真实 `GET` 请求，确认 HTTP 200 与内容完整（跳过时可传 `--skip-serve`）。

## 交付到原生桌面端

原型定稿后：
1. 对照原型与设计文档在 [ui_theme.go](../../../cmd/gateway-desktop/ui_theme.go)、[ui_app.go](../../../cmd/gateway-desktop/ui_app.go) 及对应页面文件中实现原生 UI；
2. 切入 [desktop-dev](../desktop-dev/SKILL.md) 执行 `make package` 与 `verify-desktop-theme.py`（明暗主题与九页面截图验收）。
