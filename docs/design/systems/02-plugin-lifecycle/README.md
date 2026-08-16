# Plugin Lifecycle

## 范围

本系统定义 Plugin 的完整用户可见生命周期。Plugin 是唯一 user-facing resource；每个 Plugin 与一条隐藏 bare Repository 组成同步、强制的一对一 aggregate，并共享同一 UUID。Repository 不拥有独立 namespace、slug、权限或 CRUD。

覆盖范围：

- namespace-scoped Plugin create/read/list、visibility、archive/restore；
- hidden Repository provisioning 与 operational state；
- development Git read/write authorization；
- canonical tag validation、Version publish/default/delete/restore；
- Marketplace revision projection 协调；
- durable receive intent、CAS、fail-closed read 与 reconciliation。

不在本 slice：Marketplace authoring API、private distribution credential 设计、SSH、team role matrix、物理删除、frontend implementation，以及任何跨 SQL/Git/filesystem ACID 承诺。

## 设计记录

| 文档 | Status | Approval |
|---|---|---|
| [System design](system-design.md) | `approved` | 2026-08-16，implementation not approved |
| [Persistence design](persistence-design.md) | `approved` | 2026-08-16，implementation not approved |
| [Management API contract rationale](management/api-contract.md) | `approved` | 2026-08-16，implementation not approved |
| [Git contract](git/api-contract.md) | `approved` | 2026-08-16，implementation not approved |

## Contract sources

- 精确 proposed management wire：[management-v1-design.yaml](../../../../api/openapi/management-v1-design.yaml)
- 当前 deployed management wire：[management-v1.yaml](../../../../api/openapi/management-v1.yaml)
- 当前请求平面与 Git Smart HTTP 规则：[protocols.md](../../../protocols.md)
- 当前实现能力汇总：[current-state.md](../../../current-state.md)

## 当前基础与实现边界

当前 runtime 已有 development Smart HTTP、bare repository 原语、Plugin/Version/Marketplace persistence foundation、immutable projection builder 和 read-only distribution reader。它没有本设计的完整 Plugin lifecycle services/routes、shared-ID target migration、protected receive admission、receive intent 或 reconciler。

目标仍保持五个顶层模块。`backend` 拥有 Plugin policy、lifecycle、authorization 与 SQL orchestration；`git` 只拥有 Git/storage/quarantine/projection mechanics。跨模块只传窄 interface/value DTO，不暴露 GORM records、DAO、handler、concrete service 或 server path。

## 状态

- **Design status:** `approved`
- **Design approval:** 2026-08-16，用户要求将交互确认结果固化并提交。
- **Implementation status:** not implemented
- **Implementation approval:** none；每个实现 slice 仍需单独批准。
