# Durable automation trigger coverage

Router Macro/Public owner composition and its bounded transaction/version behavior
are documented in [automation-owner-composition.md](automation-owner-composition.md).

Phase15 valid no-op activity and v4 snapshot behavior are documented in
[automation-activity-parity.md](automation-activity-parity.md). Its bounded
new-admission policy supersedes older no-op/version statements below; historical
policy-less runs retain their prior semantics.

Phase11 extends the opt-in durable trigger surface. The default remains
`AUTOMATION_EXECUTION_MODE=legacy`; this work does not activate a database, push,
deploy, or contact an external provider.

## Declared behavior and legacy baseline

This is an explicit extension where the unchanged legacy emitter has a gap,
not a claim that legacy already emitted these events.

| Rule event | Existing legacy API validation / callback | Durable behavior |
| --- | --- | --- |
| `conversation_created` | Accepted; creation callback | One created fact, gated by the existing creation workflow |
| `conversation_updated` | Accepted; update callback | Retained on ordinary owner changes and supported CON action facts |
| `message_created` | Accepted; message callback | Retained on ordinary message creation after its stable CON activity receipt |
| `conversation_opened` | Accepted but no emitter | Alias only for a captured actual transition from another valid status to `open` |
| `conversation_resolved` | Rejected by legacy API validator | Accepted only in durable API mode; alias only for a captured actual transition from another valid status to `resolved` |

Standalone AUT rule commands accept all five names because they belong to the
durable runtime. Unknown names remain rejected. Create, update, and clone keep
existing definition validation. Existing active opened rules can now be frozen
at durable activation; administrators must review their newly effective meaning
before an independently authorized activation. The database-wide execution-mode
claim and offline migration requirements are unchanged.

## Actual transitions

- Open creation emits `conversation_created`, without `conversation_opened`
- `resolved` -> `open`, `pending` -> `open`, and `snoozed` -> `open` carry opened
- `open` / `pending` / `snoozed` -> `resolved` carry resolved
- Repeated `open` -> `open` or `resolved` -> `resolved` writes retain the existing
  generic updated behavior; they do not acquire a status alias
- Transitions to `pending` or `snoozed` retain generic updated behavior
- A failed owner mutation, stale version, wrong tenant, or rolled-back transaction
  creates no transition fact

The source transaction records `status_before` and `status_after` alongside the
existing owner event, only for actual changes. Both must be valid known statuses,
unequal, and accompanied by `status` in `changed_fields`. The status alias is
never inferred from mutable current conversation state or just a requested action.
Malformed markers are quarantined rather than converted to aliases. Old facts
without markers remain replayable with their original generic semantics.

Covered ordinary mutation paths are `UpdateStatus`, the guarded generic
conversation `Update`, and macro mutations. Runtime status commands and the
automation mute action also record their actual before/after states under the
owner lock. A mute that really resolves the conversation therefore carries
resolved; a no-op mute produces no invented transition.

## One fact, one causal batch

A status alias and its generic updated event are views of one owner fact and one
owner version. They are not independent outbox records. AUT admits matching rules
for these views in one durable batch with the original rule-ID order, one condition
observation, and the existing causal predecessor checks. An earlier status rule's
confirmed effect can advance a later generic rule's target within this batch.
Unrelated edits still fail version comparison; this is not a general stale rebase.

For an ordinary incoming message that actually reopens a resolved conversation,
CON commits the reopen markers into the same stable activity receipt as its owner
version. Its optional transition query validates that immutable receipt; it never
reads the conversation's later status. MSG enriches its pending `message_created`
fact using that receipt, preserving the original MSG journal envelope. That fact
admits message, updated, and opened rule views together. Conversation rule views
have no triggering-message condition context. Muted, blocked, stale-version,
already-open, and no-reopen-intent activity receipts have no status alias. Historical
receipts without markers are not retroactively interpreted as opened events.

## Bounded exclusions and verification

Snooze-scheduler expiry now captures its actual guarded transition through the
CON owner transaction; see [expiry lifecycle](automation-snooze-expiry.md).
[Explicit local retry](explicit-message-retry.md) now has a separate receipt-verified
compatibility fact on the existing message, with queued/saved outcomes rather than
a provider-delivery claim. Historical imports remain deliberately silent. Owner
paths outside the listed contracts are not blanket-covered.

Focused tests cover the ordinary writer/transition matrix, creation and repeated
writes, transaction rollback, durable replay, malformed-marker quarantine,
immutable reopen receipts, rejected reopen cases, mode-specific API validation,
service freezing and updates, and runtime status/mute event metadata. Integrated
AUT tests cover the shared alias batch and cascades separately. Provider sends,
frontend behavior, production rollout, and long-duration load remain unverified.

## Action-produced MSG and CON causal families

