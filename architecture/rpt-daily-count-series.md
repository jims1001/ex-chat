# Bounded account fixed-offset count series (phase53)

The existing GET roots `/api/v1/accounts/:account_id/reports` and
`/api/v2/accounts/:account_id/reports` expose three account record counts with
hour/day/month/year calendar grouping. Phase53 extends phase47 with exact
fixed numeric offsets; it does not add routes or a generic report engine. Product documents
08/12/20 define the route, message types and metric meaning; foundation 00/04
govern authority and errors; product 02 permits a raw array.

## Request and result

- Require recognized `metric` and both explicit valid `since`/`until`. Unix whole
  seconds, RFC3339 fractional/offset timestamps, and YYYY-MM-DD use phase43's
  unchanged shared time parser and exact half-open selection. Date-only until
  advances to next UTC midnight. `ReportFilter` is unchanged.
- `from`/`to` apply only when the corresponding raw primary value is empty.
  A malformed or whitespace primary never falls through to an alias or an
  unbounded read. Duplicate and unknown query fields, invalid encoding, missing
  bounds, unknown metric/type, malformed offset/boolean/format return 400.
- Omitted/empty type, group_by and timezone_offset select account/day/0.
  `group_by=hour|day|month|year` selects the corresponding fixed-offset calendar unit;
  unknown group_by values retain doc20's fallback to day. Recognized week,
  other valid dimensions or documented non-count metrics,
  account `id`, IANA `timezone` and CSV return 422. An invalid ID is a 400 format
  error. JSON is the sole response format. No implicit recent-week range exists.
- `timezone_offset` is a fixed numeric number of hours: positive means local
  civil time = UTC + offset. Decimal notation (optional sign, fractional part,
  or scientific exponent) is interpreted exactly and must produce whole seconds
  between -9,223,372,036 and +9,223,372,036. This is a time.Duration arithmetic
  representation bound, not a geographic timezone limit. For example +5.5,
  +5.75, -3.5, 25 and .0025 hours are supported. No DST, IANA, account timezone,
  locale, minute-only precision, or ±24-hour policy is inferred.
- Unsupported nonintegral-second or unsafe-magnitude values return 422
  `unsupported_timezone_offset`, without rounding, truncation or float64
  acceptance. Invalid decimal syntax, nonfinite values, and rejected values
  beyond the prior finite-float range return 400 `invalid_timezone_offset`.
  Ordinary signed/scientific/zero-padded zero remains zero. Hexadecimal literals,
  Go underscore separators and rational slash notation are not decimal input and
  return 400; the former Go-only zero spellings had been incidental ParseFloat
  acceptance. Long zero padding is normalized linearly before bounded integer
  work; arbitrarily large exponents never create large rational allocations.
- `since`/`until`, including date-only UTC interpretation, remain unchanged.
  Offset changes natural calendar boundaries and labels, never the selected
  absolute interval or the records in it. Checked shifts, integer Unix addition,
  epoch chronology and inverse conversion reject unrepresentable timestamps with
  422 `report_query_bound_unsupported`, including unsafe empty/reversed inputs.
- `conversations_count` counts CON.created_at. `incoming_messages_count` and
  `outgoing_messages_count` count MSG.created_at, including new messages on old
  conversations. Outgoing includes private notes, bots, queued and failed stored
  outgoing records; activity/template are excluded by exact message type. This
  is record volume, never evidence of sending or delivery.
- `business_hours` is accepted but does not change these three counts. Legacy
  summary/trends and other report workflows retain their existing semantics.
- Success is a sparse raw array of `{value: integer, timestamp: integer}`,
  ascending and without zero buckets. Timestamp is the absolute UTC Unix second
  of the natural local bucket start: HH:00 for hour, midnight for day, day 1 midnight for month, and
  January 1 midnight for year, in the fixed-offset calendar. Labels are not moved to the requested start.
  Each bucket selects its exact clipped `[since, until)` intersection. Calendar
  increments preserve leap days, different month lengths and year transitions;
  an exclusive endpoint at a natural boundary adds no following bucket.
- Empty/reversed valid intervals return `[]` without owner SQL; nonempty
  intervals with no matching records also return `[]`. This route makes no
  projection freshness claim: each owner read uses one statement snapshot.

## Read and storage limits

At most 400 selected intersecting fixed-offset buckets of the requested grouping are allowed,
including partial buckets and buckets that will contain no records. An interval
intersecting bucket 401 returns 422 `report_range_too_large` before either owner
is called. The limit is selected buckets, not elapsed days or result rows: 400
years may be valid while just over 400 selected hours is not. The request context
gets a five-second deadline, preserving an earlier deadline/cancellation. Auth,
tenant and permission read clones retain each original owner handle, context and
existing authority function; only their query logging is silenced. The owner
receives context and checks it before, during and after reading. Canceled/error
reads discard all rows.

Each CON or MSG call uses one parameterized VALUES-bounds CTE and grouped COUNT,
with two bound times per bucket, one account, and at most one exact message type:
801/802 parameters at 400 buckets and at most 400 output points. Only the selected
owner's table is joined. No bodies, cross-owner table joins, writes, migrations or
indexes are introduced. This bounds buckets, parameters, returned rows and
request duration; it does not claim bounded rows scanned, bounded calendar
duration, constant database work, or a new index plan.

PostgreSQL uses phase43 ReportTimeBoundary on query parameters only, preserving
exact membership against its stored microsecond grid. HTTP instants and interval
lengths are never rounded. Session timezone does not affect the bound buckets.
The SQLite representability restriction below does not narrow PostgreSQL's
existing broader query-bound support.

