# 构建渠道隔离

## 身份与构建入口

`internal/buildinfo/profiles.json` 是版本号、应用名称、Bundle ID、slug、默认端口的共同来源。MyGo 配置读取它，Go 通过 embed 使用同一文件。`internal/buildinfo` 默认渠道为 dev；受控构建入口使用 linker flags 固定 stable/dev 和 Git revision。运行时设置 `GATEWAY_BUILD_CHANNEL` 不会改变身份。

- `make dev`：运行 Dev 桌面端。
- `make desktop`：Dev 裸可执行文件，输出 `bin/dev/gateway-desktop`。
- `make package`：Dev macOS app，不签署更新、不配置正式更新源。
- `make release`：显式生成 Stable 分发包，不上传。
- `node scripts/build-channel.mjs package stable -skip-dmg`：本机验证 Stable app。
- `scripts/release-macos-local.sh --upload`：证书签名、公证和受保护上传。
- `scripts/bump-version.sh <version>`：在干净、同步的 main 上修改共同版本源并推送 tag。

直接 `go run` / `go build` 和普通 `go tool mygo build` 默认 Dev。Stable MyGo 配置拒绝缺少对应 runtime linker flag 的构建；包运行时还会核对应用名称与编译渠道。发布渠道与编译调试选项独立，Dev 可用优化构建复现问题。

## 隔离资源

| 资源 | Stable | Dev |
| --- | --- | --- |
| Name | MCP Gateway | MCP Gateway Dev |
| Bundle ID | NaichengDeng.mcp-gateway | NaichengDeng.mcp-gateway.dev |
| 用户数据库目录 | UserConfigDir/mcp-gateway | UserConfigDir/mcp-gateway-dev |
| 默认 HTTP 端口 | 8788 | 8789 |
| MCP server key | mcp-gateway | mcp-gateway-dev |
| 写入记录 | ~/.mcp-gateway/client-config | ~/.mcp-gateway-dev/client-config |
| 更新 | 原正式发布源 | 未注册 updater plugin，无更新源 |
| 输出 | build/stable/ | build/dev/ |

Stable 路径保持原值，无需迁移正式数据。两版写入相同客户端文件中的不同条目，分别记录和撤回。Dev 与 Stable 对同一目标配置文件使用同一份跨进程锁（旁置 `.mcp-gateway.lock`），避免同时点击写入造成丢失更新。外部编辑器不遵守此锁，因此仍在替换前比较文件内容。

Dev 的数据/公开地址环境覆盖使用 `GATEWAY_DEV_DATA_DIR` / `GATEWAY_DEV_PUBLIC_URL`，避免继承生产环境的同名通用配置。Stable 保留原变量。显式 `-data` 或 `-addr` 是开发者主动覆盖，仍被支持；复制数据用于复现时应使用副本。命令行后端、控制 CLI 与评测 CLI 的本地默认端口均为 Dev 的 8789。

健康接口包含渠道、应用 ID、版本、进程实例 ID、数据路径指纹。桌面端只自动连接全部匹配的实例；旧版缺少身份信息、其他渠道、其他版本或其他数据目录都会返回端口冲突。没有泄露完整数据目录路径。系统单实例锁使用 MyGo 的包 ID 与应用名称，因此两版锁独立。

## 发布保护

本地上传入口和 GitHub Release 工作流均调用 `release-policy.mjs`：

1. 版本格式有效，工作区干净。
2. 当前 HEAD 与 `v<version>` tag 的 commit 一致。
3. GitHub 上该版本不存在，或仍为 draft；正式发布过的版本拒绝上传。
4. 无法查询 GitHub 状态时失败退出，不把网络错误当作“版本不存在”。

GitHub 发布工作流在整个仓库内串行。普通 PR/主分支 CI 只把 Dev app 打成保留可执行权限的 zip，作为 Actions artifact；不写 Release。手动 Release 工作流要求输入未发布版本的现有 tag。发布密钥和正式签名配置在 Dev 构建进程中移除。

构建与发布入口的这些检查不取代 GitHub 服务端权限；绕开入口手动使用 gh 上传不受本地脚本约束。此次验证未上传、覆盖或发布任何正式版本。

## 验证

```sh
go test ./...
go test -ldflags='-X gatewaylab/internal/buildinfo.channel=stable' ./...
node --test scripts/release-policy.test.mjs
make package
node scripts/build-channel.mjs package stable -skip-dmg
python3 scripts/verify-build-channels.py
```

最后一项启动两个真实 macOS bundle，使用独立的临时数据库和同一份临时客户端配置目录，验证：

- Bundle ID、应用名、窗口版本和健康接口身份一致；反向运行时渠道环境变量无效。
- 两版均拥有独立后端；Dev 改设置不影响 Stable。
- Dev 更新关闭，Stable 更新保留。
- 五客户端由两个真实进程并发写入，两套 server key 同时存在，分别指向正确端点。
- 撤回 Dev 保留 Stable；随后撤回 Stable；记录目录独立。
- 捕获两版真实原生页面截图。只停止验证脚本创建的进程。

2026-11-16 本机 arm64 验证通过，首次端到端证据目录：`/var/folders/gl/r2dr8jk125d3_0_3fqwdj_t40000gn/T/gateway-channels-rhd7kyca`。另以默认端口启动 Dev，实际监听 8789，同时已有 8788 实例继续运行；重复启动 Dev 正常复用单实例。默认启动证据在 `/tmp/mcp-gateway-channel-defaults/`。

追加验证：加入跨进程文件锁后，两个真实 bundle 并发写入全部五客户端的测试通过，证据目录为 `/var/folders/gl/r2dr8jk125d3_0_3fqwdj_t40000gn/T/gateway-channels-6_hav15t`。发布保护用 GitHub 上 v0.0.1 的实际 `isDraft=false` 元数据验证，正确拒绝再次上传；未调用发布操作。

最终回归：Dev 与 Stable 两套 `go test ./...` 均通过，5 项 Node 构建/发布策略测试通过；新增渠道与客户端配置的针对性 race 检查通过，两个 workflow YAML 和发布 shell 脚本解析通过。最后一次完整并发双包验证证据在 `/var/folders/gl/r2dr8jk125d3_0_3fqwdj_t40000gn/T/gateway-channels-rs2xyr8u`。正式发布流水线仅修改定义，没有触发真实发布。
