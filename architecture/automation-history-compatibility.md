# Phase14 account automation history compatibility

Phase15 valid no-op activity and v4 snapshot behavior are documented in
[automation-activity-parity.md](automation-activity-parity.md). Its bounded
new-admission policy supersedes older no-op/version statements below; historical
policy-less runs retain their prior semantics.

Legacy remains the default. This read/query and presentation layer does not
change execution, cancellation, provider availability, snapshots or the mode
lock. A client must explicitly select the adapter before relying on its logical
results. Passing these local API tests is not acceptance of an unchanged external
client, frontend, provider, production rollout, or every G16 migration concern.

## Existing wire contracts stay distinct

- `GET /api/v1/accounts/:account_id/automation_rules/executions` returns an array
  in the existing `data` envelope. Detail is the same path plus `/:execution_id`
- With no `view` (or `view=raw`), GET still exposes the model's stored `status`,
  JSON-encoded **string** `action_results`, and optional `error`. Legacy historical
  bytes, including malformed historical result strings, are not rewritten
- `POST /api/v1/accounts/:account_id/automation_executions/:execution_id/cancel`
  retains its existing default parsed owner-step **array** and allowlisted
  `reason_code`. The module `aut.execution.get.v1` contract is unchanged
- All three ordinary account routes accept `view=compat_v1`. This explicit view
  gives the same model-shaped/string result representation on list, detail and
  cancel. An unknown view is HTTP 400, before cancellation can execute
- No private runner, evaluation, frozen parameters, command envelope, actor,
  lease, snapshot or message/note body is returned by the compatibility projector

The old GET and cancel types cannot be unified implicitly without breaking one
of their existing clients. The explicit adapter is the deliberate compatibility
boundary. Old clients expecting names and legacy statuses must opt in and still
handle newly truthful states and outcomes before any cutover is considered.

## Query and paging contract

`rule_id` and `conversation_id` remain positive decimal ID filters, now validated
consistently on SQLite and PostgreSQL. `status` matches both values in these
families, in either direction:

- `success`, `succeeded`
- `partial_failed`, `partially_succeeded`
- `depth_exceeded`, `loop_blocked`

All other values, including unknown future states, match literally. In
particular no_match, received, evaluating, running, waiting, cancelled and
manual_action_required are not collapsed into success/failure. Aliases classify
execution-level owner-step progress; they do not assert logical completion.

The default stays the 100 newest records. Optional `limit` is 1 through 100;
`order=desc` (default) or `order=asc` sorts by `triggered_at` and then `id`, both
in that direction. Equal timestamps therefore have a deterministic order.
There is no unvalidated SQL sort field or offset. Invalid query values return
HTTP 400.

When another page exists the response supplies `X-Next-Cursor`. Send its complete
opaque value in `cursor`, retaining the account, rule/conversation/status filters
and sort direction; limit and view can change. Equivalent status aliases bind to
the same filter. The data envelope remains an array and no total is introduced.
The endpoint adds X-Next-Cursor to Access-Control-Expose-Headers while retaining
existing exposed headers and origin policy. Cursors are read positions, not credentials: each database query independently
scopes the tenant and filters. A high-water ID excludes later inserts, including
backdated ones. This is not snapshot isolation across requests: status changes,
record deletion, or administrative changes to old sort fields can change a live
traversal. Refresh without a cursor for a fresh view.

## Logical result contract

The compatibility view declares `result_schema=compat_v1` and preserves
`raw_status`. Its `status` uses only the three aliases above, choosing the legacy
spelling. New or unknown states remain distinct. `action_results` remains a JSON
string of logical entries, with `action_name`, `status`, optional `error`,
`logical_action_index`, `completed`, `outcome` and allowlisted `owner_steps`.

Original names (including aliases) and positions come from the exact immutable
rule version. Mutable current definitions are never used. Legacy rows with no
version use their persisted names. Older strict plans without logical indexes
may be grouped only when the complete frozen plan and result identities match.
Missing, malformed or inconsistent evidence gets an explicit
`action_results_state` diagnostic rather than an invented successful action.

For durable logical entries, `completed=true` means every required compiled step
succeeded. Legacy entries instead expose recorded historical success with
`action_results_state=legacy` and `outcome=legacy_success`; this does not independently
verify an old capability, mutation or provider action. Free-form legacy error
messages are omitted from the explicit projection; safe code-like reasons remain
available in error/reason_code. The raw GET view retains those historical bytes.
No completed value means external delivery. Public message creation reports `queued`; private creation
reports `saved`. A successful lookup or message preparation followed by a failed
effect is a failed logical action with `partial_progress`, not a successful
logical action. Multi-effect logical actions can report `partial_failed` when an
actual earlier effect succeeded. Their owner_steps retain original expanded
`action_index`, new `logical_action_index`, status, reason_code, version, resource
and outcome so earlier progress remains inspectable. No cross-owner rollback or
provider sent/delivered status is invented.

## Cancellation and migration boundary

Permissions and tenant isolation are unchanged. Legacy mode still returns 409
for cancellation. Queued durable work can cancel immediately; running/waiting
work requests cancellation and reconciles accepted/unknown work before stopping
future steps. An in-flight final effect can finish successfully. Repeated
cancelled requests are idempotent and other terminal states reject. Committed
changes are never undone. `compat_v1` reads the resulting stored state after the
same cancellation operation; a concurrent runner can already have advanced it.

G16 has an implemented, explicit local compatibility surface with deterministic
paging; client adoption and frontend/reporting acceptance remain open. G09
queued/saved truth remains intentional. G02/G03 operators and typed changes,
G08 optional providers, G11 parameter/definition limits, G12 no-op activity and
G17 broader architecture are independent gates. No default switch follows from
this implementation.

## Architecture inventory

The account handler still reads only AUT through its service. The source-method
binding replaces ListExecutions with ListExecutionsPage and adds the explicit
CompatibilityExecutionViews read adapter. The pure projector receives only an
execution and its immutable AUT version. Registered module contracts and owner
directions are unchanged; the exact collaboration-inventory fingerprint is
updated with its source guard intact. ARCH-04/ARCH-08 remain open.
