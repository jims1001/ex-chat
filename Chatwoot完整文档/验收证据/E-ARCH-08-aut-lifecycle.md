# AUT module execution lifecycle: bounded implementation

Historical lifecycle-stage evidence. The subsequent bounded local-action runner is documented in [E-ARCH-08-aut-local-runner.md](E-ARCH-08-aut-local-runner.md); its supported queued actions and cancellation handshake supersede the no-runner limitations below. Other engine/integration gaps remain open.

Scope: declared `aut.execution.start.v1`, `aut.execution.cancel.v1`, execution query and `aut.automation.executed.v1`, `aut.action.failed.v1`, `aut.loop.blocked.v1`. This is not completion of AUTO-01..18, the automation engine, monolith wiring, ORC integration or provider execution. ARCH-08 remains REWORK.

## Start contract

Target: `{type: "rule", id: "<rule ID>"}`. Input: `event_id`, `event_name`, `conversation_id`, positive `target_version`; optional `rule_version`, `origin_execution_id`, nonnegative `automation_depth`, `message_id`. Message-created triggers require `message_id`. IDs/versions are positive integers. The latest immutable rule version is selected by default. Explicit older versions may replay existing executions, but cannot begin new ones.

The module verifies rule ownership and activity, CON ownership/current object version, and MSG ownership/conversation membership when a message is supplied. It never reads foreign module tables. The semantic execution key includes account, rule, frozen rule version, event ID, conversation ID and target version. Distinct command receipt keys cannot duplicate that execution. Legacy monolith execution rows retain nullable keys.

Repeated execution of a rule on the same object version is blocked even with a different event or rule version. Persisted origin ancestry catches repeated rule/object paths. Depth follows parent depth exactly; the existing threshold of 3 is preserved. An origin is required for nonzero depth. Trigger producers must preserve the causal origin; authenticating facts and origin propagation through real module producers remain integration work.

## Outcomes and limits

Empty conditions and actions form a genuine no-op and finish `succeeded`. Nonempty conditions finish `failed` with `condition_evaluator_unavailable`; nonempty actions finish `failed` with `action_capability_executor_unavailable`. Invalid frozen definitions fail explicitly. No real action is attempted. Per-action audit results contain only indices, status and fixed reason codes, never action parameters. Loop prevention finishes `loop_blocked`.

All new executions are created `received` and reach the determined terminal state in the same command-receipt transaction. Terminal execution state, command receipt and event outbox are atomic. Success/cancellation use the declared generic execution event with explicit status, failures use the declared failure event, and loop blocking uses its dedicated event. Consumers must inspect status and must not interpret every execution event as success. Execution queries expose safe reason codes only; legacy private errors/action payloads remain hidden.

Cancel target is `{type: "execution", id: "<execution ID>"}`. `received` or `evaluating` work can become `cancelled`. Repeat cancellation returns the same result without another event. Terminal work cannot be changed. `running` and `waiting` are rejected because an action could be in flight and this runtime has no cancellation acknowledgement protocol. Cancellation does not claim to reverse side effects.

## Remaining integration

No injectable synchronous provider runner was added: it would invite network side effects inside a transaction that can roll back. The next increment needs a durable claim/checkpointed action runner, registered capabilities, authoritative condition queries, per-action command idempotency/results, Process Intent/Signal support, timeout reconciliation and cancellation acknowledgement before starting nonempty definitions. No completion endpoint accepts caller-asserted success.

Use the command-handler helper boundary in `aut_lifecycle.go` when adding that runner, preserving the existing transaction and outbox guarantees. Do not replace the current explicit failures with fabricated success or indefinite waiting. Wiring monolith triggers, authenticated producer facts, deployment/migration verification on PostgreSQL and real provider verification are separate acceptance stages.

## Tests

`TestAUTExecutionLifecycleContracts` covers receipt replay, semantic replay, no-op success, unsupported-action failure, immutable versions, disabled rules, tenant/object ownership, stale versions, repeated-object and persisted-origin guards, depth threshold/reset rejection, cancellation/replay/in-flight refusal, safe event payloads, and forced outbox-failure rollback of execution and receipt. Existing AUT rule/version/query tests continue to run.
