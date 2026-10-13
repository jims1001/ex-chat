# Stored ReportingEvent query (phase31)

`rpt.report.get.v1` reads existing RPT-owned `ReportingEvent` records. The
transport authenticates an allowlisted module caller; `account_id` in the query
envelope is the tenant boundary. Actor type, ID and auth-source claims do not
expand it. Upstream callers remain responsible for their existing authorization.
The query makes no IAM, TEN, CON, provider or other network calls.

This bounded slice does not generate reports, compute metrics, run exports,
provision data, apply defaults, validate conversation existence or write records.
Existing REST endpoints and repository methods are unchanged. Full independent
RPT acceptance remains pending (`independentRuntimeImplemented: false`).

## Request

- `query_type`: `rpt.report.get.v1`
- Positive envelope `account_id`, ordinary required actor/request timestamp
- `filter`: an object containing only optional `name` and `conversation_id`
- `name`: exact UTF-8 string, including empty/whitespace/case-sensitive values;
  at most 1,020 UTF-8 bytes. Absence means any name
- `conversation_id`: positive canonical JSON integer, at most 2^63-1. Strings,
  fractions, exponent forms, zero, negatives and overflow are rejected. This
  query opts into precision-preserving transport decoding
- `sort`: exactly `["-id"]`; `consistency`: exactly `"strong"`
- First request: `page: "1"`, no cursor. Continuation: no page and `cursor` equal
  to a canonical positive base-10 ID string (at most 2^63-1)
- `fields`: exactly this ordered complete DTO list, without projections:
  `["id","account_id","conversation_id","user_id","inbox_id","name","value","event_start_time","event_end_time","metadata_json","created_at"]`

Unknown filters, extra/reordered/duplicate fields, wildcard fields, alternate
sorts, page numbers and mixed page/cursor requests fail with a static validation
error. The request body remains subject to the runtime's configured body limit.

## Response and continuation

The ordinary contract envelope's `data` is:

```json
{"events": [], "has_more": false, "next_cursor": ""}
```

Each event contains all eleven requested fields. Nullable relationship IDs are
explicit nulls; explicit stored zero relationships remain zero. Metadata remains
an opaque string byte-for-byte, including empty text and malformed JSON; it is
never decoded into the response, synthesized, truncated or redacted. Values
(including zero) and finite serializable times are preserved; time output uses
UTC. Value follows the existing float64 model/DTO semantics, including normal
float64 rounding; arbitrary PostgreSQL decimal precision is not promised.
Nonzero decimals that underflow to zero, overflow, NaN and infinities fail.
No total, generated-report status, version or relationship existence is
claimed.

One parameterized SELECT scopes by account, optional exact name and `id < cursor`,
orders descending by ID and transfers at most 101 bounded candidate rows. Up to
100 candidates are examined and at most 25 matches are returned. The remaining
candidate is lookahead only. If another candidate exists, `has_more` is true and
`next_cursor` is the last examined ID, which can be lower than the last returned
ID. An empty page can therefore have a continuation. Continue until `has_more`
is false; continuation does not promise another matching event.

A page is one database statement snapshot. Cursors are stateless lower-ID
boundaries, not an immutable snapshot or signed authorization token. Keep the
same account and filters while traversing. Ordinary restart needs no cursor
state; concurrent inserts, updates and deletes or offline restore can change
later results. Cross-restore traversal continuity is not promised.

## Exact conversation association

A row matches when its direct conversation ID equals the requested ID OR its
metadata is a complete valid JSON object with one unambiguous top-level
`conversation_id` equal to that canonical positive integer. Whitespace and
escaped key spellings are accepted. Duplicate top-level keys, nested-only keys,
arrays, strings, null/boolean values, prefixes, negative/overflow IDs,
fractional/exponent forms and invalid/trailing JSON do not match metadata.
A direct match still succeeds for malformed metadata or a conflicting metadata
ID. A metadata match still succeeds when the direct ID names another conversation.
No synthesized relationship ID is returned.

