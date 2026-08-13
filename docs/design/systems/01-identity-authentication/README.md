# Identity Authentication

## Scope

本系统拥有 management password login、stateless frontend JWT，以及用户 PAT 的创建、分页、reveal 和 revoke。PAT 只表达 subscription/Git capability，不是 management API credential。

## Design records

| Document | Status | Approval |
|---|---|---|
| [System design](system-design.md) | `approved` | 2026-08-12 交互评审确认 |
| [Persistence design](persistence-design.md) | `approved` | 2026-08-12 交互评审确认 |
| [Management API contract rationale](management/api-contract.md) | `approved` | 2026-08-12 交互评审确认 |
| [Git credential contract](git/api-contract.md) | `approved` | 2026-08-12 交互评审确认 |
| [Subscription distribution credential contract](distribution/api-contract.md) | `approved` | 2026-08-12 交互评审确认 |
| [ADR-0006: Repeatably revealable credential storage](../../../decisions/0006-repeatable-credential-plaintext-storage.md) | `approved` | 2026-08-12 交互评审确认 |

## Contract sources

- 精确 proposed management wire：[management-v1-design.yaml](../../../../api/openapi/management-v1-design.yaml)
- 当前 deployed wire：[management-v1.yaml](../../../../api/openapi/management-v1.yaml)
- Management 公共约定：[management-api-contract.md](../../management-api-contract.md)

## Implementation status

- **Status:** `implemented`
- **Runtime ownership:** `backend` module 内部的 `domain/identity/{model,dao,service}` 与 `handler/identity`
- **Deployed management wire:** login、health、PAT list/create/revoke/reveal 已提升到 [management-v1.yaml](../../../../api/openapi/management-v1.yaml)
- **Evidence:** identity model/DAO/service/handler tests、backend route tests、OpenAPI parse/local-reference/conformance tests

实现保持五模块架构：`model` 拥有 GORM record/value，`dao` 拥有 owner-scoped query、migration 与 legacy guard，`service` 拥有 JWT/PAT/authorization 业务规则，handler 只处理 management wire。数据库内 repeatably revealable PAT `secret_plaintext` 使数据库及备份进入 credential trust boundary；旧 scope/HMAC-only credential schema 会拒绝启动，开发重建与非开发迁移边界见 [Persistence design](persistence-design.md) 和 [Operations](../../../operations.md)。

`implemented` 只说明该 identity slice 已接入当前 runtime；PostgreSQL/MySQL/race/frontend 覆盖是否实际执行仍必须按本次验证结果单独报告，不能由本文状态推导。源码、tests、migration、routes、deployed OpenAPI 与 [`current-state.md`](../../../current-state.md) 仍共同决定当前事实。
