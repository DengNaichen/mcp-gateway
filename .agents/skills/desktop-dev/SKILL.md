---
name: desktop-dev
description: "管理 mcp-gateway 本地 Dev 桌面端的源码运行、macOS 本地打包（.app / .dmg）、安装到 /Applications 与桌面主题/双渠道隔离验收。当用户要求打包本地 Dev 应用、运行 Dev 桌面端、验证明暗主题截图或双渠道隔离时使用。"
---

# Desktop Dev

本 skill 拥有 `mcp-gateway` 本地 Dev 原生桌面端（`cmd/gateway-desktop`）的日常运行、macOS 本地打包、安装启动与桌面验收流程。正式 Stable 升版、tag 发布与签名公证见 [release](../release/SKILL.md)。

## Dev 渠道与构建入口

- **Dev 身份隔离**（定义见 [profiles.json](../../../internal/buildinfo/profiles.json) 与 [mygo.config.ts](../../../mygo.config.ts)）：
  - 应用名：`MCP Gateway Dev`（橙色 DEV 角标图标 `icon-dev.png`）
  - Bundle ID：`NaichengDeng.mcp-gateway.dev`
  - 默认端口：`8789`；用户配置目录：`UserConfigDir/mcp-gateway-dev`
  - 客户端 MCP server key：`mcp-gateway-dev`（写入记录 `~/.mcp-gateway-dev/client-config`）
  - 签名与更新：ad-hoc 签名（`-`）、关闭自动更新、输出目录 `build/dev/`，可与正式版 `MCP Gateway`（Stable / `8788`）同时安装与运行。
- **共享构建入口**：[build-channel.mjs](../../../scripts/build-channel.mjs) 留在根 `scripts/`（由 `Makefile`、`ci.yml`、`release.yml` 与 `worktree-runtime.mjs` 共用），负责注入 `-ldflags` 固化渠道和 Git revision，并在 Dev 渠道剥离签名与更新私钥环境变量。

## 本地运行（不打包）

- 主 checkout 运行 Dev 桌面端：
  ```sh
  make dev
  ```
- 次级 worktree 运行 Dev 桌面端（按 worktree 槽位隔离端口 `8801–8899` 与 `data/dev`）：
  ```sh
  node .agents/skills/git-workflow/scripts/worktree-runtime.mjs run desktop
  ```
- 仅编译裸可执行文件（输出 `bin/dev/gateway-desktop`）：
  ```sh
  make desktop
  ```
- 仅启动无界面 Dev Go 后端：
  ```sh
  ./start.sh
  # 或次级 worktree 隔离后端：
  node .agents/skills/git-workflow/scripts/worktree-runtime.mjs run api
  ```

## 本地打包 Dev `.app` / `.dmg` 与安装启动

- 本机生成 Dev `.app`（跳过 DMG，macOS 上包含 ad-hoc 签名）：
  ```sh
  make package
  # 等价于：node scripts/build-channel.mjs package dev -skip-dmg
  ```
- 使用 [package-dev-macos.sh](scripts/package-dev-macos.sh) 完成打包、可选测试、DMG、安装到 `/Applications` 与启动：
  ```sh
  .agents/skills/desktop-dev/scripts/package-dev-macos.sh [--platform darwin/arm64] [--dmg] [--test] [--install] [--open]
  ```
  - 默认按本机架构打包并传 `-skip-dmg`，产物位于 `build/dev/darwin-<arch>/MCP Gateway Dev.app`。
  - `--dmg`：同时生成 `.dmg` 安装镜像。
  - `--test`：打包前先运行 `CGO_ENABLED=0 go test -count=1 ./...`。
  - `--install`：用 `ditto` 将 `MCP Gateway Dev.app` 安装到 `/Applications/MCP Gateway Dev.app`（不影响正式版 `/Applications/MCP Gateway.app`）。
  - `--open`：启动打包（或安装）后的 `MCP Gateway Dev.app`。直接双击/打开 `.app` 始终使用默认 Dev 端口 `8789` 与用户目录；如需多 worktree 并行调试请用 `worktree-runtime.mjs run desktop`。

## 日常 CI 的 Linux 交叉打包

