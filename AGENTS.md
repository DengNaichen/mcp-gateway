# AGENTS.md

## Prototypes

- 仓库内的所有 UI / 交互原型与配套设计文档统一存放在根目录 `prototypes/` 下（索引见 [prototypes/README.md](prototypes/README.md)），一律使用仓库相对路径链接，严禁留在 `~/.gemini` 等临时目录或 artifact 目录中。
- 在开发新界面、调整布局层级、精简文案或修改交互流程前，先查阅 [prototypes/README.md](prototypes/README.md) 及对应设计文档，复用已定稿的信息层级、降噪规则与交互规范。
- 新增或更新原型时，直接保存在 `prototypes/` 下并同步更新 [prototypes/README.md](prototypes/README.md)；提交前运行 `python3 scripts/check-doc-links.py` 校验仓库内 Markdown 相对路径链接有效性。
- 原生桌面端代码实现遵循 `cmd/gateway-desktop/`（`mygo/ui`）架构与 `cmd/gateway-desktop/ui_theme.go` 中的 Stripe Sail / Linear 极简主题与描述清洗规范。

## Release

- 版本号升级与打标统一通过 [scripts/bump-version.sh](scripts/bump-version.sh) 完成：校验工作区干净并与 `origin/main` 同步后，更新 `internal/buildinfo/profiles.json` 中的统一版本号、提交并推送 `v<version>` tag。
- Tag 推送后由 [.github/workflows/release.yml](.github/workflows/release.yml) 自动执行版本校验、Draft Release 创建、前端构建与 Go 测试、`mygo build`（`darwin/arm64,darwin/amd64`）Developer ID 签名与 Apple 公证、生成 Ed25519 自动更新清单（`update-darwin-*.json`）与 `SHA256SUMS` 并正式发布至 GitHub Releases。
- 本地签名公证与打包调试可使用 [scripts/release-macos-local.sh](scripts/release-macos-local.sh)（自动读取 `.env.release`、Keychain Developer ID 证书、notary profile 与 Ed25519 更新私钥并调用 `node scripts/build-channel.mjs package stable`）。
