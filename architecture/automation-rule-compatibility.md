# Saved-rule compatibility inventory (phase16)

Phase17 adds only explicitly adopted numeric custom comparisons; see
[the profile contract](automation-numeric-conditions.md). Markerless rules and
old frozen executions retain the behavior described below.

This is a read-only report for the existing durable-local target. Legacy remains
the default. It does not enable, edit, freeze, activate, migrate, evaluate, or run
any rule. It cannot claim a mode, create execution/event/receipt rows, call owner
services, or contact providers. No real saved-user database was supplied: the
acceptance evidence is synthetic. Actual deployment audit was not performed.

## Operator usage and scope

Use the normal authenticated account API:

`GET /api/v1/accounts/{account_id}/automation_rules/compatibility?target=durable_local_v1`

The route requires `automation_manage`, including for inactive definitions. The
same tenant middleware and account membership checks apply as other account
routes. It works in legacy mode without starting a durable bridge. Responses use
the ordinary success/data envelope and `Cache-Control: no-store`; the request
logger omits this endpoint's entire query string, including rejected parameters.

Optional filters are `rule_id` (positive decimal ID), `active=all|true|false`
(default `all`), `limit=1..100` (default 100), and the returned opaque `cursor`.
Unknown, empty, duplicate, malformed, and mismatched cursor parameters are errors.
Every cursor binds schema, target, implementation revision, tenant, filters,
maximum rule-ID horizon, and last inspected ID. A cursor is a read position,
never authorization. Rule and account IDs and versions are decimal strings.

Each page is one database read transaction. PostgreSQL uses read-only repeatable
read; SQLite uses its read snapshot. Only AUT rule/version tables are queried.
Current raw definitions and latest immutable versions are observed consistently
within that page. The latest version is the one new durable admissions select;
this report does not describe already-admitted executions using older versions.
Missing versions use a pure preview of the exact existing freeze normalization.
The real freeze and report preview share the helper; preview creates no row,
version, activation timestamp, or execution.

The multi-page collection remains live. A high-ID horizon excludes later higher
IDs, but edits, toggles, deletions and version creation can change later pages.
`consistency=page_snapshot_live_collection`, `collection_snapshot=false`, and the
caveat describe that limitation. `page_exhausted` means no further matching rows
were visible within the current page snapshot/horizon. `complete` means all
returned sources were fully inspected within report bounds. Neither means all
account definitions have been reviewed or that cutover is approved. Remaining
row counts are page-snapshot counts, not an atomic collection inventory.

Before a separately authorized cutover, an operator must repeat review against
a quiesced source or an explicitly supplied immutable export and resolve actual
findings and independent readiness gates. This patch adds no database-opening
CLI, export/import tool, automatic activation, or mode transition.

## Redacted findings

Schema: `aut.rule_compatibility.v1`; implementation revision:
`phase16.inventory.v1`; target: `durable_local_v1`. The target reports current
new-admission action policy `continue_definite.v1`, condition behavior
`legacy_comparison_v1` (a report description of current semantics), and phase15
activity policy `touch_valid.v1`. No semantic policy is changed by this report.

Each rule contains `saved` and either `effective_frozen` or `activation_preview`.
The actual current parser/compiler is the executable truth. Ordinary alias,
whitespace and scalar/array status normalization does not alone indicate drift.
Lost metadata and actual effective changes remain separately visible, including
`custom_attribute_type` dropped by today's freeze DTO. Preview-only changes use
`activation_preview_definition_change`; differences from a real immutable
version use `saved_frozen_definition_drift`.

Issues use bounded known-schema paths and numeric indexes, stable enum codes,
category, severity, runtime effect, and the compiler's allowlisted reason code
where applicable. Details contain only counts, limits, units, known operators,
events, owners, or other allowlisted identities. No rule title/description,
custom key, operand, target ID, label title, action body, arbitrary nested key,
endpoint, credential, raw error, raw definition, or definition hash is returned.
Unknown code-like inputs are `unknown`. Rule IDs, account IDs, version and update
time are intentional operator references. Repeated fixed input gives identical
core output; page observation timestamps and cursors are outside that guarantee.

Rules sort by numeric ID, sources saved then effective, issues by schema path
with numeric index order then code. Severity counts count returned issues across
both inspected sources; they may count the same finding twice. Truncated source
counts are lower bounds; omitted issue counts are stated. Inactive findings keep
their severity and are counted separately, never silently activated.

## What the findings mean