日常 CI 仅使用 Linux runner：`Test (Linux)` 与 `Package (macOS cross-build)` 两个 job 并行运行，再由必需检查 `Checks (Linux)` 汇总结果。其中打包 job 执行 macOS arm64/amd64 交叉打包：

```sh
node scripts/build-channel.mjs package dev -platform darwin/arm64,darwin/amd64 -skip-dmg
node scripts/build-channel.mjs package stable -platform darwin/arm64,darwin/amd64 -skip-dmg
python3 scripts/verify-cross-packages.py
```

Linux 生成的 `.app` 未经 macOS 签名；CI 保留带执行权限的 Dev `.tar.gz`，不能将其视为正式发布包。macOS runner 仅用于 Release 工作流的签名、公证与 DMG。真实桌面运行与主题验收仍在本机 macOS 执行。

## 桌面端本地验收

### 1. 明暗主题与九页面截图验收

使用 [verify-desktop-theme.py](scripts/verify-desktop-theme.py)：

```sh
make package
python3 .agents/skills/desktop-dev/scripts/verify-desktop-theme.py
```

脚本使用临时端口（`127.0.0.1:0`）、临时数据目录与 `GATEWAY_DESKTOP_CONTROL` socket 启动 `build/dev/*/MCP Gateway Dev.app`，依次切换 `light` / `dark` / `system` 主题并截取全部 9 个视图（`overview`、`services`、`market`、`catalog`、`repos`、`playground`、`logs`、`settings`、`clients`）截图到 `/tmp/gateway-theme-*`，结束时自动退出所启动的进程。

矩阵视图可用隔离的仓库、默认授权及超长名称样例复验（不会修改用户配置）：

```sh
python3 .agents/skills/desktop-dev/scripts/verify-desktop-theme.py --repo-matrix
```

### 2. Dev / Stable 双渠道并发隔离验收

使用 [verify-build-channels.py](scripts/verify-build-channels.py)：

```sh
make package
node scripts/build-channel.mjs package stable -skip-dmg
python3 .agents/skills/desktop-dev/scripts/verify-build-channels.py
```

脚本同时启动 Dev 与 Stable 两个真实 `.app`（使用隔离临时数据目录与共享临时客户端配置目录，并故意反向注入 `GATEWAY_BUILD_CHANNEL` 环境变量），核对：
- `CFBundleIdentifier`、`CFBundleName`、窗口标题版本号及 `/api/health` 身份一致；
- 两版端口、数据目录、设置与更新开关完全独立；
- 五客户端并发写入同时保留 `mcp-gateway` 与 `mcp-gateway-dev` 且分别独立撤回。

### 3. 系统钥匙串与重启恢复

macOS 桌面端默认将模型 Key 和服务 Header/Env 凭据保存到系统 Keychain；作用域由构建渠道和规范化数据目录确定。`GATEWAY_CREDENTIAL_STORE=memory` 显式改用内存，`keychain` 可为 macOS 无界面后端启用持久化。远程浏览器 OAuth TokenSource 仍遵循现有会话生命周期。

```sh
make package
python3 .agents/skills/desktop-dev/scripts/verify-credential-persistence.py
```

[验证脚本](scripts/verify-credential-persistence.py) 使用临时数据目录、控制 socket、假模型/上游与一次性凭据启动真实 Dev 应用，保存后退出重启，确认真实 HTTP 调用使用恢复的密钥；清除后再次重启，确认凭据不再出现，并检查数据库/日志无明文泄露。结束后删除该测试的 Keychain 条目。测试不会改动用户的模型/服务配置；浅/深色截图保存在输出的临时目录。

## 相关资源

- 临时构建与验收的产物收尾遵循 [git-workflow 的编译产物清理规则](../git-workflow/SKILL.md#编译产物)。

- UI 原型与主题规范：[prototypes/README.md](../../../prototypes/README.md)、[ui_theme.go](../../../cmd/gateway-desktop/ui_theme.go)
- 应用与托盘图标重生成：[cmd/gateway-desktop/assets/README.md](../../../cmd/gateway-desktop/assets/README.md)（[generate-icons.cjs](../../../scripts/generate-icons.cjs)、[generate-dev-icon.cjs](../../../scripts/generate-dev-icon.cjs)）
