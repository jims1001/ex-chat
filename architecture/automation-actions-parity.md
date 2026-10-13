# Phase10 local automation action parity

Phase15 valid no-op activity and v4 snapshot behavior are documented in
[automation-activity-parity.md](automation-activity-parity.md). Its bounded
new-admission policy supersedes older no-op/version statements below; historical
policy-less runs retain their prior semantics.

The default remains legacy. Durable mode is still an explicit opt-in with the
phase9 mode lock, activation watermark and fact-source guarantees. This stage
does not send through any provider, deploy, switch the default, or implement
email/webhook/provider actions. The phase9 bounded action list is superseded by
this document and automation-conditions-parity.md.

Phase13 continuation and snapshot compatibility are documented in
automation-failure-continuation.md; the old failure-policy statements below are
historical where superseded.

## Legacy action mapping

| Legacy names | Durable owner effects |
| --- | --- |
| assign_agent; remove_assigned_agent | CON same-account TEN member assignment; removal |
| assign_team; remove_assigned_team | CON same-account TEN team assignment; removal |
| mute_conversation | One CON transaction sets resolved, muted=true, clears snooze |
| snooze_conversation; snooze | CON snoozed status and frozen UTC deadline; valid positive Go duration or legacy 24h default |
| change_status; resolve_conversation; close_conversation; close; resolve; open_conversation | Existing CON status contract; closed normalizes to resolved |
| change_priority | Existing CON priority contract |
| add_label; add_labels | CUS tenant title lookup/create, then CON validated attachment |
| remove_label; remove_labels | CUS tenant ID/title resolution, then CON detachment; missing target no-op |
| send_message; send_reply; reply | CON guarded preparation, then one MSG public outgoing row with status queued |
| add_private_note; private_note | CON guarded preparation, then one MSG outgoing private row with status saved |

Legacy scalar, array, and named object parameter forms are supported. Agent
aliases user_id/agent_id and team aliases team_id/team_ids/id are recognized.
Message aliases message/content and label labels/label/remove are recognized.
Explicit v1 capability IDs are supported for status, priority, assign/remove
agent/team, mute, snooze, label add/remove and public/private message creation.
Fractional IDs and ambiguous multi-field object fallbacks are rejected rather
than reproducing nondeterministic/lossy legacy parsing. Valid status values remain
open, pending, resolved; snoozed uses the dedicated deadline-bearing action.
A rule has at most 100 logical definitions, 100 labels in one definition and 1000
expanded owner steps. For new plans, unsupported/invalid logical actions fail explicitly while independent
later supported actions remain eligible; see automation-failure-continuation.md.
Old admitted plans retain their frozen stop behavior.

## Durable plan and receipts

Each local command envelope is privately persisted in runner_context before
sending: actor, provenance, target and expected version, command identity,
parameters, snooze deadline, and the label ID or message grant obtained from the
preceding owner step. Unknown outcomes retry precisely that envelope. A step's
lease can expire on another instance without changing its owner idempotency key.
Multi-owner steps are individually visible as action_results with explicit step
labels (prepare_message/create_message and resolve_label/apply_conversation); status succeeded
means that local step completed. For messages, outcome queued or saved is explicit.
There is no sent/delivered claim, provider invocation, or provider dispatch queue
worker in this implementation. A private note body is excluded from automation
execution views, CON authorization grants and MSG events.

CUS/CON/MSG effects go through their contracts and local journals. Foreign owner
queries execute in registered preflights before receipt transactions; successful
receipts are checked before preflight and rechecked in the transaction. This
works with one SQLite connection and independent owner databases. The four new
owner commands bind actor identity to their receipts; prior capabilities retain
the byte-identical phase9 digest format.

CON message preparation atomically checks the version, advances it once and
records a content-hash/visibility/actor/provenance-bound authorization. MSG checks
that live CON authorization before a first local save; a later manual CON change
rejects the unconsumed authorization at this check. This is a multi-owner workflow,
not a cross-database transaction: the authorization check is its point-in-time
owner observation. MSG has a unique consumed-grant journal identity, so even
alternative command keys cannot create a second message. Once saved, MSG's
receipt is authoritative even if CON subsequently changes or becomes unavailable.
A MSG-owned durable per-conversation fence serializes first local creation with
the real conversation cleanup transaction. Once cleanup commits its terminal
tombstone, any stale grant is rejected and cannot recreate public/private rows.
Creation that commits first is removed by subsequent cleanup; pending deleted
message events are superseded. A grant-bound MSG event delivered to CON validates
its local preparation and records an inbox receipt without a second activity
version increment.

A stopped/failed workflow can retain its CON preparation or CUS label creation,
reported as partial local outcomes, never silently rolled back across owners.

## Version and recovery proofs

Status/priority and message preparation confirm exactly +1 CON version. New CON
assignment/label/mute/snooze actions confirm either an actual +1 mutation or an
unchanged true no-op. CUS resolution and MSG creation carry the last confirmed CON
version without inventing an increment. Causal sibling rules reconstruct this
ordered proof; no live-state rebase is permitted. Manual interleaving therefore
fails the next guarded CON effect or first unconsumed MSG authorization check.

The private snapshot contains frozen envelopes through
runner_context, without exposing them through public JSON. Restore validates
capability allowlists, action adjacency, envelope identities, origin, payloads,
versions and frozen-plan correspondence. Phase9 status/priority-only checkpoints
without dispatch arrays expand safely at their next claim. Unknown new action
snapshots preserve the actual owner receipt and reconcile once after restart.
The generalized version proof also supports condition no_match outcomes.

## Synchronous owner directions

AUT -> CON/CUS/MSG; CON -> TEN/CUS; MSG -> CON; TEN -> IAM. CUS does not call
CON for label creation. CON does not call MSG. The monolith composes those exact
contracts in-process without giving AUT access to foreign storage.

## Validation coverage

Tests inspect actual owner DB state, metadata-only event payloads, tenant guards,
manual interleaves, no-ops, queued versus delivered truth, private note privacy,
per-step lost responses, frozen snooze deadlines, multi-owner snapshot restore,
phase9 checkpoint upgrade, tampered private dispatch rejection, one-grant/one-row
concurrency and PostgreSQL owner CAS/title serialization. Existing architecture
and storage guards are retained; the declared contract fingerprint advances by
exactly four commands and one query with ARCH-04 still reopened.

## Historical phase10 message-trigger dependency

The phase11 bounded resolution is documented in automation-trigger-parity.md.
The following records the phase10 dependency; it is not a current intake limit.

This is local action parity, not complete action-produced message trigger parity.
MSG saves emit runtime event_outbox events carrying origin_execution_id and
bounded automation_depth. The existing monolith MSG source only consumes
repository automation_trigger rows bound to create/activity receipts; it does not
yet feed these new runtime events back into AUT. Legacy executeAction itself did
not call HandleMessageCreated, but the broader automation architecture requires
new action-produced message facts to distinguish incoming/outgoing/private and
retain ancestry. That broader requirement remains open.

Safe follow-on intake must wait for the originating execution/fact batch to
reach a proved terminal state, derive the final CON target version from its
confirmed causal receipts, and preserve the immutable message event identity and
original owner version. Otherwise ordinary later actions in the originating
plan would make the new message fact stale. Reading and adopting arbitrary live
CON versions would incorrectly rebase unrelated manual edits and is prohibited.
This patch does not silently add that unsafe rebase or claim recursive message
intake is complete. Legacy mode remains the default while this work is staged.
