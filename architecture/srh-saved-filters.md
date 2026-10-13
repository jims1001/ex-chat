# SRH private saved-filter runtime

This stage implements only the already-declared `srh.custom_filter.manage.v1`,
`srh.custom_filter.get.v1`, and `srh.custom_filter.changed.v1` independent owner
surface. Search execution, indexing, checkpoints, account-index purge, frontend
adoption, and overall SRH/ARCH-08 acceptance remain incomplete.
`independentRuntimeImplemented` remains false. No provider or dependency edge is
added: membership uses the existing SRH → TEN public query.

## Authority and ownership

Use authenticated, allowlisted module contract transport. The asserted actor
must be `User`, with a canonical positive decimal ID and `auth_source: module`.
The caller's internal module credential authenticates the calling module; this
stage does not verify a separate end-user token or prevent a trusted credential
holder from asserting a different User actor.

Before every query and before any command receipt replay, SRH calls
`ten.permissions.check.v1` through `moduleClient.requireMember`. Revoked or
missing membership returns the existing public-port `resource_not_found` result;
a TEN outage fails closed with `dependency_unavailable`. A cache hit is never a
membership grant. The membership check and local transaction are not a distributed
transaction; TEN is authoritative at the time of the check.

All rows are scoped by envelope account ID and actor user ID. No administrator
exception exists. Other users' or accounts' detail/write targets return not-found.
Input/filter ownership fields, caller-assigned IDs, versions and timestamps are
rejected. The entire actor is part of this new command's receipt digest. Other
capabilities retain their prior digests and receipt-first preflight semantics.

## Commands

All commands use `srh.custom_filter.manage.v1` and a stable idempotency key.

- Create: target `{type: "account", id: "<account_id>"}` and input
  `{operation: "create", name, filter_type, query}`. No expected version.
- Partial update: target `{type: "custom_filter", id: "<id>",
  expected_version: <version>}` and input `{operation: "update", ...fields}`.
  Submit at least one of name, filter_type or query. Omitted fields are preserved.
- Delete: the same versioned object target, input `{operation: "delete"}`.

Names must be nonblank strings of at most 255 Unicode characters. Filter types
are exactly `conversation`, `contact`, and `report`. `query` must be a JSON object,
including an empty object. Explicit null, strings containing JSON, arrays and
scalar values are rejected. Query contents are opaque saved criteria: unknown
keys, nested objects/arrays, null and exact JSON numeric values are preserved.
This contract neither executes criteria nor claims compatibility validation
against every CON/CUS/RPT evaluator. Transport request-size limits still apply.

Create/update return the full DTO: id, account_id, user_id, name, filter_type,
query as an object, created_at, updated_at, object_version, and change_id when a
change occurred. Delete returns id, account_id, user_id, deleted=true,
object_version and change_id. New rows start at version 1. Update/delete require
and atomically compare the current version; each actual change advances it by 1.
A stale version conflicts. Deletion may make a later request not-found.
A semantic update no-op keeps the version and updated_at, writes only its
idempotency receipt, and emits no fabricated change/outbox fact. JSON object key
order, whitespace, and equivalent decimal numeric representations are no-ops.
Large integers and decimals are never converted through binary floating point,
including on receipt replay.

## Queries

`srh.custom_filter.get.v1` reads the private SRH authority with
`consistency: "strong"`, `page: "1"`, `sort: ["-id"]`, `fields: ["*"]`, and no
cursor. These values are validated; other pagination, sorting, projections or
consistency modes are explicitly rejected, not silently ignored.

- Detail: `filter: {id: <id>}` returns one full DTO
- List: `filter: {}` or `{filter_type: "conversation|contact|report"}` returns the
  complete collection in descending ID order; an empty collection is `[]`

Combining an ID with filter_type, or including other filter keys, is invalid.
There is no page-size/cursor pagination in this bounded v1 surface. Queries write
no receipt, local change or event.

## Atomic persistence and privacy

The owner row/CAS, local change journal, outbox and command receipt commit in one
SRH transaction. Failure of any local write rolls them all back. Native PostgreSQL
row locking plus CAS serializes competing versions, including semantic no-ops;
receipt uniqueness handles simultaneous identical keys.

The changed event uses aggregate CustomFilter and the actual committed object
version. Its payload is only filter_id, user_id, filter_type, operation and
object_version, plus the envelope's changed-field names, actor, tenant,
correlation and causation. Neither the saved name nor query/private criteria
appear in change facts or outbox events. Private full DTOs remain in owner-only
receipts so a verified current owner can replay a result.

## Recovery and legacy boundary

Owner snapshots retain rows, versions, local changes, receipts and pending outbox
facts. A quiesced restore advances the filter ID sequence past the maximum
surviving row or retained SRH create/update/delete journal object ID, on both
SQLite and PostgreSQL. Thus deleting the highest (or every) filter before a
snapshot cannot recycle an old CAS identity after restore. Keep the owner journal
with the snapshot. Older snapshots or independently pruned journals without this
historical high-water evidence cannot establish non-reuse of deleted IDs; no such
guarantee is claimed for them. Restore remains an offline owner operation.

The additive ObjectVersion column defaults to 0 for existing/legacy rows; only
runtime-created rows start at 1. The private runtime excludes version-0 rows,
including shared user_id=0 entries. It does not adopt or reassign legacy ownership.

Phase25 repairs REST privacy with explicit writer separation. The existing REST
routes retain owned version-0 CRUD and string-query DTOs, include only the current
owner in reads, and preserve unowned rows without exposing them. REST may read
owned runtime rows but rejects updates/deletes with 409; it cannot bypass this
runtime's CAS/events/receipts. New REST rows remain at version 0. See
[REST compatibility and migration boundaries](rest-private-saved-filters.md).
Full REST-to-independent-SRH adoption remains open; the privacy repair does not
claim that deployment is integrated.

## Verification

Focused SRH tests exercise all three types, partial updates, opaque exact JSON,
semantic no-ops, tenant/user/actor privacy, forged ownership, membership denial
and outage before replay, actor/payload idempotency conflicts, local rollback,
owner-only storage and TEN public-port access. SQLite and native PostgreSQL tests
cover snapshot/restart replay and deleted-ID non-reuse. Native PostgreSQL race
tests cover competing updates/deletes and identical-key create/update/delete.
Private REST compatibility fixtures and strict architecture/storage guards remain required.
Aggregate gate results belong to the accompanying execution evidence; this note
does not imply that a focused pass establishes full module completion.
