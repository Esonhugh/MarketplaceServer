# Identity Authentication Management API Contract Rationale

- **Status:** `approved`
- **Request plane:** `/api/v1` management
- **Owner:** `backend/domain/identity`
- **Created:** 2026-08-12
- **Last reviewed:** 2026-08-12
- **Approval record:** 用户通过逐项 AskUser 评审直接批准

## Authority

精确已部署 path、method、parameter、request/response schema、status、header 和 example 的完整权威是 [management-v1.yaml](../../../../../api/openapi/management-v1.yaml)；[management-v1-design.yaml](../../../../../api/openapi/management-v1-design.yaml) 继续承载尚未实现的 broader management target。本文只解释 identity 系统范围与 rationale；发生冲突时以 deployed OpenAPI 和 runtime tests 为准。

## Operations

本系统只拥有：

```text
POST   /api/v1/auth/login
GET    /api/v1/me/tokens
POST   /api/v1/me/tokens
DELETE /api/v1/me/tokens/{tokenId}
POST   /api/v1/me/tokens/{tokenId}/reveal
```

Login 和 health 是匿名 public allowlist；其他 management operations 只接受 Bearer JWT。Basic account password 和 Basic PAT 都不是 management request credential。Password 只出现在 login body 和 PAT reveal body。

## Wire rationale

- Login 成功只返回 `username`、`token`、`expiresAt`。
- Protocol/body 解析错误返回 400；可解析但缺失/非法认证字段、未知用户、错误密码和 disabled user 返回 401。认证失败使用稳定 code；安全 `details` 是可选字符串。
- Unknown JSON properties ignored；只解析 contract 声明字段；malformed JSON、缺 body 和 trailing second JSON document rejected。
- PAT create 使用 `name`、三值 `preset` 和可选未来 `expiresAt`。
- PAT list 使用 page/size/exact total 和固定 `createdAt DESC,id DESC` 顺序，包含全部 status。
- PAT create/reveal 返回 metadata、计算后的 status 和 plaintext token；普通 list 永不返回 token。
- Reveal 是 `POST .../{tokenId}/reveal`，body 只有 password；错误密码返回 401，非 owner/未知 token 不泄露为其他用户资源。
- Revoke 使用 DELETE 但语义是幂等 revoke，不是物理删除，成功 204。
- Success/204 不要求 request ID。Error response 由框架生成 ULID `X-Request-Id`，body `requestId` 与 header 相同。
- Login、PAT create 与 reveal 都返回 `Cache-Control: private, no-store` 和 `Pragma: no-cache`；frontend 可在当前内存会话保留 reveal 结果，但不能写 localStorage/sessionStorage。

## Credential-plane separation

PAT preset 不出现在 management security scheme 中。Git 和 subscription 的 Basic PAT credential boundary 分别见 [`git/api-contract.md`](../git/api-contract.md) 与 [`distribution/api-contract.md`](../distribution/api-contract.md)；本文件只记录 management 明确拒绝 PAT。

## Implementation 与 compatibility

Identity management operations 已按本设计部署：public login/health、PAT Bearer-only、page/size/total、preset、create/reveal plaintext 与幂等 revoke。旧 Basic/cursor/scope contract 已从 deployed OpenAPI 删除，未来 Plugin/Marketplace/team paths 未提升。

Persistence 不增加 legacy 双读、backfill 或 compatibility mode。Startup guard 对旧 scope table 或缺少目标 PAT columns 的 schema 拒绝启动；开发环境由 operator 确认可丢弃后重建，非开发环境必须先备份并设计显式 migration/rotation。
