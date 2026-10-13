# Phase9 bounded monolith AUT bridge

Router Macro/Public owner composition and its bounded transaction/version behavior
are documented in [automation-owner-composition.md](automation-owner-composition.md).

Phase15 valid no-op activity and v4 snapshot behavior are documented in
[automation-activity-parity.md](automation-activity-parity.md). Its bounded
new-admission policy supersedes older no-op/version statements below; historical
policy-less runs retain their prior semantics.

Historical phase9 baseline. The local action and condition limits below are
superseded by automation-actions-parity.md, automation-conditions-parity.md,
and automation-trigger-parity.md;
the mode lock, activation and owner workflow guarantees still apply.

This is an opt-in local integration, not a production rollout or full legacy parity.
The default remains `AUTOMATION_EXECUTION_MODE=legacy`. No external provider,
credential, push, deployment, browser UI, or long-duration capacity claim is made.

## Execution ownership and activation

`legacy` retains the immediate existing service. `durable` suppresses that executor
for the whole service: there is no per-rule fallback. CON/MSG transactions persist
trigger facts before asynchronous work. The application composes AUT, CON, and MSG
contracts through an in-process HTTP transport; it opens no internal listener and
exports no authentication token. `cmd/server` supplies the worker cancellation
context. Application composition constructs the ORC creation coordinator from
CON/MSG owner ports and passes it explicitly to `NewMonolithAutomation` before
starting the outer bridge. The bridge calls creation recovery, then AUT intake
and action draining; AUT `Runtime` never calls ORC. Embedded callers must use this
composition and either supply `router.Options.BackgroundContext` or drive its
`AutomationBridge.RunOnce`. A bare AUT runtime or a bridge literal without a
workflow coordinator does not recover creation plans. Owner preparation can run
in the request; automation rule effects remain asynchronous.

Startup claims a database-wide mode in the AUT local journal. Conflicting new
instances fail startup. The claim is never automatically changed. Stop all old
instances before first durable activation; old binaries cannot enforce this new
claim, so a mixed-old-binary rolling deployment is unsupported. A database already
claimed by the other mode needs a separately approved offline migration after
all processes stop and pending work is reviewed. Merely toggling an environment
variable is intentionally insufficient. No such migration has been performed.

Durable startup freezes previously unversioned legacy rules at activation time,
without backdating a snapshot. An active preexisting `conversation_opened` rule
blocks activation explicitly. API create/update/clone rejects that event in
 durable mode; its legacy validator behavior is unchanged. No new event mapping
is invented. CON uses a durable activation watermark to exclude old runtime facts.
Only enabled capture creates ordinary CON/MSG bridge facts, so legacy-handled
ordinary events are not replayed during a later activation.

## Bounded parity map

- Event names: `conversation_created`, `conversation_updated`, `message_created`
- Empty conditions: empty string, `[]`, `{}` normalize to `[]`
- Status: `change_status` with open/pending/resolved (`closed` maps to resolved),
  `resolve_conversation`, `close_conversation`, `close`, `resolve`, `open_conversation`
- Priority: `change_priority` with low/medium/high/urgent
- Explicit v1 capabilities: `conversation.change_status`, `conversation.change_priority`
- Supported legacy structure: arrays, single action objects, actions/rules/values
  wrappers, JSON strings at the API, name/params aliases; scalar status/priority
  values normalize to a one-element array. Capability identity/version is preserved
- Parameter validation is strict: exactly one expected field or array value;
  ambiguous extra parameters are not silently ignored
- Conditions and other actions (assignment, labels, message/email/webhook/provider,
  private notes, mute, snooze) record explicit failed executions before any action
- `conversation_opened` is rejected in durable mode; neither path had an emitter

## Causal rule ordering

All matching rules for one fact are frozen in ascending original rule ID order in
one AUT intake transaction. Private batch and predecessor identities bind that
order to the original account, conversation, event, owner version, and intake
receipt. A later rule cannot claim work while any earlier rule in the batch is
active, leased, or waiting for an unknown owner result. Other facts and tenants
remain eligible; a blocked batch is not a global queue head.

The runner reconstructs the causal owner version from durable earlier action
results. Only exact contiguous owner-command receipt confirmations advance the
version. A no-op, unsupported, rejected, or cancelled rule contributes no invented
effect; already confirmed effects from a partly completed rule are retained.
Queued cancellation can skip a rule before its predecessor finishes without
losing the predecessor's eventual version. A corrupt/quarantined or otherwise
unverifiable outcome blocks later effects with `causal_predecessor_unverified`.

This is not a general stale-state rebase. An unrelated manual update between
rules still fails CON's version comparison; the runner cannot adopt that update's
version. The original target and execution identities remain immutable. Unknown
responses reconcile the same owner command before any successor can proceed.
Loop ancestry/depth remains the event's ancestry, separate from sibling ordering.

## Owner workflow completion

Durable authenticated conversation creation selects initial routing before the
CON create commit, so the selected assignee/team and created fact share one owner
transaction. Legacy mode retains its original create-then-route behavior. Routing
capacity selection retains the existing best-effort cross-instance semantics.

