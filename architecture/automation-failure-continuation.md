# Phase13 bounded continuation after definite failure

Phase15 valid no-op activity and v4 snapshot behavior are documented in
[automation-activity-parity.md](automation-activity-parity.md). Its bounded
new-admission policy supersedes older no-op/version statements below; historical
policy-less runs retain their prior semantics.

Legacy remains the default. This stage repairs G10 working later-action outcomes
for new durable admissions. It adds no providers, operators, public failure-policy
selector, history DTO adapter, mode transition, push or deployment.

## Frozen behavior

New plans freeze `continue_definite.v1` in private runner state. Every expanded
owner step carries its zero-based original `logical_action_index`, also included
in that step's result. `action_index` remains the expanded owner-step index. The
original immutable rule version and source definitions remain unchanged.

A validly parsed but unsupported or invalid logical definition becomes one local
`validate_action` step. It fails explicitly with the unavailable-capability or
invalid-parameter reason, without an owner request. Earlier and later supported
logical actions are still eligible. Unparseable whole definitions and more than
100 source actions still fail admission. The 1000 expanded-step budget is kept;
an action that would consume space reserved for remaining definitions instead
becomes an explicit invalid-parameter result. Existing content, label, ID and
parameter restrictions are unchanged.

On a first owner attempt, structured validation, missing-resource, state-conflict,
authentication and permission rejection are definite no-effect outcomes. A failed
step is recorded with no object version, resource ID or success outcome. Remaining
steps of that same source action are `skipped/dependency_failed`; the next source
action can start from the last proven CON version. This includes all remaining
labels in a multi-label source action. Earlier CUS creation and CON message
preparation are retained as their already confirmed results, without pretending
that a cross-owner rollback occurred.

Transient dependency/transport errors and invalid owner responses remain unknown.
A missing configured dependency conservatively remains retryable. An accepted or
unknown step always reconciles its identical frozen owner request, even after
cancellation. A later authentication, permission, validation or other rejection
cannot disprove a possible earlier commit; it remains unknown. No later logical
action or sibling condition is released by such a rejection. Existing stable
receipts determine whether the earlier action actually completed.

Cancellation skips future work; it does not undo confirmed preparation or other
effects. Manual CON interleaving is never adopted: later guarded commands use the
receipt-proven version and can independently reject the same stale target. No
fresh unrelated owner version is used as a continuation base.

## Results and causal proof

The aggregate retains the existing owner-step meaning: all successful steps yield
`succeeded`; at least one failed and at least one successful local step yield
`partially_succeeded`; failures with no successful step yield `failed`. Skipped
steps never count as success. Cancellation retains `cancelled` with completed
steps visible. The first failed step supplies the terminal failure reason.

`partially_succeeded` does not assert a complete source action, provider delivery,
or even a mutation for every successful step. A CUS resolution can create a label
or merely resolve an existing one; both report `resolved` and its ID. A successful
existing-label lookup followed by rejected CON attachment is partial owner-step
progress with no attachment. A successful CON preparation followed by rejected
MSG creation retains an actual CON version increment, but no message row. These
truthful step results and logical indexes are groundwork for the separate G16
history/client adapter; they are not that adapter's completion.

Version proofs now traverse successful, failed and dependency-skipped prefixes.
Only successful owner results can advance the version, and only their validated
frozen dispatches can produce descendants or update sibling projections. Failures
and skips carry the previous proof without inventing a version. A successful CON
no-op after a failed step does not create a phantom descendant. Pending, unknown,
active or quarantined executions still block causal successors.

## Snapshot compatibility

New exports use `aut.execution.v3`. The policy is also frozen before deferred
sibling evaluation, so restoring an old pending evaluation does not opt it into
new continuation behavior. Complete v1/v2 snapshots with the old policy remain
accepted and retain their prior stop/whole-plan-failure decisions. They cannot
contain the new policy marker. The original strict compiler remains solely for
these old frozen plans and explicit compatibility fixtures; new admission uses
the bounded continuation compiler. Snapshot validation chooses the matching
compiler, verifies original logical grouping, frozen envelopes, results, terminal
aggregate and the entire causal version proof before committing restore.

There is no retroactive retry of terminal old work, new schema column, or safe
mixed-old/new binary rollout claim. G05's existing old frozen condition decisions
are preserved independently.

## Verification and limits

Differential tests invoke the unchanged actual legacy service for both created
and ordinary-updated triggers. Controlled team lookup rejection preserves later
priority/status effects, first/middle/end failures, sibling match/no-match timing,
unsupported-plus-local rules and tenant isolation. Unsupported legacy actions
that merely touch activity are not relabeled as real capability successes. Legacy
ignored-storage-error behavior, nondeterministic/lossy coercions, provider-delivery
claims and arbitrary values are intentionally not reproduced.

Recovery tests cover every label/message step position, existing-label read-only
progress, partial preparation, unsupported/invalid definitions, frozen plan edits,
dependency/unknown/lost-response/authentication, cancellation races, manual CAS
conflicts, old/new snapshot restart, failed/skipped proof validation, descendant
enumeration/replay and native PostgreSQL multi-worker contention. See the delivery
report for exact commands and results. Parameter limits, no-op activity semantics,
numeric/date/changed fields, provider execution and G16 history compatibility
remain separate gates. No production, throughput/soak, UI or external delivery
acceptance is claimed.
