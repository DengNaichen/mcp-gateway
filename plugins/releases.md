# 独立插件发布

插件 Release 使用 `plugin-vX.Y.Z` tag，由 [Plugin Release 工作流](../.github/workflows/plugin-release.yml) 从已合入 `main` 的提交构建。插件版本取自 tag。[App 配置](../internal/buildinfo/profiles.json) 的 `pluginVersion` 记录 App 随附的插件基线版本，`version` 记录 App 版本。

每个 macOS 架构和渠道发布一份签名清单及对应的 Hook。清单包含六个客户端的插件模板、版本、平台、渠道及 Hook 的 SHA-256 和字节数。签名使用现有的 `MYGO_UPDATER_PRIVATE_KEY`，插件清单有专用签名域。插件 Release 保留自己的 tag，GitHub 的最新正式 App Release 继续供 App 更新器使用。

Gateway 启动后按上次成功时间判断是否检查，此后每 6 小时检查。后台检查只取得并验证清单。网络错误按 15 分钟、1 小时、6 小时退避，GitHub 要求等待更长时采用其重试时间。用户可在客户端页或用 `gatewayctl plugin check` 立即检查。

用户应用更新时，Gateway 验证 Hook 并将清单和 Hook 缓存在版本目录，然后用现有安装器填写本机 MCP 地址、Hook 参数和客户端配置。每个客户端的安装收据记录其实际使用的插件版本。Gateway 的更新范围是它自己管理的安装，宿主管理的插件由宿主更新。
