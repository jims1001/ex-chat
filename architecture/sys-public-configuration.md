# SYS persisted public-branding snapshot (phase26)

This stage implements only the already-declared `sys.configuration.get.v1`
query on the authenticated independent module transport. It reads SYS-owned
`SystemConfig` directly through its repository, without a foreign table,
dependency call, schema change, command, event, or new application HTTP route.
API remains the existing declared SYS consumer. This is one owner-local query
slice; `independentRuntimeImplemented` remains false. SYS startup and capability
dependencies, and IAM/TEN/ENT metadata, remain unchanged.

## Public data boundary

The fixed allowlist is section 8 of
[the installation configuration dictionary](../Chatwoot完整文档/Chatwoot产品与API文档/24-Feature开关版本与安装配置字典.md).
It contains exactly these ten keys:

`INSTALLATION_NAME`, `LOGO_THUMBNAIL`, `LOGO`, `LOGO_DARK`, `BRAND_URL`,
`WIDGET_BRAND_URL`, `BRAND_NAME`, `TERMS_URL`, `PRIVACY_URL`, `DISPLAY_MANIFEST`.

Only stored `config_key` and `value` strings are returned, in ascending key
order, inside the existing transport `data` array. Missing keys are omitted;
no matching rows returns `[]`. Empty strings remain empty. `DISPLAY_MANIFEST`
is an uninterpreted string, including literal `"false"`; it is not converted
to a boolean. There is no default, environment fallback, effective-value
resolution, URL validation/normalization, version, cache, or activation claim.
Consumers must handle these as untrusted persisted strings, not executable HTML.

Private, unknown, differently cased, prefixed and suffixed setting names are
excluded at SQL selection. No secret-value heuristics are used to decide which
keys become public. An operator must not store a secret under a documented
public key. IDs, timestamps, administrative fields, environment data, and
private config rows never form part of this DTO.

The same fixed public content is visible for every accepted actor/account.
The required nonzero envelope `account_id` is caller context for global content,
not an installation authority or account membership grant. The allowlisted
caller and bearer authenticate the calling module. The actor is asserted by that
caller; this query does not verify an end-user token. `SuperAdmin`, `System`,
or other valid actor values cannot select privileged settings.

## Exact request shape

POST to the existing `/contracts/queries/sys.configuration.get.v1` transport
with its configured caller, bearer, accepted contract version, request-size and
timeout rules. Supply the standard valid envelope with:

```json
{
  "query_type": "sys.configuration.get.v1",
  "account_id": 91,
  "actor": {"type": "User", "id": "7", "auth_source": "module"},
  "filter": {},
  "sort": ["config_key"],
  "page": "1",
  "fields": ["config_key", "value"],
  "consistency": "strong",
  "requested_at": "2026-10-07T00:00:00Z"
}
```

Omit `cursor` or use an empty string. Every filter key, different sort/field
sequence, projection, page/cursor, or consistency value is unsupported. There
is no arbitrary-key, admin, status, job, or privileged query variant. Invalid
envelopes, unknown envelope fields and trailing non-whitespace JSON are denied
by the transport. The query also validates the complete supported shape before
reading the store.

## Bounds and stable failure behavior

Each value must be a stored string containing valid UTF-8 and at most 4,096
bytes. NULL, non-string storage values, invalid UTF-8, duplicate keys and oversized
data fail closed for the entire query. SQLite's byte-length guard includes NUL
bytes; PostgreSQL uses `octet_length`. The SQL `CASE` returns NULL instead of
transferring an oversized value. Only the two selected columns for the ten
allowlisted keys are read, with an eleven-row corruption-detection limit.

The complete JSON-encoded `data` array, including keys, punctuation, field
names and escaping, must be at most 16,384 bytes. This is a data limit; the
existing transport correlation metadata and enclosing object are separate.
Exactly-at-limit values/data are supported. There is no truncation or partial
result on failure.

- Unsupported query shape: HTTP 400, `validation_failed`, message
  `public configuration query shape is unsupported`
- Invalid/oversized stored data or store failure: HTTP 503,
  `dependency_unavailable`, message `public configuration is unavailable`
- Existing transport authentication, envelope, version, and timeout errors
  retain their existing types and status codes

Errors contain no rejected key/value, SQL detail, partial result or admin
metadata. No query writes a config row, receipt, local journal, outbox, or cache.

## Existing HTTP and recovery boundaries

Phase26 left `/api/v1/super_admin/configs` GET/POST unchanged. The subsequent
[phase27 administrative redaction repair](admin-config-redaction.md) gives that
existing REST surface a separate redacted DTO and explicit replacement/preserve
contract while retaining its freshly loaded SuperAdmin authorization. Privileged
independent SYS authority, SYS command/status/job runtime handlers, and overall
SYS/ARCH-08 acceptance remain open. This query confers no global write privilege.
The existing `/super_admin/instance_status` route now has separate
[SuperAdmin authorization and opened-dialect reporting](instance-status-diagnostics.md).
Its status/version placeholders still must not be interpreted as readiness or
the actual running release.

The owner `SystemConfig` JSON tags and generic SYS backup semantics remain
unchanged. Private keys/values and owner metadata remain in owner-only backups;
after restore, this separate public query still excludes them. Restore is the
existing offline owner operation, not part of the public transport.

## Verification scope

Focused SQLite and native PostgreSQL tests cover exact names and string values,
missing/empty results, byte and escaped-JSON boundaries, owner-only reads, static
errors and private-data-preserving snapshot/restore. SQLite additionally probes
corrupt non-string/NULL/duplicate rows and invalid UTF-8. Native PostgreSQL also
executes the read inside a read-only transaction. Transport tests cover caller,
bearer, version, envelope and scope-expansion denials; race tests cover concurrent
reads. Existing SuperAdmin REST compatibility and strict ownership/dependency/
contract guards remain required. Aggregate evidence accompanies the delivered
patch; focused tests alone do not establish full module acceptance.
