# ADR-0002: Separate GORM Records from Management API DTOs

- **Status:** Approved
- **Date:** 2026-08-10
- **Last reviewed:** 2026-08-11
- **Approval record:** 用户确认“隔离数据库和 API 即可”

## Context

直接把 GORM record 作为 HTTP JSON 会让数据库字段、association、secret 和 schema evolution 进入 public contract。另一方面，把所有内部值拆成 persistence/domain/projection/contract/wire 多层并强制 mapper，会增加大量重复类型和维护成本。

## Decision

只强制隔离两个边界：

1. GORM persistence records；
2. `/api/v1` management request/response DTOs。

GORM record 拥有 database tags、relationships、indexes、hooks 和 persistence-only fields，不能由 handler 直接序列化。API DTO 拥有 JSON field names、public optionality 和 wire validation，不能作为 GORM schema。

Handler/service boundary 使用最简单的显式转换。可以直接字段赋值；只有有复用价值时才引入 mapper。Domain values、query projections 和 cross-module contracts 可按需要定义或安全复用，不需要加入固定 taxonomy。

仍然必须：

- cross-module contract 保持 narrow，不暴露 GORM、Jin、handler、DAO、concrete service、secret 或 server path；
- tenant-owned query/mutation 在数据库 predicate 中包含 namespace/resource scope；
- database scope 不替代 service authorization；
- external `marketplace.json` 与 management API 保持独立。

## Consequences

- 公开 JSON 不会随数据库结构意外变化。
- 内部实现可以保持简单，不为每层复制相同 struct。
- Existing internal result 带 JSON tags 只有在实际造成 DB/API coupling 时才需要由 owning slice 调整。
- 本 ADR 不新增 route、table 或 mapper framework。

## Rejected alternatives

### 一个 struct 同时承担 GORM 与 API

Rejected：容易泄露 persistence-only fields，并把 storage evolution 变成 API breaking change。

### 强制六类数据结构和每次 boundary mapper

Rejected：复杂度超过当前产品需要，增加重复代码而不能对应提升安全性。

## Related design

- [通用数据边界](../design/common-data-contracts.md)
- [GORM persistence](../design/persistence-models.md)
