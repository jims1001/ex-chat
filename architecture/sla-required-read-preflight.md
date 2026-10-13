# SLA required-source preflight and truthful read failures (phase61)

This bounded correction prevents a failed required read from becoming an empty
message history, missing Inbox configuration or a successful partial deadline.
It does not change metric eligibility, first-customer start, NRT turn grouping,
resolution history, thresholds, holidays, policy selection or snapshot policy.

## Evaluation boundary

Before `EnsureAppliedSLA` or any evaluation mutation, RPT reads its policy and
existing breach counts, CHN supplies the Inbox when no valid hydrated Inbox was
provided, MSG supplies one complete successful timeline, and an existing Applied
SLA and its historical events are read. A source error, including rows plus an
error, is returned without application/event/breach/CON timing mutations for
that conversation. Healthy evaluation still populates the caller's Inbox as before; population is
deferred until required reads succeed, so a failed preflight does not partially
hydrate the caller. An already valid hydrated Inbox keeps its existing reuse
semantics.

FRT, NRT and NRT message metadata use that same timeline. A timely human reply
at 30 seconds remains timely with a 60-second threshold even when evaluated ten
minutes later. A CHN outage never becomes wall-clock business-hours evaluation.
An actual successful missing Inbox or empty timeline retains its existing
fallback. No policy and a nil conversation remain successful no-ops. Missing
configured required owner ports return errors instead of invented empty facts.
Existing stored breach events remain historical facts; this is no repair for
false breaches recorded by earlier versions.

This preflight covers decision facts, not transactional write/readback failure
handling inside the existing repository mutations. Post-write failure recovery,
RPT/CON write atomicity and concurrent breach deduplication are not established.
There is no cross-owner transaction, migration, route or provider addition.

## Read and processing contracts

`GetConversationSLADeadlines` and the standalone `EvaluateNextResponse` now have
an explicit error result. All existing Go callers are updated. A deadline
failure returns no partial deadlines, breach flags or selected policy. Detail
and V1/V2 list handlers use generic 500 responses without data/payload or raw
source diagnostics; late failures in a list do not publish earlier items. An
Applied SLA lookup failure is no longer swallowed, while a genuinely missing
application and conversation retain their existing success/404 semantics.

`EvaluateAccountSLAs` continues other conversations, retaining their independent
successful effects and returned breaches, but joins errors for failed
conversations. All four process aliases return generic 500 `Failed to process
SLAs` on any such failure. An unconfigured process service returns generic 503
`SLA service unavailable`; neither condition claims `processed: true`. Scheduler
account failures are logged without raw source error text. There is no account
atomicity or automatic rollback/retry promise.

The explicit ApplySLA route still performs its existing primary policy assignment
and application creation before optional evaluation. Those already-authorized
primary writes are not claimed to be preflighted or atomic by this correction;
failed optional evaluation must not invent additional breach facts.

Already committed message creation and conversation status changes keep their
existing success when optional SLA evaluation fails. This phase changes neither
those operations nor their retry or durability contracts.

## Verification obligations

Proposed isolated RPT/CON/CHN/MSG SQLite and native PostgreSQL regressions cover
empty/partial owner errors before any writes (including existing applications
and policy switch risk), policy/breach/application/event errors, deadline error
values, business-hours outage/recovery, one timeline, historical events,
healthy/missing/eligibility compatibility, native SQL table outages, account
partial independence and owner/tenant isolation. Actual-router tests cover both
detail aliases, V1/V2 lists, early and later row-plus-error reads, every process
alias, unconfigured service, authorization, missing/empty cases and successful
saved operations with failed optional evaluation.

The proposed inventory adds 135 exact native root/subtest obligations under two
roots (current total 2648 / 157). Static preparation and inventory are not runtime
evidence. At preparation, phase60 acceptance and explicit parent runtime release
are pending. Compilation, baseline negative proof, focused ordinary/race/native
runs, complete core/native gates and independent final-source review are NOT RUN.
Status: INCOMPLETE / UNVERIFIED. No full RPT, SLA or ARCH-08 completion is claimed.


## Corrected phase60 base and contract registry

This phase was statically replayed onto corrected phase60 commit
`0f951900b20670110f9370362e47e98708fe4ecf`; the earlier source/history checkpoints
remain preserved. Phase60 acceptance and runtime release are still pending.
The SLA method names are unchanged: adding checked error results introduces no
new source-method key or public contract. The corrected collaboration registry
and its exact SHA-256 remain byte-identical to phase60, including UpdateState.
The strict guard retains exact source/registry equality and all old assertions,
with positive assertions added for the existing SLA read/evaluate contracts and
CON timing writer. The new RPT-owned facts helper is explicitly scan-scoped.
Static key comparison is not execution of the Go architecture tests.
