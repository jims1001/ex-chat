# Bot report correction (phase42)

This bounded repair changes the existing `bot_metrics` and `bot_summary` reads.
It adds no report job protocol, async report engine, CSV policy, provisioning,
route, permission or public response field. Other report metrics are unchanged.

## Counts and ownership

- `conversation_count` uses conversations created in the requested range in
  CHN-owned, same-account inboxes with an existing same-account EXT AgentBot.
  `AgentBot` has no active/status field; none is inferred. Empty/invalid bindings
  select no conversations, never all conversations.
- EXT validates bindings by joining only its `agent_bot_inboxes` and `agent_bots`
  tables. RPT intersects the returned IDs with CHN's account-owned inbox IDs.
  CON and MSG are still accessed through their existing owner ports.
- `message_count` counts outgoing messages in that creation cohort and the same
  message time range. A nonnil empty ID list remains MSG's select-none input.
- Outcomes use the existing account/event-time scope, independent of conversation
  creation time, current bindings and current conversation existence. Distinct
  positive direct `conversation_id` values identify outcomes. An account-scoped
  historical event is not reauthorized by current CON/CHN records.
- A handoff excludes the same conversation from resolutions within each requested
  event window, irrespective of event order. Event `value`, current status and
  current assignee do not fabricate bot activity.
- Summary counts come directly from integer event sets. They are never reversed
  from one-decimal rates. Missing previous-period dependencies fail the request
  rather than silently replacing previous values with zeros.

## Compatibility and unresolved semantic boundaries

Doc20 sections 4 and 10 document distinct outcomes and handoff precedence; the
creation cohort is explicit for the denominator and message count. This repair
preserves the current account/event-time numerator scope rather than imposing an
unconfirmed shared creation cohort. Consequently rates can exceed 100%, and a
zero denominator produces zero rates while `bot_summary` can have nonzero event
counts. Consumers must not treat these figures as a single immutable cohort.

Historical phase42 behavior: the time helpers and owner ports included both
`since` and `until`. That differed from doc20's half-open description and was
preserved by phase42, so an endpoint event could appear in both periods. The
frozen phase42 evidence continues to describe that historical implementation.

Phase43 supersedes only that time-boundary behavior: the existing ReportHandler/ReportService workflows now select
`[since, until)` through their RPT, CON, MSG and CUS reads. Adjacent periods no longer share
an endpoint, and equal exact instants select no records. Date-only `until`
continues including the complete named UTC date by normalizing to the following
midnight, so equal date-only inputs still select one day. Unix/RFC3339 inputs
retain their parsed instants. PostgreSQL comparisons preserve those instants on
its stored microsecond grid through report-only parameter ceiling. SQLite keeps
its existing timestamp-text representation behavior: canonical UTC storage is
covered for the UTC HTTP parameters, and matching stored/caller offsets remain
compatible; arbitrary mixed-offset storage comparison remains unresolved.
No data migration, SQLite parameter rounding or timezone-storage policy is added. With both times present, the previous window shifts back
by their exact difference; with only `since`, it covers the preceding seven days;
without `since`, previous remains zero. `BusinessHours` is preserved across periods and does not
change these count metrics. The current bot endpoints consume time/business-hours
fields only; this repair adds no type/id/timezone filtering semantics.

## Unidentifiable stored bot events

The documented event relationship is the direct internal conversation ID.
Phase31's direct-or-metadata association is specific to its stored-event query
contract and is not an aggregation rule. This repair does not parse metadata,
choose between metadata keys, migrate records or synthesize associations.

A selected bot event with a missing, zero, negative or unrepresentable direct ID
fails the entire report with the static service error
`bot reporting events are unavailable`. Database/query failures are also returned
under that static text while preserving the underlying error for internal error
classification. The event query does not log raw SQL/scan errors. The HTTP layer
keeps its existing 500 error envelope and endpoint-specific generic message.
No partial count is returned and stored records remain unchanged. This means
legacy custom events that supplied an ID only in metadata now cause a visible
unavailable report instead of a fabricated count. Unselected names, accounts and
time windows are unaffected. PostgreSQL bigint cannot store fractional/text values; its native tests use
rejected textual bigint input (numeric driver parameters may be coerced before
storage). SQLite malformed text/fractional values are rejected when the report
reads them.

## Execution limits

RPT obtains both outcome kinds in one DISTINCT event query per period. It loads
one row per distinct conversation/name pair and uses in-memory sets, so memory
and transfer cost grow with the selected distinct event population. There is no
silent cap, truncation or scalable aggregate/report-job claim. Conversation and
message owner reads retain their existing loading behavior. Current/previous
queries and separate owner reads do not form a cross-owner atomic snapshot;
concurrent changes may alter results between reads.

Focused SQLite and PostgreSQL tests share owner-isolated cases for duplicate
facts, handoff precedence, exact 1-of-3 counts, invalid bindings, account/time
boundaries, missing associations, error propagation, no fabricated state-derived
outcomes, concurrent reads and owner table isolation. HTTP regression checks
cover V1/V2 response shapes, administrator/report permission access, ordinary
agent/foreign membership denial and static failure envelopes.
