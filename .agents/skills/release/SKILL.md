---
name: release
description: "管理 mcp-gateway 的版本升级、tag 打标、GitHub Actions 正式发布与 macOS 本地签名公证打包。当用户要求发版、升级版本号、打 release tag、本地打包签名/公证/上传 Stable 版本或排查 release 流水线时使用。"
---

# Release

本 skill 拥有 `mcp-gateway` 的版本升级、tag 发布与 macOS Stable 本地签名公证流程。

## 共同版本源与发布规则

- **共同版本与渠道定义**：[profiles.json](../../../internal/buildinfo/profiles.json) 定义 `version`、`stable`（`MCP Gateway` / `NaichengDeng.mcp-gateway` / 端口 `8788`）与 `dev`（`MCP Gateway Dev` / `NaichengDeng.mcp-gateway.dev` / 端口 `8789`），由 [mygo.config.ts](../../../mygo.config.ts) 与 `internal/buildinfo` 共用。
- **构建入口**：受控打包统一通过 [build-channel.mjs](../../../scripts/build-channel.mjs) 注入 `-ldflags` 固定编译期渠道与 Git revision；运行时设置 `GATEWAY_BUILD_CHANNEL` 不会改变应用身份。普通 `make dev` / `make package` 始终为 Dev 渠道且不含更新源、剥离签名与更新私钥环境变量。
- **发布策略保护**（[release-policy.mjs](../../../scripts/release-policy.mjs)）：
  1. 版本号必须符合语义化版本（`X.Y.Z` 或 `X.Y.Z-prerelease`），且工作区干净。
  2. 当前 `HEAD` 必须严格指向 `refs/tags/v<version>`。
  3. GitHub 上 `v<version>` 必须不存在或仍为 `draft`；**已正式发布的版本禁止覆盖上传，必须升版**。无法查询 GitHub 状态时失败中止。
- **凭据与敏感产物**：`.env.release`、`*.p12`、`*.cer`、`*.pem`、`*.key`、`mygo-update.key`、`*.dmg`、`build/` 均已忽略，严禁提交到仓库。
- **执行确认**：推送 `main`、推送 `v<version>` tag 或本地 `--upload` 会影响远端发布状态，运行前须确认目标版本号与发布意图。

## 发版前检查

1. 确认当前处于干净的主 checkout `main` 分支并与 `origin/main` 同步：

```sh
git status --short --branch
git fetch --no-tags origin +refs/heads/main:refs/remotes/origin/main
git rev-parse HEAD origin/main
```

2. 查看当前版本、已有 tag 与 GitHub Release 状态，确认 GitHub CLI 已认证：

```sh
gh auth status
cat internal/buildinfo/profiles.json
git tag --sort=-v:refname | head -n 10
gh release list -R DengNaichen/mcp-gateway --limit 10
```

3. 运行回归验证（对齐 CI）：

```sh
CGO_ENABLED=0 go test -count=1 ./...
CGO_ENABLED=0 go test -ldflags='-X gatewaylab/internal/buildinfo.channel=stable' ./...
node --test scripts/release-policy.test.mjs
python3 scripts/check-doc-links.py
python3 scripts/check-foundation.py
```

## 常规发版（Tag + GitHub Actions）

### 1. 升级版本号并推送 Tag

从干净、同步的 `main` 运行 [bump-version.sh](scripts/bump-version.sh)：

```sh
.agents/skills/release/scripts/bump-version.sh <version>
```

脚本按顺序执行以下校验与动作：
- 校验 `<version>` 符合 SemVer，当前在 `main` 分支，工作区干净且 `HEAD == origin/main`，本地与 `origin` 均不存在 `v<version>` tag。
- 若 [profiles.json](../../../internal/buildinfo/profiles.json) 当前版本不等于 `<version>`，会更新 `version` 字段、提交 `chore: bump version to <version>` 并推送到 `origin/main`（若仓库开启分支保护禁止直推 `main`，可先通过 PR 合并版本号修改到 `main`，再在 `main` 上执行同一命令：当 `profiles.json` 已是 `<version>` 时脚本会跳过修改并直接为 `HEAD` 打标）。
- 创建 `v<version>` tag 并推送到 `origin`。

### 2. 监控 GitHub Release 流水线

Tag 推送触发 [.github/workflows/release.yml](../../../.github/workflows/release.yml)（仓库内串行执行，不取消进行中的发布）：

```sh
gh run list -R DengNaichen/mcp-gateway --workflow release.yml --limit 3
gh run watch <run-id> -R DengNaichen/mcp-gateway
```

流水线包含三个 job：
1. `draft-release`（`ubuntu-latest`）：核对 tag 与 `profiles.json` 版本一致，运行 `node scripts/release-policy.mjs`，创建 Draft Release（版本号含 `-` 时自动加 `--prerelease`）。
2. `build`（`macos-latest`，默认 `darwin/arm64,darwin/amd64`）：运行 Go 测试；若配置全部签名/公证 Secrets（`MACOS_CERTIFICATE`、`MACOS_CERTIFICATE_PASSWORD`、`MACOS_SIGNING_IDENTITY`、`APPLE_ID`、`APPLE_TEAM_ID`、`APPLE_APP_PASSWORD`）则导入临时 keychain 签名与公证，否则回退为 ad-hoc 未签名构建并保持 Draft；若配置 `MYGO_UPDATER_PRIVATE_KEY` 则传 `-upload` 生成并上传 `.dmg`、`.tar.gz`、`.delta`、`update-darwin-*.json`、`install.sh` 与 `SHA256SUMS`。
3. `publish`（`ubuntu-latest`）：仅在 tag `push` 且 `signed == 'true' && uploaded == 'true'` 时执行，将 Draft Release 转为正式发布（`--draft=false`）。

若需对已存在的未发布 tag 手动补跑构建，可在 GitHub Actions 触发 `workflow_dispatch`（传入 `git_ref` 与可选 `platform`）；`workflow_dispatch` 不会自动运行 `publish`，构建完成后仍为 Draft，需人工核对产物后再发布。

## 本地签名、公证与打包调试

运行 [release-macos-local.sh](scripts/release-macos-local.sh)：

```sh
.agents/skills/release/scripts/release-macos-local.sh [--platform darwin/arm64,darwin/amd64] [--skip-notarize] [--upload]
```

脚本行为与环境变量（自动读取仓库根目录 `.env.release`，不覆盖已导出变量）：
- `MACOS_SIGNING_IDENTITY`：未设置时自动读取 Keychain 中第一个 `Developer ID Application:` 证书。
- `MACOS_NOTARY_PROFILE`：未传 `--skip-notarize` 时默认为 `notary`（通过 `xcrun notarytool store-credentials` 预先存储）。
- `MYGO_UPDATER_PRIVATE_KEY`：未设置时默认读取 `~/Library/Application Support/mygo/update-keys/mcp-gateway/mygo-update.key`；传入 `--upload` 时必须存在有效私钥。
- 先执行 `CGO_ENABLED=0 go test -count=1 ./...`，再调用 `node scripts/build-channel.mjs package stable ...`；传入 `--upload` 时会经过 [release-policy.mjs](../../../scripts/release-policy.mjs) 检查。

仅需本机验证 Stable 打包（不签名公证、不上传）时：

```sh
make release
# 或跳过 DMG，只验证 .app 身份与双渠道隔离：
node scripts/build-channel.mjs package stable -skip-dmg
make package && python3 .agents/skills/desktop-dev/scripts/verify-build-channels.py
```
