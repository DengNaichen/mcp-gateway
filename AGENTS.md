# AGENTS.md

## Roster

- 本仓库使用 Roster 管理任务和 Spec；MCP Server 名称为 `roster`，连接方式为远程 HTTP + OAuth，地址为 `https://qietzaqhmforixtpicjd.supabase.co/functions/v1/mcp/w/mcp-gateway/mcp`。
- 默认 Workspace 为 `mcp-gateway`，默认 Team 为 `MCP Gateway`（key：`MCP`）。按要求在此 Team 中管理任务和 Spec，不自动创建新的 Workspace 或 Team。
- 修改已有任务前，先读取其最新内容；修改已有 Spec 前同样先读取最新内容及版本，遵守服务端的版本检查。
- 任务中记录相关仓库（`https://github.com/DengNaichen/mcp-gateway`）、实际工作分支及 PR 链接；尚未创建 PR 时不编造链接，创建后补充。
- OAuth 凭据由 coding agent 的用户级凭据存储管理，不将 Token、Supabase 密钥或其他认证凭据写入仓库。认证失败时报告具体错误和缺少的 OAuth 配置，不绕过认证，不创建测试任务验证连接。

## Prototypes

- 仓库内的所有 UI / 交互原型与配套设计文档统一存放在根目录 `prototypes/` 下（索引见 [prototypes/README.md](prototypes/README.md)），一律使用仓库相对路径链接，严禁留在 `~/.gemini` 等临时目录或 artifact 目录中。
- 在开发新界面、调整布局层级、精简文案或修改交互流程前，先查阅 [prototypes/README.md](prototypes/README.md) 及对应设计文档，复用已定稿的信息层级、降噪规则与交互规范。
- 新增或更新原型时，直接保存在 `prototypes/` 下并同步更新 [prototypes/README.md](prototypes/README.md)；提交前运行 `python3 scripts/check-doc-links.py` 校验仓库内 Markdown 相对路径链接有效性。
- 原生桌面端代码实现遵循 `cmd/gateway-desktop/`（`mygo/ui`）架构与 `cmd/gateway-desktop/ui_theme.go` 中的 Stripe Sail / Linear 极简主题与描述清洗规范。

## Release

- 版本号升级与打标统一通过 [scripts/bump-version.sh](scripts/bump-version.sh) 完成：校验工作区干净并与 `origin/main` 同步后，更新 `internal/buildinfo/profiles.json` 中的统一版本号、提交并推送 `v<version>` tag。
- Tag 推送后由 [.github/workflows/release.yml](.github/workflows/release.yml) 自动执行版本校验、Draft Release 创建、前端构建与 Go 测试、`mygo build`（`darwin/arm64,darwin/amd64`）Developer ID 签名与 Apple 公证、生成 Ed25519 自动更新清单（`update-darwin-*.json`）与 `SHA256SUMS` 并正式发布至 GitHub Releases。
- 本地签名公证与打包调试可使用 [scripts/release-macos-local.sh](scripts/release-macos-local.sh)（自动读取 `.env.release`、Keychain Developer ID 证书、notary profile 与 Ed25519 更新私钥并调用 `node scripts/build-channel.mjs package stable`）。
