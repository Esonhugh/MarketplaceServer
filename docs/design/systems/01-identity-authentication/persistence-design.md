# Identity Authentication Persistence Design

- **Status:** `approved`
- **Owner:** `backend/domain/identity`
- **Created:** 2026-08-12
- **Last reviewed:** 2026-08-12
- **Approval record:** 用户通过逐项 AskUser 评审直接批准

> 本 schema 已由 `domain/identity/model`、`dao` 和 `service` 实现；PostgreSQL production 与 SQLite 单进程测试覆盖必须引用实际 test report，不能仅由设计状态推定。MarketplaceServer 运行时不支持 MySQL。

## 1. Schema 增量

`users` 不增加 `auth_version`、login metadata、JWT session 或 refresh-token 字段。JWT 签发和验证不改变 User record。

目标 `personal_access_tokens` 只保留一个当前模型，不设计 legacy HMAC-only record、nullable compatibility state、backfill 或双读分支：

| Field | Go/DB representation | Null/default | Constraint/index | Meaning |
|---|---|---|---|---|
| `id` | `string`, canonical UUID, `char(36)` | non-null | primary key | PAT resource ID |
| `user_id` | `string`, canonical UUID, `char(36)` | non-null | FK users, owner/list index | owner |
| `name` | `string`, max 128 | non-null | owner-scoped management field | user label |
| `preset` | `string`, max 32 | non-null | check in `sub-read`,`git-clone`,`git-write` | stable cumulative capability preset |
| `secret_plaintext` | `string`, max 64 | non-null | never serialized by persistence record | repeatably revealable `mpsk_...` PAT |
| `secret_hmac` | `string`, max 128 | non-null | unique index | peppered equality lookup |
| `expires_at` | `*time.Time` | nullable | index | optional future expiry |
| `last_used_at` | `*time.Time` | nullable | none | successful credential use fact |
| `revoked_at` | `*time.Time` | nullable | index | explicit revoke fact |
| `created_at` | `time.Time` | non-null | owner pagination index | creation fact |
| `updated_at` | `time.Time` | non-null | none | mutable metadata fact |

`personal_access_token_scopes` 不属于当前模型；preset 名是唯一持久化 capability 表达。Startup guard 发现旧 scope table，或已有 PAT table 缺少 `preset`、`secret_plaintext`、`secret_hmac` 时返回 `identity: legacy credential schema requires operator rebuild`，不会自动删除、backfill 或双读。开发环境只有在 operator 确认数据可丢弃后才能停止服务并重建整个开发数据库；非开发环境必须先备份并交付显式 PAT migration/rotation 方案。

## 2. Secret boundary

经 [ADR-0006](../../../decisions/0006-repeatable-credential-plaintext-storage.md) 批准，`secret_plaintext` 是有限的 persistence 例外：主库、replica、dump、snapshot、PITR archive、backup/restore operator 都进入 credential trust boundary，必须按可直接使用的 secret material 控制访问、传输、恢复与销毁。账号 password 仍只保存 Argon2id hash；JWT signing secret 不入库。

GORM record 与 management DTO 必须隔离：

- list/query projection 不 select `secret_plaintext`；
- 普通 token metadata result 不包含 secret；
- create/reveal service 使用专用 result 显式读取 secret；
- HMAC、plaintext、password hash 不进入 error、log、audit 或 debug dump。

## 3. Queries

### Owner-scoped list

```text
WHERE user_id = resolved JWT username owner ID
ORDER BY created_at DESC, id DESC
LIMIT size OFFSET (page - 1) * size
```

同一 owner predicate 执行 exact `COUNT(*)`。Page 最小 1；size 默认 20、最大 100。默认包含 active、expired、revoked 全部记录。

### Owner-scoped mutation/reveal

Revoke 和 reveal 必须将 owner scope 放入 repository predicate，或先通过 canonical username 安全解析 owner ID 后使用 `(id,user_id)`；禁止全局读取 token 后在 handler 内比较 owner。

### Credential lookup

认证从完整 PAT 计算 peppered HMAC，通过唯一 `secret_hmac` 等值定位。随后检查：

1. preset 是否允许当前 plane/operation；
2. `revoked_at`；
3. `expires_at`；
4. owner 当前资源 policy。

`secret_plaintext` 不参与 lookup。

## 4. Derived status

数据库不保存随时间漂移的 status。API/service 计算：

```text
revoked_at != null             => revoked
else expires_at <= now         => expired
else                           => active
```

Revoked 优先于 expired。Revoke 只设置 `revoked_at`，不删除记录或 secret；重复 revoke 是幂等 no-op。

## 5. AutoMigrate 与测试

Schema authority 仍是 identity GORM records、ordered model list 和 `mod/backend/migrate.go` 的 `AutoMigrate` coordinator。

当前测试覆盖 model/ordered migration list、legacy guard、三个 preset allow/unknown deny、duplicate HMAC/owner predicate、page/total/order、revoke/expiry status，以及 metadata projection 不读取或泄露 `secret_plaintext`。PostgreSQL 条件 suite 需要 `MARKETPLACE_TEST_POSTGRES_DSN`；SQLite 只覆盖单进程开发/测试边界。

本实现不提供旧 PAT 数据迁移。开发数据库重建流程见 [Operations](../../../operations.md)；任何非开发环境存在数据时必须停止并设计显式迁移/credential rotation，而不是静默删除。
