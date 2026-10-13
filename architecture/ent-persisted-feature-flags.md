# ENT persisted account feature flags (phase28)

This slice implements the already-declared `ent.features.list.v1` query through
the existing authenticated independent module transport. It reads only ENT-owned
`AccountFeature` rows scoped by the envelope `account_id`, with no foreign table,
TEN callback, dependency change, new public REST route, write, event, receipt,
journal, cache or schema change. Existing REST response shapes/permissions and
`ent.capability.check.v1` are unchanged. Only this query is added to the ENT
runtime inventory; `independentRuntimeImplemented: false` and pending acceptance
remain in place. No independent feature mutation contract, quota/license logic or general ENT
completion is introduced. The related false-value persistence defects below are
repaired in existing owner writes/recovery.

## Data and authority

The existing persisted REST semantics (`EnterpriseRepository.GetAccountFeatures`
and `EnterpriseHandler.GetAccountFeatures`) are a feature-name to boolean map.
The new query returns that persisted map as transport `data`: for example,
`{"captain": true, "audit_logs": false}`. No rows yields `{}`, never `null`.
Explicit false is preserved, absent keys are omitted, and valid unknown keys
remain visible without a catalog filter or name normalization. IDs, account
IDs and timestamps are excluded. JSON key ordering is deterministic.

This is not an effective-capability or license answer. The product dictionary
`24-Feature开关版本与安装配置字典.md` sections 1–2 distinguishes stored account flags
from version, installation prerequisites, enterprise authorization, user role,
inbox and provider state. No default, environment fallback, edition, activation
or license decision is introduced.

Transport authorization is unchanged: the configured module bearer and an
allowed `X-Module-Caller` authenticate the caller. Existing API-to-ENT dependency
and query declarations authorize this account-scoped owner read; this slice adds
no caller or authority grant. The calling module asserts the account and actor.
The actor is **not** a verified end-user identity or a SuperAdmin authority proof.
The upstream gateway remains responsible for end-user authentication and tenant
access before invoking this internal query. Every accepted actor gets the same
persisted map for the envelope account and cannot override it with filter data.
A missing account has no persisted rows and returns `{}`; the query does not
consult TEN to prove that the account exists. Deployments must keep configured
module credentials and caller access within this existing trust boundary.

## Fixed request shape

POST `/contracts/queries/ent.features.list.v1` with the existing bearer, caller,
accepted contract version, body-size and timeout rules, using this shape:

```json
{
  "query_type": "ent.features.list.v1",
  "account_id": 91,
  "actor": {"type": "User", "id": "7", "auth_source": "module"},
  "filter": {},
  "sort": ["feature_name"],
  "page": "1",
  "fields": ["feature_name", "enabled"],
  "consistency": "strong",
  "requested_at": "2026-10-07T00:00:00Z"
}
```

`cursor` must be absent or empty. All filter keys, alternate field/sort sequences,
pagination and consistency modes are rejected. There is no account override,
search, arbitrary projection, catalog/default expansion or effective-state mode.
The transport rejects invalid envelopes, unknown fields, extra JSON values,
trailing garbage and oversized bodies. The query validates its full shape before
reading storage. `strong` here means a single direct storage statement snapshot,
without a cache or multiple independently read pages.

## Bounded, complete results

The read selects only `feature_name` and `enabled`, with an account predicate,
ordered by feature name and capped at 513 rows to detect the 512-row limit. Keys
must be nonempty UTF-8 strings of at most 400 bytes, matching the maximum UTF-8
byte width of the existing PostgreSQL 100-character key column. SQLite's byte
length includes embedded NULs; PostgreSQL uses `octet_length`. A SQL CASE prevents
an oversized key from being transferred. Empty/NULL/blob/invalid-UTF-8 keys,
duplicates, more than 512 rows or corrupt boolean storage fail the entire result.
Stored unknown keys and surrounding spaces are preserved within these bounds.