- Malformed whole action definitions and more than 100 logical actions fail
  admission if conditions match. Invalid/unsupported individual logical actions
  produce a failing local validation step; later independent actions may still
  run. `failed_logical_indexes` is obtained from the continuing compiler
- `attempted_expanded_steps` sums independent candidate compiled steps, counting
  one validation step for an invalid logical action. `compiled_steps` is the
  actual bounded continuing plan. Five 100-label actions followed by a priority
  action attempt 1001 steps but compile to 802: the fifth label action is replaced
  by one failed validation step to reserve room for the priority action
- Unknown comparison operators are false leaves, never proof that the entire
  rule cannot match. An OR branch can still match. This includes greater/less,
  days_before, attribute_changed, and changed. No operator is implemented here
- Opened/resolved are supported durable events with a newly effective trigger
  review notice. They are not marked unsupported
- Typed custom scope is currently ignored; arbitrary keys read literal CON
  custom-map keys. Missing keys have the preserved false-leaf behavior, including
  is_not_present. Catalog ownership/type/existence remains unverified
- Connector defaults, previous-condition OR, left folding, ignored final
  connectors, nil message context, empty operands, stringification and numeric
  rounding risk are preserved and reported rather than normalized into new logic
- Compiler parameter precedence, ignored extras, status defaults, forced aliases,
  snooze fallback, normalized labels, byte limits and platform-dependent ID ranges
  are reported separately from actual compiler failure
- Local message creation means queued/saved local state, never external delivery.
  Email, transcript, webhook, attachment, SLA and other unimplemented actions stay
  unsupported regardless of configuration

`blocker` denotes a definite current static incompatibility; `review` describes
an ambiguity/behavior to assess; `notice` describes an observed default or scope;
`unknown` keeps unassessed runtime evidence visible. Severity never changes
runtime execution or disables a rule.

## Safety bounds, configuration, and incomplete evidence

Each source permits at most 1 MiB conditions and 1 MiB actions, token nesting 32,
1000 condition records, 10000 aggregate operand nodes, JSON number
tokens of at most 128 bytes and absolute exponents at most 4096, and 200 returned
issues. The numeric bounds prevent a short saved exponent from requesting
expensive arbitrary-precision parsing during a read-only report. SQL guards check bytes before materializing blobs (SQLite BLOB length;
PostgreSQL octet_length). Unknown event text is reduced to an allowlisted event
or `unknown`. Page output is capped at 2 MiB including the success envelope;
a continuation or bounded individually-oversized summary preserves progress.

These are inspection bounds, not new runtime/compiler limits. Exceeding one
returns `analysis_limit_exceeded`, incomplete analysis, and no fabricated runtime
rejection. When issues exceed 200, highest-impact findings are retained and the
source is incomplete with exact omitted count. Actual runtime limits remain the
existing 100 source actions, 100 normalized labels per action, 100-byte label,
100000-byte message/note, and 1000 expanded plan steps with reserve-for-later.

Pure inputs can supply non-secret composition facts about owners and request
caps. Presence is not connectivity. The normal endpoint deliberately supplies
no inferred deployment profile, so configuration remains unassessed. Required
owners include transitive CON-to-TEN assignment needs; this adds no AUT-to-TEN
call or cross-owner SQL. An unavoidable JSON-encoded value larger than a declared
request cap is a lower-bound failure; otherwise envelope size fit stays unknown.

No target existence, custom catalog, current permissions/credentials, owner
reachability, provider/channel suitability, feature/quota, client adoption,
actual migration, or cutover acceptance is established. No fake conversation or
dry-run command is used. The report is tooling to review saved definitions, not
a readiness certificate. ARCH-04/ARCH-08, client/frontend, live deployment,
load/soak, broad trigger coverage and provider-delivery gates stay open.

## Architecture and future semantics

AUT owns the pure DTOs/projector and tenant-scoped rule/version reads; the account
handler is the existing ORC composition seam. The collaboration inventory adds
only `ListRuleCompatibility -> aut.rule.get.v1` as a read adapter and updates its
exact fingerprint with the source guard intact. No registered module query,
mutation contract, dependency edge or persisted schema is added. A dedicated
source test enforces AUT model ownership, pure projection and no write/owner-call
methods in the report path.

A later numeric comparison implementation must be a separately approved explicit
per-rule/version semantics adoption. Existing saved rules and frozen executions
must retain today's behavior. days_before and general attribute_changed lack
resolved timing/operand contracts and remain deferred. This patch does not start
that stage.
