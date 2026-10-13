# Installation-status diagnostic authorization

The existing `GET /super_admin/instance_status` is an installation-management
route, as documented in `25-SuperAdmin与安装级管理功能.md` sections 1 and 17. It now
runs the existing access-token/revocation middleware and the same live
`RequireSuperAdmin` policy as the management API. Persisted User type/role and
the operator-configured SuperAdmin email list authorize access; tenant
administrator membership and request/JWT privilege hints do not. Each request
reloads the user. The root path and HTTP method are unchanged; no
`/api/v1/super_admin/instance_status` alias is introduced.

The existing response remains a three-string object. `database` comes from the
opened GORM database's `Dialector.Name()` and therefore reports `sqlite` or
`postgres` independently of an unrecognized fallback/stale configuration value.
`status: "online"` and `version: "4.16.0"` remain compatibility placeholders;
this response does **not** establish readiness or the actual running release.
Version/revision/edition/license/migration/cache sources and unknown-status
semantics require separate product decisions. No Redis/provider probes were
added. The public `/health` response and authorization behavior are unchanged.

This bounded monolith repair does not register privileged independent SYS
contracts. The declared `sys.instance.status.v1` and `sys.jobs.observe.v1`
remain unavailable; actor claims are not a new global authority source.

Route regressions cover anonymous, invalid, expired and revoked credentials;
agent/tenant-admin denial without diagnostic payloads; existing type, role and
configured-email policies; fresh demotion with reused credentials; the public
health route; SQLite fallback and native PostgreSQL dialect/config mismatch.
