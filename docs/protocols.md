# 协议与请求平面

本文维护 MarketplaceServer 的 REST、开发 Git 和 Claude Code 分发协议边界。真实已注册路由见 [当前实现状态](current-state.md)；本文中的“规划中”部分不表示当前 endpoint 已存在。

## 请求平面隔离

| Prefix | 用途 |
|---|---|
| `/api/v1` | 管理和控制 API |
| `/git` | 用户开发 repository 的 Git Smart HTTP |
| `/distribution` | Claude Code Marketplace/Plugin 不可变分发 |
| `/` | frontend 静态资源与非保留 SPA route |

`/gapi` 保留给可能的 gateway，不代表当前运行时已注册 gRPC Gateway。

各平面可复用 principal、action/resource policy 和稳定错误语义，但必须使用独立 handler 与 credential scope。禁止用 User-Agent 或其他可伪造 header 判断请求是否来自 Claude Code。frontend SPA 的完整 reserved-path 清单只在 [系统架构](architecture.md#frontend-架构) 维护。

## REST API 约定

管理 JSON API 固定使用 `/api/v1`：

- 错误响应至少包含稳定 `code`、用户可理解的 `message` 与框架 ULID `requestId`，并返回相同 `X-Request-Id`；success/204 是否携带 request ID 由 operation contract 定义，不返回内部 stack；
- 创建成功使用 `201`，异步命令 `202`，无 body 删除 `204`，并发冲突 `409`，业务校验 `422`，限流 `429`；
- 已部署 PAT list 使用 `page`/`size`，默认 `1`/`20`、最大 size `100`，并在 `data` envelope 中返回 owner-scoped exact `total`；
- 可变资源应提供 ETag/version，并通过 `If-Match` 防止覆盖并发更新；
- namespace URL slug 必须先解析为 namespace ID，再授权和查询；
- 高风险副作用使用显式 command endpoint，不通过通用 `PATCH status` 触发；
- 外部输入在 handler 校验，资源归属和最终授权仍在 service boundary 完成。

当前 management identity wire 由 [`api/openapi/management-v1.yaml`](../api/openapi/management-v1.yaml) 精确定义：login/health 公开，PAT list/create/revoke/reveal 只接受 Bearer JWT；account password 只出现在 login/reveal body，PAT 不作为 management credential。未来 endpoint 清单只在 [路线图](roadmap.md) 以 feature slice 描述；不要在协议文档中把未注册 route 写成当前 API。

## 开发 Git Smart HTTP — 已实现

标准形态：

```text
GET  /git/{namespace}/{repo}.git/info/refs?service=git-upload-pack
POST /git/{namespace}/{repo}.git/git-upload-pack
GET  /git/{namespace}/{repo}.git/info/refs?service=git-receive-pack
POST /git/{namespace}/{repo}.git/git-receive-pack
```

处理顺序：

1. 严格解析 namespace/repository，去除一个预期 `.git` suffix，拒绝 NUL、编码斜杠、`..`、重复 separator 和非法 slug。
2. allowlist `git-upload-pack`/`git-receive-pack`；未知 service 不传给 Git。
3. 只解析 Basic username+PAT 为 Principal，不记录 Authorization；account password 与 JWT 在 Git 平面拒绝。
4. backend resolver 将 slug 转为 opaque repository ID、visibility、status。
5. 通过 Plugin 解析其隐藏 repository；Plugin read 同时授权 metadata 与 upload-pack，Plugin write 单独授权 receive-pack。Repository 不暴露独立产品权限。
6. receive-pack 还需执行 Plugin/repository 状态、protected ref、quota 与并发规则；default branch/tag proposed commit 在隔离目录运行 Claude Plugin validation 和 exact name check。
7. 用 `exec.CommandContext` 和独立参数调用受控 Git binary；物理路径只由 storage root 与 opaque ID 计算，使用 containment check，环境不继承危险 `GIT_*`。
8. 流式转发 Git content type，限制 body/header，设置 timeout，client disconnect 时取消 subprocess。
9. 成功 push 通过幂等 outbox/event 更新 refs、审计和 manifest/version projection；当前完整 worker 流程仍在规划中。

不自行解析或重写 packfile。公开 repository 可匿名 upload-pack；receive-pack 永远需要有效主体和 write authorization。

## Public Marketplace JSON — 已实现

```text
GET /distribution/marketplaces/{marketplacePublicKey}/marketplace.json
```

- public key 必须符合 `{normalized-name}-{8-lowercase-hex}`，是不可变 locator 而不是 credential；
- resolver 只返回 active ready projection artifact；tag move rebuild 完成后 pointer 可切换到新 artifact，失败统一为 not found；
- 响应使用 `application/json`、强 ETag、`If-None-Match → 304`、`no-cache` 和 `nosniff`；
- handler 只读取已发布 bytes，不能在 GET 中构建 draft、写 DB 或修改 projection。

## 用户动态 Marketplace JSON — 已实现

```text
GET /distribution/users/{username}/marketplace.json
```

- path username 必须与 BasicAuth username 和 PAT authenticated user 恒定时间匹配；account password 与 JWT 在 subscription distribution 平面拒绝；
- 每次请求根据当前 authorization 生成用户可读 Plugin 索引；
- 无效身份、资源或 Host 统一返回 not found；
- 响应使用 `private, no-store`、`Vary: Authorization, Host`、ETag 与 `nosniff`；
- Host 只接受安全的 canonical host，不能构造含 credential 或非法分隔符的 source URL。

该 endpoint 使用用户 BasicAuth，不等同于未来的 per-Marketplace private distribution credential。

## Read-only Git distribution — 已实现

```text
GET  /distribution/marketplaces/{marketplacePublicKey}.git/info/refs?service=git-upload-pack
POST /distribution/marketplaces/{marketplacePublicKey}.git/git-upload-pack
GET  /distribution/plugins/{distributionUUID}.git/info/refs?service=git-upload-pack
POST /distribution/plugins/{distributionUUID}.git/git-upload-pack
```

- Marketplace route 只接受 canonical public key；Plugin route 只接受 canonical UUID。
- `info/refs` 只 allowlist `git-upload-pack`；没有 receive-pack、upload-archive、Dumb HTTP 或任意 write route。
- resolver 每次只授予一个 kind 匹配的 active ready projection artifact。
- request handler 只能 advertise/read projection，禁止调用 repository init、projection builder、update-ref 或任何 filesystem mutation。
- `info/refs` 与 `git-upload-pack` 不缓存，并受 body、并发与 timeout 限制。
- 日志只记录安全的 resource identity、result 和 request ID，不记录 credential、pack body 或完整敏感 URL。

Marketplace Git projection 只承载 `.claude-plugin/marketplace.json`。Plugin projection 只承载发布 snapshot 的单一 tag，并与开发 repository 的 objects/history 隔离。

## Private Marketplace distribution credential — 规划中

Private HTTP JSON 与只读 Git 计划接受同一组 per-user/per-Marketplace BasicAuth credential：

1. 以 peppered HMAC 定位 active credential；
2. 验证公开 username 与目标 Marketplace 匹配；
3. 检查 expiry/revocation 和 active user；
4. 动态执行当前 `marketplace.read` 与 distribution 归属授权；
5. 失败使用一致的 401/404 策略，不泄露资源或用户存在性。

Private Plugin URL 可以包含非敏感 public username 以帮助 Git Credential Helper 选 credential，但绝不能嵌入 password。分发 credential 发送到 `/api/v1` 或 `/git` 时必须拒绝。

HTTP JSON 的 Authorization header 不会自动转发给 Plugin Git；Git Credential Helper 也不会被普通 HTTP JSON 下载自动调用。安装说明必须分别处理两条 credential 链路。

## Marketplace schema 与 source

生成或修改 Marketplace JSON 前核对当前 Claude Code 文档、SchemaStore 格式参考和官方 examples，并以 golden tests 固定已支持行为；MarketplaceServer 不承担通用 Plugin JSON validator：

- 顶层 `name`、`owner`、`plugins` 等字段遵循官方 schema；
- 直接下载 JSON 不使用 relative Plugin source；
- Git source 使用官方 `url` 形式、可访问 URL、selected canonical tag 与当前完整 distribution SHA；
- 不自创 transport/source 字段；
- serialization 和 Plugin ordering 稳定；
- public stable JSON、revision projection 与 private JSON 使用各自正确的缓存策略；
- HTTP 与 Git Marketplace 必须指向同一 published snapshot。

## Git SSH — 规划中的目标协议

> 当前没有 SSH listener、公钥模型或 SSH route。以下是未来实现必须遵守的协议边界。

- 使用独立 listener/port，不强行复用 HTTP cmux。
- 只允许 `session` channel 和 Git exec request；拒绝 shell、PTY、subsystem、port/agent/X11 forwarding 与任意 environment。
- 只解析严格的 `git-upload-pack '<namespace>/<repo>.git'` 或 `git-receive-pack '<namespace>/<repo>.git'`，转换为枚举 operation 和 repository ID；原始字符串绝不交给 shell。
- 通过唯一 active public-key fingerprint 定位 user/service account；unknown/revoked key 或 disabled account 在启动 Git 前拒绝。
- upload/receive 复用 HTTPS 的 Plugin-backed repository resolver、Plugin read/write authorization、protected-ref 和 service contract，保证 transport parity。
- host key 使用受保护文件/secret mount 持久化；支持安全轮换，并设置 handshake、authentication、idle、command timeout 和并发上限。
- audit 可以记录 fingerprint/algorithm、actor、repo、action、result 和 session ID，不能记录完整 key 或 pack body。
