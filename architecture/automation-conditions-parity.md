# Durable automation condition parity

Phase17 adds only explicitly adopted numeric custom comparisons; see
[the profile contract](automation-numeric-conditions.md). Markerless rules and
old frozen executions retain the behavior described below.

Phase15 valid no-op activity and v4 snapshot behavior are documented in
[automation-activity-parity.md](automation-activity-parity.md). Its bounded
new-admission policy supersedes older no-op/version statements below; historical
policy-less runs retain their prior semantics.

Phase12 retains phase10 comparison semantics and restores ordered sibling
condition visibility for newly admitted fact batches. The default execution mode
remains legacy. No provider, deployment, or activation is implied.

## Semantics

The differential test calls the unchanged legacy `AutomationService` and observes
its recorded execution; it does not use a hand-written legacy oracle.

| Area | Preserved behavior |
| --- | --- |
| Empty definitions | Only empty string, `[]`, and `{}` are unconditional |
| Structures | Arrays, single objects, `conditions` / `rules` / `values` wrappers, in that order |
| Aliases | `key` -> `attribute_key`; `operator` -> `filter_operator`; scalar `value` only when `values` is empty |
| Fold | Left to right; previous condition's case-insensitive `OR`; otherwise AND; no boolean precedence rewrite |
| Conversation fields | status, priority, inbox_id, assignee_id, team_id, contact_id |
| Message fields | content, message_type, private_note; nil outside message_created |
| Contact fields | email, phone_number, country_code, city, company_name |
| Company | Loaded company name wins, including an empty name; otherwise contact custom-attribute fallback |
| Conversation attributes | browser_language, conversation_language, referer, mail_subject and any actually present custom key |
| Scalar equality | Legacy Go stringification, case-sensitive; IDs compare to numeric or textual values |
| Text operators | contains/includes, does_not_contain, starts_with are case-insensitive |
| Presence | nil/empty string absent; false and numeric zero present |
| Label aliases | labels, label, tag, tags, label_ids, conversation_labels |
| Label equality | Trimmed condition values; case-insensitive title or decimal ID; any match |
| Label containment | Title substring or exact title/ID for contains/includes; does_not_contain only tests title substring, as legacy does |
| Unknown operators | Do not match |
| Unknown custom key | Does not match, including is_not_present |
| Invalid definition | Explicit invalid_frozen_definition, never unconditional admission |

Labels have their legacy specialized operator set, including presence and
negative membership. They do not acquire a starts_with operator. Empty wrappers,
null, or unparseable JSON remain distinct from an explicitly empty rule.

## Ownership and time

CON provides conversation state, custom attributes, and owned label IDs. MSG
provides the triggering message, never an invented latest message. CUS provides
only needed contact/company/label data through tenant-scoped owner contracts.
No foreign SQL or in-transaction cross-owner local calls are used.

The first rule and direct starts evaluate at admission. For later siblings with
nonempty (or invalid) conditions, intake freezes the exact rule version,
conditions/actions and initial shared observation, but defers the decision.
Unconditional definitions remain known matches without an extra evaluation pass.
Original rule-ID order also applies across the status aliases of one owner fact.

After all preceding executions are terminal and their confirmed owner receipts
prove a contiguous CON version, the runner claims a read-only evaluation lease.
A strong CON observation must match that exact version. A stale or unavailable
CON target records an explicit failure; it never adopts an unrelated live version
or turns a dependency outage into `no_match`. Unknown/quarantined predecessors
cannot authorize evaluation. An ordinary transport/store error releases the lease
with a retry time; other facts remain runnable. All owner reads occur outside AUT
transactions, including the single-connection monolith.

The legacy engine carries one in-memory conversation across its ascending rule
loop. The new path preserves that distinction instead of replacing the shared
object with a fresh owner view:

- Status, priority, agent/team assignment and removal, mute-derived resolved status,
  and snooze-derived status are projected from confirmed predecessor action steps
- Inbox/contact IDs, conversation custom attributes, triggering message content and
  flags, and cached contact/company data retain the initial owner observation
- Label conditions use the exact-version CON observation's label IDs and fresh,
  tenant-scoped CUS label records, matching legacy's storage-backed label lookup

Only needed CUS fields are queried. Initial contact/company observations remain
admission-time observations, not an atomic historical event snapshot. No new
cross-owner transaction or total ordering against external CUS changes is claimed.
A contact/company first loaded later in a truly concurrent legacy callback may
observe different external changes; this stage does not promise concurrent
external-edit equivalence. Manual CON edits still veto later evaluation/actions
through version checks. Edits after a committed decision do not cause reevaluation;
the subsequent action retains owner CAS.

The observation, exact proved version and match/no-match/invalid decision commit
in one fenced AUT transaction before any action dispatch. A crash before that
commit may repeat read-only preparation; exactly one durable decision wins. A
lost lease cannot overwrite a newer decision or cancellation. A cancelled pending
evaluation has no effects and does not block later siblings once their earlier
version proof is available. Already committed owner effects remain governed by
the existing cancellation and failure policies.

`no_match` remains a durable successful evaluation with no action effects,
`conditions_not_matched`, skipped action results, and the ordinary terminal event.
Its decision and observation survive replay, restart and snapshots without owner
rereads. Matching decisions similarly remain frozen, including after rule edits.

## Snapshot and migration boundary

New private snapshots use `aut.execution.v2`, adding the explicit sibling
evaluation checkpoint. The `aut.fact.v3` receipt and `aut.fact_batch.v2` manifest
bind which executions require that checkpoint. Restore verifies the frozen rule,
condition decision, projected fields, predecessor proof and manifest membership;
invalid restores roll back atomically. The public execution JSON and rule
create/update API shapes are unchanged.

Old complete `aut.execution.v1` snapshots are accepted with no evaluation field.
Their already-frozen admission decisions are retained exactly; old queued work is
never silently re-evaluated using phase12 semantics. Historical public-only or
incomplete codecs remain rejected. This is an explicit compatibility boundary:
new behavior applies to newly admitted batches, not retroactive rewriting of old
no-match history. The existing offline mode transition and review remain required.

## Bounded verification and remaining gates

Differential fixtures call the unchanged actual legacy engine for created and
ordinary updated facts, comparing matched rule IDs and final status/priority,
agent/team and label effects. They include urgent and reversed-low conditions,
multiple/no-match siblings, assignment removal, add/remove labels, and a phantom
preloaded label proving storage is authoritative. Recovery fixtures cover leases,
owner outages, cancellation, stale manual edits, snapshots, immutable definitions
and concurrent PostgreSQL workers.

This closes the bounded successful sibling-outcome difference, not general
migration parity. Continue-after-failure versus stop, whole-plan rejection,
parameter limits, history/client shapes, no-op activity, unsupported operators and
external provider capabilities remain separate gates. Legacy remains the default.

Direct start uses a context-only, optional command preparation hook before the
local receipt transaction. It binds the selected rule version and rechecks it
under the AUT transaction. Receipt replay still works when dependencies fail;
a receipt committed during a failed preparation wins over the dependency error.
Existing command digest and transaction receipt checks remain authoritative.
