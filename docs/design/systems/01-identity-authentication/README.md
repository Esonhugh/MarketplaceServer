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

## Implementation authority

设计已批准，但尚未授权或证明 production implementation。当前 route、schema 和认证行为仍以源码、tests、deployed OpenAPI 与 [`current-state.md`](../../../current-state.md) 为准。