MSG action saves now enter durable AUT through the MSG owner fact port. Public
replies remain outgoing/queued; private notes remain outgoing/private/saved. No
message body is copied into a fact. CON preparation already accounts for message
activity; intake does not apply it again. Incoming, public outgoing, and private
outgoing conditions read the actual triggering MSG-owned record at admission.
This is an explicit durable extension: the legacy automation reply implementation
did not call its message-created callback.

The MSG source verifies each action fact against its own consumed-grant receipt,
including command, tenant, conversation, original version, message identity,
visibility, content hash, and origin/depth. It leaves runtime outbox envelopes and
external fanout state unchanged. Separate MSG delivery/quarantine receipts belong
to this consumer. Ordinary reopened MSG facts enrich only the returned payload
from the CON activity receipt; ordinary journals are not rewritten.

AUT waits until the complete origin fact batch is terminal. It reconstructs its
final version from contiguous confirmed owner-command results; pending, leased,
unknown, or quarantined outcomes cannot authorize a descendant. New intake
receipts have an `aut.fact_batch.v1` execution-ID manifest, validated on traversal
and snapshot restore, so a missing final sibling cannot look like completion.
Historical phase9/10 receipts retain their existing predecessor-chain compatibility.
Already-consumed historical action facts lacking a causal-admission claim cannot
prove this new cross-fact order; affected families wait for reviewed migration or
repair. This is not a hot migration for an already-activated durable database.

All successful event-emitting CON/MSG action steps in that batch form the expected
child set. Message preparation, CUS lookup/creation, failed/skipped steps, and true
CON no-ops are not invented events. The expected set comes from frozen dispatches,
not current source-page membership. Families advance breadth-first, then by parent
rule/action order within each generation. Each earlier child's complete terminal
batch contributes only its confirmed effects before the next fact is admitted.
This supports two messages in one plan, messages from several sibling rules, and
mutual message-producing rules without arbitrary sibling staleness. Missing facts
remain pending rather than silently being skipped or reordered.

The source event ID, occurrence time, aggregate sequence, original owner version,
and ancestry stay immutable. AUT stores the original envelope privately alongside
its admission envelope, whose effective `object_version` is derived solely from
that causal family; `origin_object_version` retains the original version. A unique
owner-command claim prevents an alternate event ID from creating another causal
intake. Replays use the existing receipt and never rerun actions. Admission again
compares the proved version to CON, and action dispatch still uses owner CAS, so an
unrelated manual change fails rather than being adopted.

Cancellation retains effects already confirmed and reconciles unknown effects
before concluding; future steps remain skipped. Cancelled siblings contribute no
invented mutation. The existing ancestry repetition guard and maximum depth of 3
bound direct and mutually triggering message rules. Distinct sibling facts are
not collapsed by a rule/target-version heuristic after a no-match or empty action;
immutable fact receipts and ancestry are their duplicate/loop authority.

## Paging, recovery, and remaining boundaries

Both owner sources expose an optional opaque, owner-bound cursor with a fixed
journal-ID horizon per pass. The bridge walks all pages in that finite horizon.
A first page of blocked/unverified descendants cannot hide a healthy later family.
Facts emitted while draining belong to a later pass. Cursors contain no shared
mutable state and acknowledgements remain durable and replay-safe across workers.
A source without the optional page port retains its original one-page contract.

Corrupt causal proofs create an AUT diagnostic with `causal_origin_unverified`;
the source is not acknowledged and dependent family effects remain blocked for
repair. A missing/quarantined/deleted expected event also blocks later members of
that family. Other families remain eligible. This is safe bounded causal ordering,
not a general total order across unrelated manual/API facts or a promise that
corrupt/deleted families automatically recover.

Snapshot checks preserve alias batch membership, frozen dispatch/result prefixes,
original/admitted envelopes, unique causal claims and cross-generation version
proofs. The phase11 private execution codec was `aut.execution.v1`; phase12 adds
`aut.execution.v2` for deferred sibling condition decisions, while preserving
existing v1 decisions on restore. No public
execution response schema or HTTP command/query contract is added in this stage.
The strict contract fingerprint and ownership/dependency guards remain in force.

Phase12 restores bounded sibling condition timing as described in
automation-conditions-parity.md. Remaining compatibility gates include stop versus
continue-after-failure, permissive legacy parameter coercions, queued/saved versus
sent result meaning, no-op activity differences, execution-history/client shapes,
and unimplemented numeric/date/changed-field conditions or provider capabilities.
This stage does not change those policies or justify enabling durable mode.

Verified locally: actual CON/MSG owner state and ordinary API paths; public/private
conditions; no-op/create exclusions; terminal-origin and whole-batch waiting;
unknown/cancel/restart; manual interleaves; two-message and multi-rule siblings;
105-message paging and delayed delivery; healthy intake past a blocked first page;
immutable receipts, alternate identity rejection, tenant boundaries, mutual loop
and depth limits; snapshot corruption rejection; concurrent PostgreSQL workers.
Provider delivery, UI acceptance, throughput/soak, old-binary rolling upgrades,
and production activation remain outside this bounded validation.