This deliberately replaces accidental substring matches only for the new
contract. It uses neither float64 comparison, SQL LIKE nor PostgreSQL JSON casts.

## Engineering bounds and corruption

The page/scan budgets are fixed engineering limits, not business data limits:
25 results, 100 examined candidates plus one lookahead; name 1,020 UTF-8 bytes;
metadata 16 KiB; PostgreSQL decimal text 512 bytes and SQLite timestamp text 64 bytes; encoded `data` object 1 MiB (inclusive). The common outer contract
envelope adds its normal small framing. The repository bounds text and malformed
SQLite numeric storage before transfer, checks raw storage types, UTF-8, finite
values and serializable times, and checks JSON escaping expansion before sending
success. SQL uses byte length even when SQLite text contains embedded NULs. PostgreSQL
uses native typed timestamps and UTC output, so historical timezone offsets and
session DateStyle do not change representability. Finite years 0000–9999 are
accepted when the database can represent them; infinity and unencodable years fail.

A malformed or oversized examined row fails the whole page with the static
`dependency_unavailable` message `stored reporting events are unavailable`.
It is neither skipped nor partially returned. Malformed metadata JSON alone is
valid opaque data. Rows outside the SQL account/name/cursor scope do not affect
the result. Logs and errors do not expose query data, metadata or SQL details. Missing storage
(receiver/database/config/dialect) and missing/canceled contexts fail statically.
Both page and complete snapshot reads recheck cancellation before successful return.

## Owner snapshot preservation

A failing-before fixture demonstrated two existing generic restore defects:
GORM struct Create changed stored `Value=0` to its model default 1 and changed a
zero `CreatedAt` to the restore-time clock value. The RPT-only restore hook now
uses explicit columns within the existing owner restore transaction. It preserves
IDs, nullable relationships, opaque metadata and all three times, including zero.
No SLA restore behavior or general snapshot format is changed.

The existing snapshot format remains compatible. All eight nonnullable fields
must be present and nonnull; duplicate, unknown or case-alias fields are rejected.
Legacy omitted nullable IDs and explicit null mean nil; explicit representable
unsigned IDs, including zero, are preserved. Missing Value never becomes a new
zero. Invalid source scalars or a later restore failure roll the transaction back.
Existing PostgreSQL sequence restoration remains in use.

Additional failing-before fixtures showed that generic GORM export normalized
SQL NULL to zero/empty for Value, metadata and all three timestamps, and that a
nonzero JSON value such as 1e-400 could silently restore as zero. The RPT snapshot
export now reads and validates raw scalar types before encoding; restore rejects
nonzero underflow. This is complete private backup processing, not the bounded
query: it has no 25/100 row, 16-KiB metadata, 1,020-byte name or 512-byte decimal
limit. Arbitrary valid opaque metadata remains preserved. Corrupt source types,
NULL nonnullable scalars, invalid UTF-8 and nonrepresentable values fail rather
than producing a normalized checkpoint; a prior snapshot file stays intact.

SQLite nanoseconds are preserved for SQLite restore. PostgreSQL timestamps have
microsecond precision, so a snapshot with any finer timestamp is rejected
transactionally instead of silently rounded. This explicit representability
restriction applies only when restoring such a snapshot into PostgreSQL. Owner
writes still must be quiesced for complete snapshot creation/restore.

## Verification

Focused SQLite and native PostgreSQL tests cover transport/shape/tenant authority,
large exact IDs, direct/metadata associations, sparse pagination and stateless
restart, byte and encoded-size boundaries, malformed storage, read-only execution,
query cancellation, concurrent reads, static failure/log output, snapshot zero
preservation, source-field validation, rollback and PostgreSQL sequence advancement.
The stage also runs focused race/vet and architecture ownership/contract guards.
Combined full gates are run after integrating this bounded slice with phase30.
