# ADR-0005: Management API Success Envelope

- **Status:** Approved
- **Date:** 2026-08-10
- **Last reviewed:** 2026-08-11
- **Approval record:** 用户确认统一 `data` success、page/size/total list 和 direct error

## Context

历史 management routes 曾混用 direct health、direct cursor list 和 enveloped create response。Identity compatibility slice 已将当前 health/PAT API 统一为本 ADR 的 success envelope，同时保留简单、稳定的 direct error。

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

当前 health 与 token list 已完成 compatibility implementation，并由 deployed OpenAPI 描述为 enveloped health 与 page/size/total list。

## Consequences

- Frontend 对 target success 使用统一 `data` extraction。
- List metadata 位置固定，不引入 opaque cursor contract。
- Exact total 会增加 count query 成本，repository 必须 tenant-scoped 且 indexed。
- Identity direct/cursor compatibility 已结束；客户端以当前 deployed OpenAPI 的 envelope 与 page/size/total contract 为准。

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
