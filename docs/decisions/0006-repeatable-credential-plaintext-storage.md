# ADR-0006: Repeatably Revealable Credential Plaintext Storage

- **Status:** Approved
- **Date:** 2026-08-12
- **Last reviewed:** 2026-08-12
- **Approval record:** 用户明确选择可查看型 credential 明文保存，并接受数据库与备份读取者可直接获得这些 credential

## Context

MarketplaceServer 原规则要求 token/password 只保存 hash/HMAC 并只显示一次。产品现在要求部分 credential 可以在登录后的管理界面重复 reveal。应用层加密和一次性显示都被评审后拒绝，以优先保持目标功能和实现简单。

## Decision

只有 owning system 的已批准产品/API contract 明确支持 repeatable reveal command 时，该 credential 才允许使用显式 `secret_plaintext` persistence field。实现者、UI 或 operator 不能自行把其他 secret 分类为可查看型。

采用该模型意味着：

- 数据库只读访问者、数据库管理员和 backup 读取者能够直接取得这类 credential；
- 数据库和备份被纳入 credential vault 信任边界，必须依靠其访问控制与保护承担风险；
- lookup 仍可另存 peppered HMAC 以支持索引认证；
- persistence record 绝不能直接作为 API DTO；
- plaintext 不能进入 list、普通 detail、error、log、audit、trace、debug dump、URL 或非 secret-bearing response；
- create/reveal 是否需要 password confirmation、允许哪些 owner、是否返回 inactive secret，由 owning system contract 明确；跨系统默认只要求 JWT，但系统可以采用更严格规则；
- frontend 是否暂存 plaintext 由 owning design 决定，但 localStorage 等持久化必须单独批准。

当前 Identity PAT 是第一种获批例外：它要求 owner JWT 加当前账号 password，支持 reveal revoked/expired PAT，并记录不含 secret 的 actor/resource/result 日志。

## 不适用范围

以下仍禁止 plaintext persistence：

- account password；
- JWT signing secret；
- private key、Authorization header、pack body；
- 没有获批 repeatable reveal contract 的 PAT、distribution credential 或其他 secret。

## Consequences

- 数据库或备份泄露可直接转化为 Git/subscription credential 泄露，这是已明确接受的风险。
- Backup、export、support dump 和数据库访问权限必须按 secret material 处理。
- Future repeatably revealable distribution credential 可以引用本 ADR，但仍需自己的 system/API approval。
- 将现有 one-time credential 改为 revealable 是安全 contract 变更，不能只增加一个数据库列。

## Rejected alternatives

- HMAC-only、创建时显示一次：不满足重复查看目标。
- AES-GCM application encryption：被拒绝以避免额外 key/rotation/format complexity。
- 全局允许任意 secret 明文：范围过宽；例外只由获批 reveal contract 触发。

## Related design

- [Identity Authentication](../design/systems/01-identity-authentication/)
- [GORM/API boundary](0002-data-boundary-separation.md)
