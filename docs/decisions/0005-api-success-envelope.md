# ADR-0005: Management API Success Envelope

- **Status:** Approved
- **Date:** 2026-08-10
- **Last reviewed:** 2026-08-11
- **Approval record:** 用户确认统一 `data` success、page/size/total list 和 direct error

## Context

Current management routes 混用 direct health、direct cursor list 和 enveloped create response。Target API 需要统一 success extraction，同时保留简单、稳定的 direct error。

## Decision

所有 target `/api/v1` non-`204` success 使用：

```json
{
  "data": {}
}
```

List 使用：

```json
{
  "data": {
    "items": [],
    "page": 1,
    "size": 20,
    "total": 0
  }
}
```

- `page` default/minimum 为 1；
- `size` default 20，maximum 100；
- `total` 是授权和 filter 后的 exact count；
- server 使用稳定固定顺序；
- `204` 没有 body；
- error 保持 direct `{code,message,requestId,details?}`；
- `message` 不作为 client enum。

Current health 和 token list 仍由 deployed OpenAPI 按实际 direct/cursor shape 描述，直到独立 compatibility implementation slice 完成。

## Consequences

- Frontend 对 target success 使用统一 `data` extraction。
- List metadata 位置固定，不引入 opaque cursor contract。
- Exact total 会增加 count query 成本，repository 必须 tenant-scoped 且 indexed。
- 已部署 direct/cursor clients 在迁移前继续按 deployed contract 工作。

## Rejected alternatives

### Cursor pagination 作为通用 contract

Rejected：用户选择更直观的 page/size/total management UI contract。

### Error 也放入 `data`

Rejected：会破坏 direct error discrimination。

### `204` 返回 `{ "data": null }`

Rejected：与 no-content semantics 冲突。

## Related design

- [Management API contract](../design/management-api-contract.md)
- [ADR-0004](0004-openapi-management-contract.md)
