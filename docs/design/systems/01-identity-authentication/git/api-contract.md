# Identity Git Credential Contract

- **Status:** `approved`
- **Request plane:** `/git`
- **Owner:** identity principal production in `backend`，Git protocol enforcement in `git`
- **Created:** 2026-08-12
- **Last reviewed:** 2026-08-12
- **Approval record:** 用户通过逐项 AskUser 评审直接批准

本文件只定义 identity credential boundary，不重述 Git Smart HTTP path、service 和 pack protocol；精确 Git wire 由 [`protocols.md`](../../../../protocols.md#开发-git-smart-http--已实现) 及后续 Plugin/Git owning design 管理。

- Basic username + `git-clone` 或 `git-write` PAT 可尝试 upload-pack；最终仍要求目标 Plugin 当前 read policy。
- Basic username + `git-write` PAT 可尝试 receive-pack；最终仍要求目标 Plugin 当前 write policy 与 protected-ref rules。
- `sub-read` PAT、account password 和 JWT 在 `/git` 明确拒绝。
- PAT preset 是 capability，不是独立资源授权，不得扩大 owner 权限。
- Unknown/revoked/expired PAT 使用稳定的非泄露认证失败。