For ordinary widget/public inbound messages, MSG persists the handler-authorized
reopen intent and expected CON version with message creation. CON applies activity
and conditional reopen atomically under its owner row lock and stable receipt.
The fact uses that receipt's exact target. A manual mutation in between vetoes the
reopen and leaves a deliberately stale automation target rather than adopting the
manual version. CON rechecks mute/version under its lock. A pending actual reopen also reads the
current CUS-owned contact before its CON transaction; a newly blocked contact
vetoes automatic reopen, and an unavailable contact owner keeps activity pending.
A duplicate completed receipt preserves its earlier decision. This is a fresh
owner-port observation, not an atomic CUS+CON transaction; a change after that
observation has the same cross-owner race window as other separate-owner reads.
Ordinary message automation still fires for muted/blocked inputs, matching the
existing handler callbacks; automatic reopen and push eligibility are separate.

An ORC/application coordinator with no database drives CON plan reads, MSG local
message creation, and CON receipt-only finalization. CON never synchronously
invokes MSG, and RTE only selects routing and persists the plan before API-owned
coordination. This is bounded application coordination, not implementation of
standalone ORC business contracts or a reverse AUT-to-ORC module dependency.

Creation's planned initial/off-hours local messages are constituent workflow steps,
not independent legacy message callbacks. Their immutable plan, idempotent message
receipts, and causal CON activity chain gate the created fact until completion.
Recovery can finish a committed plan after a restart. The captured envelope stays
immutable, including its event ID, occurrence time, and aggregate version. The
owner source gates delivery until completion, then enriches only the automation
payload's target object version from its verified receipt; AUT persists that
received target with its intake. No published source journal body is rewritten.
Only trusted internal planned
message creation suppresses a separate automation trigger; ordinary incoming
message APIs still produce `message_created`. No provider is contacted by these
local workflow steps.

Remaining bounded limitations:

1. Conditions and actions beyond the status/priority subset remain explicit failures
2. Other owner paths outside the enumerated repository mutations, historical imports,
   explicit message retry, and snooze scheduler events are not blanket trigger parity
3. Runner cancellation does not undo an action committed or with an unknown result
4. No total event ordering or causal rebase across unrelated facts is claimed

## Durability, ordering, and visibility

CON captures ordinary create/edit/status/priority/assignment/label/custom-attribute/
mute/macro changes atomically, with monotonic object versions. Generic Update uses
an explicit editable field set plus version comparison; it cannot resurrect a
removed conversation or overwrite unread/SLA/identity projections through Save.
MSG commits its fact with the message. Eligibility waits for its durable MSG-to-CON
activity receipt; the target version comes from the CON owner's stable receipt,
not from a fresh mutable conversation read. Redundant post-message activity touches
are removed, and activity timestamps are normalized to UTC.

Fact selection uses recorded occurrence time, not an alleged total commit order
across databases. Only snapshots strictly earlier than that recorded time qualify.
An intervening edit/disable/delete produces an explicit failure rather than using
a new definition for an old fact. Equal-time ambiguity cannot select a new version.
Once admitted, actions are frozen in the durable runner checkpoint and remain so
across rule edits/restarts. Normal intake queries one latest snapshot per rule;
older-event lookup pages edited-rule histories with timezone-aware Go comparisons.

Stable AUT intake identities, execution keys, owner command receipts, leases, and
separate owner delivery receipts cover replay and multiple same-mode workers.
Malformed facts are durably quarantined for manual repair and do not starve later
facts. Valid future-dated facts remain pending while later eligible facts proceed.
Database/transport errors remain retryable. CON runtime outbox delivery state is
not stolen by the automation bridge. Rule ancestry and depth persist through real
CON action facts, so self-triggered updates are blocked by the shared loop guard.

## Cancellation and inspection

`POST /api/v1/accounts/:account_id/automation_executions/:execution_id/cancel`
requires `automation_manage`, checks the tenant, and uses AUT's existing cancel
contract/receipt logic. Queued work cancels immediately; in-flight/unknown work
reconciles the stable owner action and skips future actions. Existing execution
list/detail endpoints expose statuses and reasons. A repeated cancel is idempotent;
query execution detail for current status after an earlier in-flight response.

## Verification limits

Covered locally: ordinary authenticated APIs; frozen/versioned rule changes;
canonicalization; actor/tenant boundaries; queued and unknown/in-flight cancellation;
restart/replay; local owner effects; stale-target rejection; causal sibling ordering; owner workflow completion; causal loop protection;
poison/future facts; actual PostgreSQL transaction and multi-instance contention.
The native PostgreSQL/Redis wrapper is loopback-only. Tests never contact providers.

No safe old-binary rolling upgrade, automatic offline mode migration, full legacy
condition/action parity, throughput/soak acceptance, real provider delivery, frontend
acceptance, or production deployment is implied. ARCH-04 and ARCH-08 remain open.
