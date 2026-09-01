# User and Team Lifecycle

- **Status:** `implemented`
- **Owner:** `backend/domain/identity` and `backend/domain/authorization`; frontend is the existing static `frontend` module
- **Created:** 2026-08-31
- **Approval record:** user approved design and implementation on 2026-09-01; backend lifecycle is implemented

This system extends the existing identity foundation with optional public registration, system-administrator user and administrator-membership management, Team namespaces, direct Team membership, and existing-user invitations. It adds no top-level kernel module.

Documents:

- [System design](system-design.md)
- [Persistence design](persistence-design.md)
- [Management API rationale](management/api-contract.md)
- Exact deployed wire contract is authoritative in [`api/openapi/management-v1.yaml`](../../../../api/openapi/management-v1.yaml); the design document retains future proposed slices.

Out of scope: email/unregistered-user invitations, email delivery, password reset, email verification, service accounts, custom roles, Team deletion, singular ownership transfer, durable audit storage/query API, Marketplace authoring, SSH, and OIDC/SSO.
