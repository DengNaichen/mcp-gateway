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
