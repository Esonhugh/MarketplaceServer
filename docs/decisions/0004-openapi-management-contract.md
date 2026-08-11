# ADR-0004: OpenAPI Documents Deployed and Proposed Management APIs

- **Status:** Approved
- **Date:** 2026-08-10
- **Last reviewed:** 2026-08-11
- **Approval record:** 用户确认 OpenAPI 只作 API 文档，并将 deployed 与 proposed contract 分离

## Context

MarketplaceServer 需要可评审的 management API 文档，也希望提前设计未来 API。把 proposed route 写进 deployed contract 会虚报能力；只记录 deployed route 又无法承载前置 contract design。代码生成会增加不需要的 toolchain 和 generated-code boundary。

## Decision

维护两份 OpenAPI 3.1 文档：

- `api/openapi/management-v1.yaml`：只描述当前已注册和测试证明的 deployed behavior；
- `api/openapi/management-v1-design.yaml`：描述已评审 proposed target，不表示已实现。

OpenAPI 只用于 documentation、review、lint、reference/example validation 和实现对照：

- 不生成 Go DTO；
- 不生成 TypeScript types/client；
- backend 使用 handwritten wire DTO；
- frontend 使用 handwritten application API client；
- source/routes/tests 仍决定 deployed behavior；
- `docs/current-state.md` 仍维护当前 route/capability inventory；
- Git、distribution 和 `marketplace.json` 不进入 management OpenAPI。

Operation 实现并通过适用测试后，才从 proposed design 纳入 deployed contract。仅修改 YAML 不能作为 implementation evidence。

## Consequences

- 可以完整评审 target API，而不冒充 runtime capability。
- Handwritten client/DTO 避免 generated transport types 扩散。
- 两份文档必须清楚标注 status，shared convention 变化时需同时审查 compatibility。
- Deployed behavior 与 target 不同时，design 文档明确记录迁移 debt。

## Rejected alternatives

### 一份 OpenAPI 同时混合 deployed/proposed operations

Rejected：client 无法判断 route 是否存在，也容易把设计误报为产品能力。

### 从 OpenAPI 生成 frontend/backend code

Rejected：当前规模不需要生成链，handwritten client/DTO 更直接。

### 只在实现后才设计 API

Rejected：无法在 persistence、authorization 和 frontend 实现前评审完整 contract。

## Related design

- [Management API contract](../design/management-api-contract.md)
- [Deployed OpenAPI](../../api/openapi/management-v1.yaml)
- [Proposed OpenAPI](../../api/openapi/management-v1-design.yaml)
