# 控制面 HTTP API 契约

[`openapi.json`](openapi.json) 是网关自身 `/api` 的 OpenAPI 3.1 描述，桌面端和以后的网页控制台都按它与后端通信。

## 来源

文档由代码生成，不要手工编辑：

- 请求和响应结构体在 [`internal/apitypes`](../../internal/apitypes)，这个包不依赖任何其他 internal 包。
- 路由表 `apiRoutes` 在 [`internal/api/routes.go`](../../internal/api/routes.go)，记录每条路由的请求体、成功响应和查询参数；错误响应统一是 `Error`。
- 生成器在 [`internal/api/openapi_doc.go`](../../internal/api/openapi_doc.go)。字段没有 `omitempty` 时为必填；结构体不允许多余字段；切片、map 和指针可为 `null`。

修改 apitypes 字段或路由后重新生成：

```sh
go test ./internal/api -run TestOpenAPIDocumentIsCurrent -update
```

## 契约测试

`internal/api/contract_test.go` 在以下情况下失败：

| 测试 | 失败条件 |
| --- | --- |
| `TestOpenAPIDocumentIsCurrent` | 修改了 apitypes 字段或路由表，但没有重新生成 `openapi.json` |
| `TestRouteTableMatchesMux` | 注册了 `/api` 路由但没写进 `apiRoutes`，或反过来 |
| `TestHandlersUseDocumentedTypes` | handler 解码或响应的类型与 `apiRoutes` 不一致，或直接响应 `map` 等未声明的类型 |
| `TestResponsesMatchSchema` | 真实后端的响应不符合文档中的 schema |

`internal/api/wire_test.go` 另外保证领域类型新增字段时，apitypes 同步更新。

## 受控 GitHub CLI 与 Skill 准入

管理员通过 `PUT /api/servers` 注册 `kind: "cli"` 的服务。`gatewayctl` 默认使用服务 ID `gh`。配置需提供本机 `gh` 命令和 `env: {"GH_TOKEN":"${GH_TOKEN}"}`。网关解析并保存绝对路径与 `command_sha256`，通过 `PUT /api/servers/{id}/credentials` 保存 GitHub 凭据。首次注册时生成 8 个停用的工具，通过 `POST /api/servers/{id}/tools` 启用。更新二进制时，在保存配置时显式提交新文件的 SHA-256。执行前的检查失败时返回 `cli_binary_changed`。

- 读取工具为 `pr.list`、`pr.view`、`issue.list`、`issue.view` 和 `repo.view`，返回固定的 JSON 字段。
- 写入工具为 `issue.comment`、`pr.comment` 和 `pr.merge`。可以用现有审批规则要求人工审批，并通过 `approval_id` 以相同的参数、Skill 和作用域重试。
- `POST /api/cli/exec` 接收 `service_id` 和从子命令起始的 `argv`。所有操作都需要显式指定 GitHub `OWNER/REPO`。列表支持 `--state` 和 `--limit`，评论支持 `--body`，合并需要从 `--squash`、`--merge` 和 `--rebase` 中选择一种。

```sh
gatewayctl exec -- pr view 42 -R owner/repo
gatewayctl exec -skill release -- pr merge 42 -R owner/repo --squash
```

`gatewayctl exec` 默认用本地 `scope.key` 签署请求，缺少密钥时会报告具体错误。`-scope off` 显式发送未签名请求，仍受网关的作用域策略约束。收到审批挑战时，命令输出 JSON 和重试提示，退出码为 75。

管理员通过 `GET /api/skills`、`PUT /api/skills` 和 `DELETE /api/skills/{id}` 管理 Skill 准入。`allowed_repos` 使用网关仓库 ID，`allowed_services` 使用服务 ID，每个列表也可用单独的 `"*"`。REST、MCP 和 CLI 调用可以提交 `skill_id`。网关检查已声明的 Skill，并在审计中标记 `skill_trust: "declared"`。签名证明的是请求完整性和仓库作用域。省略 `skill_id` 时沿用普通调用策略。CLI 审计支持按 `protocol=cli`、`repo_id`、`service_id`、`action` 和 `skill_id` 查询。
