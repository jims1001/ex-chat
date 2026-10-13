# AUT bounded local-action runner

This increment extends the lifecycle implementation with actual CON-owned status and priority changes. It is not full AUTO-01..18 or ARCH-08 acceptance. Provider execution, the existing monolith trigger path and the general automation condition engine remain separate.

## Operator entrypoint and boundaries

Run `cmd/module-server` with `MODULE_ID=AUT` and `MODULE_ENABLE_BUSINESS_CONTRACTS=true`. The server starts `RunAutomation` alongside the existing event fanout worker. Configure AUT's authenticated CON dependency and allow the AUT caller in CON. Submit the existing `aut.execution.start.v1` contract described in E-ARCH-08-aut-lifecycle.md; query `aut.execution.get.v1` for live progress. A start command receipt is an acceptance snapshot and can remain `received` on replay even after the execution finishes.

Supported queued definitions have empty conditions and up to 100 local actions. The static capability registry supports:

- `conversation.change_status` v1 / `change_status`, with `action_params.status` equal to `open`, `pending`, or `resolved` (`closed` normalizes to `resolved`)
- Status aliases `open_conversation`, `resolve_conversation`, `close_conversation`, `close`, `resolve`; aliases accept no conflicting parameters
- `conversation.change_priority` v1 / `change_priority`, with `action_params.priority` equal to `low`, `medium`, `high`, or `urgent`

Canonical capability definitions use `capability_id`, optional `capability_version: "v1"`, and `action_params`. Legacy names use `action_name`. A single-string-array parameter is also supported. Unsupported definitions, versions, conditions and external/provider/message actions fail explicitly before any action in the plan is dispatched. Snooze, assignment, labels, mute, messages, email, webhooks, delay, dynamic plugins, arbitrary commands, arbitrary conditions and Process Intent/Signal execution are not supported here.

This worker is module-runtime only. Existing `internal/service/automation_service.go` and API/monolith trigger wiring remain independent and were not replaced. No new authenticated event-ingestion adapter was added. Trigger producers must supply the committed fact, owned target/version and preserved origin to the start contract. Full UI-to-trigger-to-runner acceptance is not claimed.

## Durable execution and owner effects

Start freezes the normalized safe action plan and version in AUT's durable execution row within the original command-receipt transaction. It does not dispatch any action before that transaction commits. A worker leases one action, checkpoints `accepted`, commits, then calls the public CON command outside the AUT transaction. AUT never opens CON tables.

Each action uses the 64-character SHA-256 digest of `aut:<semantic execution hash>:<action index>` as its stable owner command ID/idempotency key (within the owner journal’s varchar(64) limit). The CON owner executes through its existing command-receipt transaction, enforces account ownership, and now honors the envelope's existing `target.expected_version` for status and priority changes. A successful response must confirm the same account, target, requested value and an advanced object version. The next action uses that returned version. Concurrent unrelated target changes are rejected rather than overwritten.

Checkpoint updates and AUT terminal events remain atomic. A process crash, lost response, or AUT checkpoint rollback after a CON commit is recovered by replaying the same owner command, including the same expected version. CON's durable receipt returns the original result rather than applying the effect twice. Expired leases may be reclaimed; old lease holders cannot overwrite the new worker's checkpoint. Only successful owner confirmation marks an action succeeded.

Unknown responses enter `waiting` with per-action `result_unknown`, retried at the configured polling cadence with a one-second not-before bound. Authentication, authorization, validation or other errors during reconciliation do not disprove an earlier committed effect and therefore remain unknown. There is no fabricated failure or success after a lost response. Operationally unresolved dependencies may remain waiting and need operator repair; no manual replay/resume UI is implemented.

Known first-attempt rejection stops remaining actions. If earlier actions already succeeded, the overall state is `partially_succeeded`; those effects are not rolled back. Unsupported plans are preflighted as a whole to avoid applying an initial local action before discovering a later external action.

## Cancellation, causality and corruption

Queued work can be cancelled immediately. Cancellation while an action is accepted/in flight sets a durable request, reconciles that action first, then skips unstarted actions. It does not reverse a completed effect. If the last action already completed, the execution succeeds. Legacy running/waiting rows without a recoverable runner plan remain noncancellable.

CON status/priority facts preserve `origin_execution_id` and the next automation depth. Feeding those facts back with that ancestry invokes the existing persisted loop guard. Automatic fact ingestion remains separate integration work.

Malformed or structurally invalid checkpoints are quarantined as `manual_action_required`, with fixed `invalid_runner_checkpoint` reason. Validation includes the static command/field/value registry, nonzero owner CAS version, action indices/statuses and bounded execution identity. A poison oldest row cannot block all later valid work. Quarantine does not assert that an unknown owner effect never occurred.

## Verification

The new tests use separate AUT and CON databases and real authenticated CON HTTP contract handlers, not success-only mocks. They inspect actual conversation status, priority, object versions, owner receipts and outbox records. Coverage includes frozen queued plans; unsupported-plan preflight; queued/in-flight cancellation; lost response and restart; lost response followed by credential rejection; checkpoint rollback after owner commit; stale-lease fencing; optimistic target races; partial failure; causal loop feedback; corrupt-checkpoint quarantine; and competing PostgreSQL workers.

No real messages, providers, payments, production accounts, push or deployment were used. See delivery test logs for exact final checks and platform limits.
