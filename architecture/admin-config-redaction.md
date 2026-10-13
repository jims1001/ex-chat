# SuperAdmin persisted configuration redaction (phase27)

This repair applies only to the existing `GET` and `POST`
`/api/v1/super_admin/configs` routes. The existing JWT middleware freshly loads
the user before `RequireSuperAdmin` applies its current user type, role or
configured email policy. Tenant administrators are not installation
administrators. No route, authorization grant or independent SYS privileged
resolver is added.

## Administrative read contract

GET retains its envelope and `id`, `config_key`, `value`, `updated_at` fields.
Each row adds the booleans `redacted` and `configured`:

- A fixed exact-case allowlist of the 73 non-sensitive keys in sections 8–15 of
  [the installation configuration dictionary](../Chatwoot完整文档/Chatwoot产品与API文档/24-Feature开关版本与安装配置字典.md)
  keeps its exact persisted value and sets `redacted:false`
- The dictionary's 29 sensitive keys and every other key return `value:""` and
  `redacted:true`. This includes unknown keys, spelling/case variants and
  source-used credentials missing from the dictionary. `LANGFUSE_PUBLIC_KEY`
  and `SHOPIFY_CLIENT_ID` are among its explicitly sensitive keys
- `configured` means only that the stored value is not an empty string. It is
  independent of redaction and is false for an empty stored value. Whitespace
  is nonempty. It does not establish provider validity or runtime activation

Redaction emits no mask text, prefix, suffix or secret length. GET no longer
provides credential recovery. The allowlist is compiled into the administrative
DTO, does not grant public access, and requires an explicit code change to
expand. Values placed under readable names remain readable; there is no
content-based secret detector. Clients must treat readable stored strings as
untrusted content.

## Preserve and explicit replacement

POST still requires a nonempty string `config_key`. Its response retains `key`
and `updated`, with these meanings:

- Omitted, JSON null or empty-string `value` preserves the setting and returns
  `updated:false`. This performs no configuration read or write, does not bump
  `updated_at`, and does not create a row for a missing key
- A nonempty string `value` without a `redacted` field is explicit replacement,
  returns `updated:true`, and stores the string unchanged, including whitespace.
  A single atomic upsert on `config_key` creates or updates the row and updates
  only `value` and `updated_at` on conflict. Existing IDs are preserved and
  concurrent replacements retain last-writer-wins semantics
- A nonempty `value` together with any `redacted` marker is rejected before
  persistence, including `redacted:false`. This prevents a response DTO or a
  presentation mask marked as redacted from becoming a replacement credential
- A present `redacted` marker must be a JSON boolean, even for preserve input.
  Null, strings, arrays, objects and numbers are rejected, including malformed
  earlier duplicate markers. Invalid JSON, wrong field types, missing/empty
  keys and trailing JSON are rejected with a static 400 response

An unchanged sensitive GET row roundtrip has an empty value and safely
preserves the setting. A readable GET row with a nonempty value retains
`redacted:false` and is therefore rejected if posted intact. To replace any
setting, clients must send the ordinary explicit payload containing only
`config_key` and the desired nonempty `value`, without the response marker.
There is no value-based mask heuristic: an unmarked string is replacement
intent. There is no new clear, delete, compare-and-swap or alternate authority
operation. Unknown-key write authority remains the existing SuperAdmin policy.

The preserve path never reads an old value and writes it back, so it cannot
undo a concurrent replacement. SQL failures return a static 500 and the atomic
statement leaves the old row intact or the new row absent.

## Logs, owner data and public configuration

The system-config repository uses a local silent GORM session. The caller's
global logger is unchanged. The handler logs safe operation messages without
keys, values or raw database errors. Request logging omits the full query only
on the config route and its trailing-slash spelling; other routes retain their
existing rules. Tests use only synthetic canaries to inspect success, malformed
input and injected database failures.

`domain.SystemConfig` retains all four original JSON fields. Its raw owner
repository reads and owner-only backup/restore remain complete, including
private values; they must remain private. The phase26 persisted public snapshot
still exposes only its ten existing keys and unchanged contract. Its test for
an empty stored row seeds the owner row directly because empty admin writes
now preserve absence. No public-query production code changes.

These endpoints operate on persisted settings only. Many runtime services
consume environment or application configuration; an accepted replacement
does not establish provider credential rotation, reload or activation. No
environment fallback is added. No frontend or SDK is supplied here, so client
compatibility is verified at the API shape level. Privileged independent SYS
runtime work and overall SYS/ARCH-08 acceptance remain open.

## Verification scope

The focused fixtures cover the complete 102-key catalog, empty/configured
states, unknown variants, response and log non-disclosure, preserve and marker
behavior, exact replacements, SQL failures and concurrent writers. Actual-route
tests retain type/role/email authorization paths, tenant-admin/agent/anonymous
denials and fresh demotion checks. SQLite and native PostgreSQL tests exercise
atomic writes, read-only preservation and concurrency. Existing owner snapshot
and public snapshot regressions remain required. Aggregate outcomes are
recorded separately in the delivered evidence; this document alone makes no
full-suite or deployment acceptance claim.