SQLite uses its existing lexical timestamp comparisons, without date functions.
For this count capability, supported stored data must be canonical driver UTC
text `YYYY-MM-DD HH:MM:SS[.1..9 fractional digits]+00:00` with years 0001 through
9999, valid calendar fields and driver-trimmed fractional digits (no trailing
zero). The actual clipped query bounds must also be representable as canonical
zero-offset UTC text in that year range. Unsupported query bounds return 422
`report_query_bound_unsupported`, with field `since/until`, before SQL; they are
validation failures rather than storage failures. Natural bucket labels or
nominal successor boundaries outside that range do not themselves reject an
otherwise supported clipped interval. For example, a year-9999 bucket ending
within year 9999 remains valid even though its nominal next year is 10000.
An actual bound in year 10000, including a date-only until expanded there, is
unsupported. Bounds are neither clamped nor silently normalized by the owner.

A single-statement static guard checks all stored rows for the selected account
(and exact incoming/outgoing type for MSG). Any unsupported stored
representation, including a row outside the requested interval, fails the entire
read with 503 `report_storage_representation_unsupported`, retryable false. This
conservative rule avoids pretending to know an unsupported row's absolute
instant. Other accounts and message types cannot block the read. Stored values
are neither returned, parsed into dates, normalized nor migrated. Legacy phase43
reads retain their existing representation behavior; PostgreSQL is independent.

## Authority, errors and compatibility

Both roots reuse the exact existing Auth → Tenant → PermissionReportView chain,
including administrator and report_view/report_manage role behavior. Root-only
wrappers clone existing repository handles and preserve owner stores; they do
not widen grants or change legacy authority decisions.

Only the two registered GET roots buffer responses (maximum 64 KiB, far above
400 count points) to replace early aborts, panic recovery and late cancellation.
A recovery inside legacy RecoveryLogger prevents raw panic values/stack contents
from reaching its logs. Request query values and raw SQL/driver errors are not
logged for these roots. Neighboring/legacy paths use their original responses.

Errors are `{error:{code,category,message,field_errors,request_id,correlation_id,
retryable,...}}`. Field errors contain field/code/message. Categories follow
foundation04. The request/correlation IDs are those of UnifiedContext; a numeric
Retry-After header is also exposed when applicable. Public dependency categories
are used without addresses, credentials, stored values or SQL. Timeout is 504
`report_timeout` (retryable); cancellation is 503 `request_cancelled` (not
retryable); count storage errors are 503 `report_unavailable` (retryable).
Unsupported SQLite query bounds are nonretryable 422 validation errors;
unsupported stored representations remain nonretryable 503 storage failures.

Existing root auth/tenant/permission denials keep 400/401/403/404 status meanings
and receive the structured body. Legacy summary/trends/BOT, Captain/AIC, AUT,
SAML, async jobs, remote runtime engines and all other route contracts are outside
this change. No login, push, deployment or external sending is involved.

## Local capability registration and rollback

RPT count ports are explicitly registered in dependency_sources and
collaboration_contracts, with AST guards for exact count-only signatures, owner
methods, call sites, constructors and router wiring. `domain.ReportCountGrouping`
is a string enum with exactly `ReportCountHour`, `ReportCountDay`,
`ReportCountMonth` and `ReportCountYear`. The handler `GetCountSeries` passes
context, account, metric, grouping, the unchanged ReportFilter and exact offset
seconds to service `GetCountSeriesWithOffset`. The old `GetCountSeries` method
remains a zero-offset wrapper. CON exposes
`CountReportBuckets(ctx, accountID, grouping, buckets)`; MSG adds its exact
messageType before grouping. The shared owner helper `countReportBuckets` also
receives grouping before buckets and uses only its registered fixed owner table.
Every `ReportCountBucket` carries the same `OffsetSeconds`; owners reject mixed
offsets, unsafe arithmetic, or labels not aligned to that local calendar. The
shared domain calendar helper computes absolute UTC boundaries; clipped SQL
parameters remain zero-offset UTC. No SQL timezone function or schema change is
introduced.
`ReportCountMaxBuckets=400` and the owner's matching bound are guarded.

These are local synchronous count capabilities, not remote query aliases or a
promise of remote count support. Existing RPT→CON/MSG dependency directions
remain. The split ReportHandler file retains its established RPT ownership and
explicit scan scope. Adding grouping does not expand the shared ReportFilter,
load owner objects into RPT, or add foreign storage access.

Roll back phase53 as one unit to restore UTC-only hour/day/month/year grouping,
including handler/parser, service, domain bucket/helper, owner validation and
matching guards/docs. No owner port signature has changed. Keep phase43 time parsing
and the PostgreSQL microsecond fix, phase44 roots and hardening, and phase45/46
reporting changes intact. No database or stored-data rollback is needed.

## Local verification scope

Regression coverage includes exact decimal conversion and overflow, fractional
hour offsets, negative and second-resolution offsets, offsets beyond 24 hours,
local leap/month/year boundaries, actual nanosecond half-open clipping, invariant
record totals, sparse output, 400/401 budgets, mixed-offset rejection before SQL,
owner-only projections, both route versions and the existing role chain. Native
PostgreSQL covers microsecond membership and actual UTC year 10000; SQLite keeps
its existing actual clipped-bound/storage representation restrictions. A nominal
local label may cross years 0/10000 while valid clipped UTC bounds remain usable.
The accompanying evidence distinguishes focused, race, core and native runs;
this document does not itself claim a completed gate, deployment or load test.
