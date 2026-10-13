# Phase15 valid local no-op activity compatibility

Legacy remains the default. This bounded G12 change preserves meaningful inbox
activity and ordering for valid supported local actions on new durable admissions.
It adds no capability, provider, condition operator, public policy selector, live
version rebase, default switch, deployment or historical backfill.

## Actual legacy behavior and bounded differences

The unchanged `AutomationService.executeAction` finishes supported successful
branches with `ConversationRepository.TouchActivity`. That advances
`last_activity_at` even if assignment, removal, label membership or mute state was
already satisfied. Status, priority and message preparation already advance
activity in durable mode. A fresh snooze normally changes its relative deadline;
a retry uses its frozen deadline and must not extend it again.

| Action/state | New admitted durable behavior |
| --- | --- |
| Assign the existing same-account agent/team | CON validates membership/team, touches activity once |
| Remove an already absent agent/team | CON touches activity once |
| Add an already attached valid label | CUS resolves; CON touches without reinserting the relationship |
| Remove an existing unattached or absent label | CUS resolves or returns missing; CON touches without deleting unrelated relationships |
| Mute an already resolved, muted, unsnoozed conversation | CON touches without fabricating a status transition |
| Exact already-applied frozen snooze | CON touches only for a distinct valid command; same receipt replay does nothing |
| Status/priority repeated writes; local message save | Existing activity behavior unchanged; public result stays queued, private stays saved |
| Unknown/unsupported/malformed/rejected action, no match or unattempted cancellation | No new activity effect |

Legacy also touched unsupported switch fallthroughs, missing assignment parameters,
and some rejected/ignored storage paths while reporting apparent success. Those
are excluded: G12 does not recreate fake capabilities, soften tenant validation,
implement provider no-ops, or turn failures into activity. Empty label lists,
invalid parameters and the existing coercion/bounds restrictions remain G11.

The compatibility contract is final business state plus observable activity and
ordering, not identical wall-clock timestamps or legacy internal write counts.
Legacy can write the same conversation several times per logical action. Durable
expanded label steps each retain an independently guarded owner receipt. Neither
`updated_at` nor internal version counts are claimed to be byte-for-byte legacy
outputs. `last_activity_at` is observable in conversation views and repository
ordering, and the actual-legacy tests exercise both.

## Frozen policy and owner transaction

New direct admissions and deferred sibling metadata freeze the private policy
`touch_valid.v1`. Existing `con.automation.apply.v1` envelopes include the exact
`activity_policy` input; their actor/input digest binds it to the stable command
receipt. No new owner contract or cross-owner transaction is introduced. The
compiler and immutable rule version stay unchanged. Old policy-less runner state
continues building byte-compatible policy-less envelopes.

CON still validates the automation actor/provenance, allowed operation, required
expected version, live owned target and relevant tenant references. For missing
label removal, zero is only the preceding successful CUS resolution's missing
sentinel. New-policy dispatch also carries the normalized original `missing_label`
string. A raw zero-ID/missing-parameter action is not automatically a valid touch;
normal compiler, adjacency and frozen-dispatch validation remain required.

When the validated command has no business-field mutation, CON atomically updates
activity and advances its version exactly once in the receipt transaction. The
receipt stores `activity_only: true` beside the resulting conversation. This is
persisted correctness evidence, not a live query flag. Timestamp advancement is
strict, even under clock skew, at PostgreSQL microsecond precision. Receipt
completion failure rolls the touch back. A replay returns the committed timestamp
and version, including after restart or later manual updates, without touching
again. First-attempt stale/unknown/unauthorized inputs never gain this effect.

Ordinary CON APIs, repository update semantics, supported mutations and existing
policy-less no-op behavior are unchanged. CUS resolution itself never touches CON.
MSG message preparation/creation retains its existing local outcome semantics.

## Causality, cancellation and truthful history

AUT records a proved activity-only result as `outcome=activity_touched`. For this
policy every successful CON apply is exactly +1; the activity-only outcome is
invalid for policy-less plans or other owner commands. Status/priority/preparation
and non-CON version rules stay unchanged. Cross-rule and deferred condition proofs
carry the activity increment like any other confirmed owner effect; they never
adopt arbitrary live versions. A manual same-value ordinary update still creates
a separate version and causes the next stale guarded action/evaluation to reject.

Legacy TouchActivity emits no automation trigger. Activity-only owner effects
likewise emit no event, and cascade enumeration excludes `activity_touched` while
carrying its confirmed version forward. Genuine status/label/assignment/message
mutation events retain their established behavior and ordering. There are no
invented changed fields, status transitions or missing descendants to block a
later real event. The existing compat_v1 history adapter reports completed local
activity truthfully as `activity_touched`; raw and default history shapes remain
unchanged.

Accepted/unknown commands reconcile their identical frozen envelope even after a
cancel request. A committed final touch can complete normally; cancellation skips
only later unattempted work. No committed effect is undone or replayed twice.

## Snapshot and migration boundary

Exports use `aut.execution.v4`. Complete v1/v2/v3 snapshots remain accepted with
their historical frozen policies. v1/v2 reject continuation or activity markers;
v3 permits its continuation marker but rejects the new activity marker. v4 can
also carry old policy-less rows. Deferred metadata and the resulting runner must
agree on policies. Unknown policy values or inconsistent receipt version/outcome
proofs fail validation rather than being rebased or silently migrated.

Old pending no-op work therefore retains its earlier unchanged-activity semantics;
only new admissions opt in. No retroactive retry or policy migration is performed.
Any later mode transition must explicitly consider these pending/unknown old
executions and use the separately approved offline transition. Mixed-old-binary
rolling upgrade and deployment readiness are not established.

## Verification and remaining gates

Actual-legacy differential fixtures inspect final state, activity advance and real
repository list order for repeated valid actions, absent labels, snooze and local
messages. Owner/recovery tests cover stable receipts, old/new snapshots, pending
siblings, unknown/cancelled/accepted checkpoints, transaction rollback, stale
manual updates, truthful history, no phantom descendants and real event ordering.
Focused race and native PostgreSQL worker contention/recovery are separate checks;
see the delivery report for commands and results.

G12's supported local new-admission activity difference is addressed by this
bounded implementation. G02/G03 conditions, G08 optional capabilities/providers,
G11 coercions/bounds and real saved definitions, G09/G16 actual consumers, and G17
broader orchestration/migration remain independent gates. No overall parity,
production acceptance or provider delivery claim follows from these tests.
