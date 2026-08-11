# 设计文档索引与权威边界

本目录保存 MarketplaceServer 的已评审设计。设计描述目标边界，不证明对应代码、route、migration 或 frontend 已实现。

## 状态与事实来源

| Status | Meaning |
|---|---|
| `proposed` | 正在讨论；无 implementation authority |
| `approved` | 设计语义已批准；实现仍需单独批准 |
| `implemented` | 已有代码、测试和适用运行时证据 |
| `superseded` | 已由链接的新设计取代 |

事实来源：

- 当前能力：源码、tests、migration、route、runtime config，以及 [`current-state.md`](../current-state.md)；
- 未来顺序：[`roadmap.md`](../roadmap.md)；
- 五模块和 DI：[`architecture.md`](../architecture.md)、[`di-reference.md`](../di-reference.md)；
- ownership/security/publication：[`product-invariants.md`](../product-invariants.md)；
- REST/Git/distribution：[`protocols.md`](../protocols.md)；
- deployed management wire：[OpenAPI](../../api/openapi/management-v1.yaml)；
- proposed management wire：[OpenAPI design](../../api/openapi/management-v1-design.yaml)；
- Marketplace external format：[格式来源](../../schemas/marketplace/README.md)。

## 通用设计

- [通用数据边界](common-data-contracts.md)：只强制隔离 GORM record 与 management API DTO。
- [GORM persistence](persistence-models.md)：显式 records、ordered AutoMigrate/tags、tenant-scoped queries 和 migration tests。
- [Management API](management-api-contract.md)：deployed/proposed OpenAPI、envelope、page/size、JWT/PAT 和 handwritten frontend client。
- [System design template](system-design-template.md)：后续系统使用的精简七部分模板。

## ADR

只为会长期影响多个系统的重大选择维护 ADR：

- [ADR 0001: DB/API string identifiers](../decisions/0001-string-identifiers.md)
- [ADR 0002: GORM/API boundary separation](../decisions/0002-data-boundary-separation.md)
- [ADR 0003: GORM AutoMigrate authority](../decisions/0003-gorm-automigrate-authority.md)
- [ADR 0004: OpenAPI documentation split](../decisions/0004-openapi-management-contract.md)
- [ADR 0005: API success envelope](../decisions/0005-api-success-envelope.md)

小范围 field、route 或实现选择留在 owning system design，不创建 ADR。

## 使用规则

1. 先核对 current-state 和源码/tests，再写计划增量。
2. 从七部分 template 开始，只写本系统需要的细节。
3. proposed API 放入 design OpenAPI；实现并验证后才移入 deployed OpenAPI。
4. 设计批准不自动授权代码；遵循 [`ai-development.md`](../ai-development.md) 的 TDD、Agent ownership 和 feature commit 规则。
5. 实现后只更新对应事实的唯一权威文档，不能复制另一份 route/feature inventory。
