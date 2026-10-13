# AIC persisted account usage (phase30)

This slice implements only the already-declared `aic.usage.get.v1` query through
existing authenticated module transport. The result is the nine persisted
`AIUsageQuota` fields, scoped by the envelope account. The query adds no route,
schema, dependency, provisioning, default, reset, counter update, event, receipt,
journal, outbox or provider call. The separate AIC quota recovery correction
below preserves stored values during the existing owner restore workflow. `independentRuntimeImplemented` remains false
and acceptance remains pending. The separately implemented
[bound assistant summary read](aic-bound-assistant-read.md) shares this owner;
other AIC operations remain unavailable.

## Meaning and authority

These are stored application counters and limits. Some existing writers estimate
usage. The result is not an exact provider bill, billing ledger, effective
entitlement, license decision, remaining quota or availability decision. Zero and
negative limits/counters and past reset times are returned as stored. No missing
row is created: absence is HTTP 404. The REST `GET /captain/quota` provisioning
path and its policy remain separate and unchanged.

The configured bearer token and allowlisted `X-Module-Caller` authenticate a
trusted module, which asserts the account scope. Actor fields are context, not
new end-user authentication or global authority. User, System, SuperAdmin and
other accepted actor claims get the same account-scoped read. The upstream
gateway must authenticate the current end user's tenant access before invoking
this internal query. No foreign owner table is read to reauthenticate a user,
check tenant existence, or derive an entitlement.

`architecture/orchestration_processes.json` also names `aic.usage.get.v1` as the
`resultQuery` for response generation. This persisted-account slice does not
implement that generation-result contract. Operation, command, status and all
other filters/variants are rejected. Generation completion/status polling,
commands and emitted generation events remain incomplete. A caller must not use
this DTO as evidence that a requested generation has completed.

## Fixed request shape

POST `/contracts/queries/aic.usage.get.v1` with the existing bearer, caller,
accepted version and transport timeout/body-size rules:

```json
{
  "query_type": "aic.usage.get.v1",
  "account_id": 91,
  "actor": {"type": "User", "id": "7", "auth_source": "module"},
  "filter": {},
  "sort": ["account_id"],
  "page": "1",
  "fields": ["id", "account_id", "monthly_token_limit", "used_tokens", "monthly_request_limit", "used_requests", "reset_at", "created_at", "updated_at"],
  "consistency": "strong",
  "requested_at": "2026-10-07T00:00:00Z"
}
```

Cursor must be absent or empty. Filter must be an empty object. Exact field
order is required. Alternate projection, sort, page, consistency, account
filters, operation/status variants and additional envelope fields are rejected.
The positive account ID must fit the signed 64-bit database range. The caller
must decode JSON integer fields without floating-point precision loss; stored
signed 64-bit extrema are preserved as exact JSON numbers.

## Complete bounded read

One direct SQL statement selects only the nine columns with an account predicate
and a two-row cap. A second matching row fails closed; no partial row/list is
returned. Strong means a direct statement snapshot, without cache or split-page
reads. Cancellation is passed to the database and checked before returning.

SQL guards bound each numeric field to its stored integer representation and
each timestamp to at most 64 bytes before transfer. SQLite numeric storage must
be integer, not NULL/text/blob/real. Existing column affinity can normalize a
write before storage; this read does not reconstruct the original input. Native
PostgreSQL integers must scan as int64. IDs must be positive; all four stored
limit/counter fields preserve the full signed 64-bit range, including zero and
negative values, with no business-policy interpretation.

SQLite timestamps are selected as text with their original text storage class
checked. The runtime's glebarez driver returns a scan error for a malformed
datetime; the existing CGO SQLite driver can instead silently return a zero
time. Fixtures verify both behaviors. Text projection avoids either driver's
datetime coercion and validates the actual stored string. Accepted
forms are ISO dates, ISO datetime with a space or T, up to nine fractional second
digits, and an optional Z, numeric hour offset or hour:minute offset. PostgreSQL's
ISO timestamp text is accepted. Native connections must retain ISO DateStyle
and ISO numeric timezone offsets (the verified fixture uses UTC); non-ISO
DateStyle or unsupported historical offsets with seconds fail closed with 503.
The read does not change connection settings. Values must be valid calendar/time values with
years 0001–9999 both before and after UTC conversion. They are returned as UTC
RFC3339 timestamps without dropping supported precision. NULL, binary/numeric
time storage, infinity, BC, oversized strings, invalid dates/offsets and
unrepresentable dates are rejected. A real stored year-0001 zero timestamp is
preserved; corruption is never substituted with one.

The complete JSON-encoded `data` DTO is capped at 4,096 bytes, excluding transport
metadata. The fixed valid numeric/time schema fits within this cap; the explicit
check fails closed if a future representation exceeds it. Encoding must succeed
before any result is returned. SQL errors, invalid rows, duplicates and malformed
or oversized values return static HTTP 503 `dependency_unavailable`:
`persisted AI usage is unavailable`. Absent rows return static HTTP 404
`resource_not_found`: `persisted AI usage was not found`. Unsupported request
shape is static HTTP 400 `validation_failed`:
`persisted AI usage query shape is unsupported`. Existing transport auth,
version, envelope and timeout failures retain their existing status and type.
No failure returns SQL details, stored values or partial data.

## Verification

Focused SQLite and native PostgreSQL tests cover exact fields, account
separation across actor claims, missing rows without provisioning, preserved
zeros/nonpositive values and numeric extrema, malformed timestamps and storage,
duplicate rows, cancellation, concurrent reads and owner-only stores. Read-only
SQLite/PostgreSQL transactions prove the query succeeds without write access.
Full owner snapshot comparisons verify that reads change no quota,
configuration, journal, receipt or outbox state. Authentication, envelope,
version, unknown-field, unsupported-variant and request bounds are checked.
Required core/native gates use the repository's reviewed exact native manifest;
these local fixtures do not establish real-provider or full AIC acceptance.

## AIC quota snapshot preservation correction

An isolated failing-before regression exposed an existing owner restore defect:
GORM struct inserts replaced explicit stored zero monthly token/request limits
with the model's creation defaults of 100000/1000. Those are different persisted
values, and ordinary Copilot interprets nonpositive limits differently. This
phase therefore corrects only the AIC quota snapshot path. Ordinary quota
writers, REST provisioning, model defaults and limit policy remain unchanged.

Quota recovery requires the complete nine-field row representation. Explicit
zero is preserved; a missing or JSON-null field cannot silently become an
unbounded zero limit. Incomplete, extra, duplicate or malformed fields and
invalid identities, numeric values or timestamps are rejected. Duplicate quota
identities/accounts are rejected. Snapshot timestamps must retain the existing
RFC3339 JSON representation. Native PostgreSQL restores reject sub-microsecond
values instead of silently rounding them; SQLite retains supported nanoseconds.
A malformed source quota must also fail
snapshot creation, rather than allowing a normal model scan to hide a stored
NULL as zero. Source validation includes the full nine-column projection even
for an empty source table, then checks each account through the bounded raw
read under the existing quiesced-write requirement. Valid quota snapshots retain
the existing array representation.

Restore inserts explicit column values within the existing owner restore
transaction. Invalid input or a rebuild failure rolls back owner state. Native
PostgreSQL sequence continuation remains part of the existing transactional
sequence recovery. Snapshot creation and recovery still require quiesced owner
writes and do not establish a coherent cross-owner cut. The persisted usage
query remains read-only before and after recovery and still exposes only the
requested account's stored quota.