PostgreSQL values must scan as booleans. SQLite has no boolean storage class;
only persisted integer 0 and 1 are accepted, then converted to false and true.
Text, blobs, NULL, other integers and real numbers are rejected. Existing SQLite
column affinity may already normalize values before this read; it does not
recover the original write representation.

The complete JSON-encoded `data` map must be at most 65,536 bytes, including key
escaping and punctuation. Transport metadata/enclosing fields are outside this
limit. Exact byte/row/encoded limits succeed; overflow never returns truncation
or a partial map. One SELECT provides the snapshot, with request context for
cancellation. No output or cache is retained between calls.

Unsupported shape returns HTTP 400 `validation_failed` with
`persisted feature flags query shape is unsupported`. Any invalid/oversized
stored result or storage failure returns HTTP 503 `dependency_unavailable` with
`persisted feature flags are unavailable`. Errors include no rejected key/value,
SQL detail or partial data. Existing transport failures retain their status and
types. Other declared ENT operations remain unavailable.

## Verification and recovery

SQLite and native PostgreSQL regressions cover owner-only tables, tenant
isolation, unknown keys, explicit false, missing rows, exact limits, encoded
escaping and owner snapshots. SQLite additionally covers malformed storage and
stable storage failures. Authentication/version/envelope/shape tests cover
request denials; race tests cover concurrent reads; PostgreSQL executes the
repository query inside a read-only transaction. The owner snapshot format stays unchanged. Testing exposed an existing restore
bug: struct-based GORM inserts replaced `AccountFeature.Enabled=false` with its
`default:true`. A narrowly scoped ENT AccountFeature restore branch now writes
explicit owner columns inside the existing transaction, preserving both boolean
values, IDs, account scope, keys and timestamps. Other owner restore paths,
model defaults are unchanged. SQLite/PostgreSQL regressions
verify all restored feature rows across accounts, rollback on rebuild failure,
and PostgreSQL ID sequence continuation. After restore, the query still exposes
only the account's persisted map. Existing capability-check and REST behavior remain
separate compatibility surfaces. Strict ownership/dependency/contract/runtime
inventory gates are retained; focused checks do not imply full ENT acceptance.

## Existing owner write correction

A focused failing-before regression also demonstrated that
`EnterpriseRepository.SetAccountFeature(false)` returned success while persisting
true for both a new row and an existing true row. Its struct-based
`Assign`/`FirstOrCreate` dropped the false zero value and reapplied the model
creation default. The existing writer now uses an explicit-value atomic upsert
on `(account_id, feature_name)`, updating only `enabled` and `updated_at` while
preserving row identity and account/key scope. Concurrent creates resolve via
the existing unique key. This repairs persistence, without changing request or
response shapes, permissions, license policy, model defaults or adding a new
write route/independent mutation contract. REST permissions still use the
existing tenant settings-manage policy; this phase does not redefine it.

SQLite and PostgreSQL tests cover new false rows, repeated true/false
transitions, cross-account isolation, independent-handle concurrent creates and
updates, and rejected writes preserving the old value. A real-router regression
checks the existing allowed/denied roles and false responses through POST/GET.
The new list query itself remains strictly read-only.

## Explicit REST boolean validation

Repairing false persistence exposed a previously masked request ambiguity:
`SetFeatureRequest.Enabled` was a plain Go bool, so omitted or JSON-null values
became false. Before the persistence repair this often preserved/enabled the
flag; afterward it could disable an existing true flag without an explicit
boolean decision. The supplied product documents do not define omitted/null
semantics for this compatibility write route.

This phase therefore adopts a narrow explicit-boolean request contract for the
existing POST `/api/v1/accounts/{account_id}/features`: `enabled` must be a JSON
boolean, either `true` or `false`. Missing, null, strings, numbers, arrays and
objects return HTTP 400 before any storage writes. `feature_name` remains
required and existing authorization/response shapes remain unchanged. Clients
that omitted `enabled` or sent null must now provide their intended boolean;
there is no implicit enable, disable or preserve operation. Route and
SQLite/PostgreSQL handler tests cover missing/null/wrong types, unchanged
existing rows, no absent-row creation and explicit true/false success.
