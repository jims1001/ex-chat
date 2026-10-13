# Shared canned-response read (phase33)

`ops.canned_response.get.v1` reads existing OPS-owned `CannedResponse` rows.
They are shared within an account. A canonical positive decimal `User` actor ID
and `auth_source: "module"` are required, in addition to the authenticated,
allowlisted module caller and contract version. The caller must supply a trusted
actor. Every valid read calls TEN's `ten.permissions.check.v1` with the actor ID
and envelope account, using TEN's existing decimal-string user ID input to avoid
float64 rounding for large actors; missing, revoked or mismatched membership denies access,
and unavailable TEN fails closed. OPS does not query IAM/User/Account/AccountUser
storage. It never caches membership or grants access from claimed role fields.
This retains current membership semantics; it introduces no new identity rules.

Missing TEN configuration fails this read with `dependency_unavailable`; it does
not stop OPS's existing `cus.contact.merged.v1` consumer from registering.

## Request shapes

Common fields:

- `query_type`: `ops.canned_response.get.v1`
- Positive `account_id` and canonical positive actor ID, at most 2^63-1
- `consistency`: `strong`, ordinary required request timestamp
- `fields`: exactly `["id","account_id","short_code","content","created_at","updated_at"]`, in that order

`filter.mode` is mandatory and chooses exactly one shape:

| Mode | Allowed filter fields | Sort | Pagination |
| --- | --- | --- | --- |
| `detail` | `mode`, required `id` | `["id"]` | `page: "1"`, no cursor |
| `match` | `mode`, required string `query` | `["id"]` | `page: "1"`, no cursor |
| `list` | `mode`, optional string `search`, optional `limit` | `["-id"]` | First request `page: "1"`, no cursor; continuation no page and a cursor |

Detail ID and list limit must be canonical positive JSON integers. Strings,
fractions, exponent forms, zero, negatives and overflow fail validation. Numeric
transport decoding preserves integers beyond float64's exact range. Limit is
50 by default and may be 1–100. Cursor is a canonical positive decimal ID string,
at most 2^63-1. Query/search text must be valid UTF-8 and at most 4096 bytes.
The runtime's ordinary configured request-body limit still applies.
Unsupported keys, mixed modes, wildcard/projection/reordered fields, other sorts,
page numbers, or mixed page/cursor forms fail with a static validation error.

## Matching and results

The complete persisted DTO contains only `id`, `account_id`, `short_code`,
`content`, `created_at`, `updated_at`. It has no private-user ownership, version,
visibility, derived content or uniqueness claim. Text is preserved. Detail and
match return one record, or `resource_not_found` (404) if there is none.

List returns `data: {"items": [...], "next_cursor": ""}`. Search is not trimmed:
it lowercases the supplied string and uses substring SQL LIKE against lowercased
shortcode OR content, exactly like repository `List`. Empty search has no text
predicate. Ordering is descending ID; at most the requested limit is returned.
A lower matching row sets `next_cursor` to the last returned ID. Continuation adds
`id < cursor`. There is no total count or immutable traversal snapshot. Keep the
same account/search while traversing; concurrent changes or offline restore may
change subsequent results. An empty list succeeds with empty items/cursor.

Match trims and lowercases the query, then checks shortcode equality OR content
substring LIKE. The lowest matching ID wins, exactly as repository `First` does;
a later shortcode equality does not outrank an earlier content match. Blank or
whitespace-only match also preserves repository behavior: `LIKE '%%'` can match
the first row, after current TEN authorization; 404 means no matching row.
ORC's existing blank-input short-circuit remains unchanged. The runtime read does
not add that caller-specific short-circuit to the repository contract.

SQL `%` and `_` retain their wildcard meaning. Unicode lowercasing/collation
follows the existing Go-query/database-expression combination; no normalization
or cross-dialect Unicode identity equivalence is promised. Legacy duplicate
shortcodes remain separate rows, ordered by ID. These semantics do not establish
a product uniqueness policy.

## Bounds and failure behavior

Bounds are engineering limits on this read, not new business validation:
100 records plus one lookahead; 4096-byte request text; each returned text value
at most 1 MiB before SQL transfer; encoded data budget at most 1 MiB with a small
conservative framing allowance. The outer transport envelope adds ordinary
framing. PostgreSQL `octet_length` and SQLite `length(CAST(text AS BLOB))` guard
both text columns before transfer, including embedded NUL handling on SQLite.
Rows are scanned and JSON-encoded one at a time with an accumulated byte budget;
JSON escaping expansion counts. Oversize/invalid UTF-8 records fail the requested
page, without truncation, skipping, partial output or modifying source data.
A lookahead is existence-only and is returned only on a later request.
The query checks context cancellation before SQL and before successful return.

Storage errors, absent storage, oversized results and malformed text fail with
static `dependency_unavailable`; details/content are not placed in contract
errors. A single query reads only tenant-scoped OPS rows and writes no records,
receipts, journals or events. Independent TEN and OPS calls are not an atomic
cross-owner snapshot; the membership decision is fresh at request authorization.

## Narrow legacy REST repairs

Two independently reproduced failures were retained before the repair:

1. A stale record loaded before deletion was reinserted by GORM `Save` during
   update. The existing endpoint is documented as update and already rejects a
   missing lookup. Update now uses account/ID predicates and only updates
   shortcode/content/timestamp. A disappeared row reports not-found; REST returns
   404. There is no upsert, version/CAS policy or change to concurrent-update
   winner semantics.
2. Required-field binding ran before existing shortcode trimming, so space-only
   input created/updated an empty shortcode. Create/update now reject empty after
   that same trim with 400. Content bytes, shortcode case/Unicode, duplicate rows,
   tenant access and the existing trimming policy are preserved.

No new management permission, shortcode uniqueness index, duplicate cleanup,
content normalization, PATCH route or broad CRUD redesign is introduced.

## Recovery and remaining scope

Ordinary owner snapshots preserve canned rows and unrelated OPS models. The read
adds no snapshot schema, mutation journal, version or adoption state. Snapshot
creation/restoration still needs quiesced writes. Tests compare persisted source
values (PostgreSQL's native timestamp precision included) across a round-trip.

Phase64 adds the separately specified [shared maintenance contract](ops-canned-response-management.md)
and `ops.canned_response.changed.v1` owner outbox. Existing modes above keep their
six-field contract. An explicit `detail_versioned` mode requests those same fields
plus `object_version` as a seventh field and otherwise uses the detail shape and
bounds. It reads text and version together in one SQL projection. Full OPS,
variable rendering, preview/sending, and AIC/ORC runtime wiring remain out of scope.
No provider or external account is exercised.

Focused verification covers module/actor authorization, current TEN membership
and failures, shared tenant reads, strict shapes/precise IDs, list/match parity,
duplicates/Unicode/wildcards/blank text, bounded pagination/response sizes,
cancellation, no writes/foreign tables, snapshot recovery, and native PostgreSQL
read-only execution. REST regressions include deterministic deletion between
lookup and update, no blank-input writes, foreign/missing rows and legacy values.
