# ADR-0001: Database and API Identifiers Use Strings

- **Status:** Approved
- **Date:** 2026-08-10
- **Last reviewed:** 2026-08-11
- **Approval record:** 用户确认“数据库和 API 用 string”，内部实现可使用 UUID 类型

## Context

GORM 和 JSON 都需要稳定、可移植的 textual identifier representation；内部 service 与跨模块 capability 则可能从 `uuid.UUID` 等强类型中获益。要求所有层统一成同一 Go type 会制造无业务价值的转换或弱化内部类型安全。

## Decision

- GORM persistence record 的业务 ID field 使用 `string`。
- Management API request/response 的 ID field 使用 `string`。
- UUID-backed string 在 creation、database write 和 API ingress 校验 canonical lowercase hyphenated UUID。
- Internal domain、service、query 和 cross-module contract 可以使用 `uuid.UUID` 或 string，由 owning implementation 选择。
- DB/API boundary 负责 string 与 internal type 的显式转换。
- `StorageKey`、`PublicKey`、`CommitSHA`、`TagName`、`Slug`、`Username` 和 `RequestID` 是独立语义，不能因底层是 string 而互换。
- storage key 与 filesystem path 不进入 management API DTO。

## Consequences

- Database schema 和 public JSON 不依赖特定 UUID library。
- 内部 contract 不需要为了表面统一而移除合适的 UUID 类型。
- Boundary validation 和 qualified field names 仍然必需。
- 当前内部 `uuid.UUID` contract 不是 consistency debt，不需要机械迁移。

## Rejected alternatives

### 所有层都使用 string

Rejected：会丢失有价值的内部类型信息，并强制无关模块遵守 wire/persistence representation。

### 所有层都使用 `uuid.UUID`

Rejected：不是所有 locator 都是 UUID，GORM/API 仍需要 textual representation，并会把公开 contract 绑定到 Go library。

## Related design

- [通用数据边界](../design/common-data-contracts.md)
- [GORM persistence](../design/persistence-models.md)
