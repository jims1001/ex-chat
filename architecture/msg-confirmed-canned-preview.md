# Phase 67: confirmed preview text to local MSG queue

`msg.message.create_from_canned_preview.v1` accepts a caller-confirmed immutable text snapshot via the existing authenticated, allowlisted module contract transport. This is a local MSG foundation for later OPS orchestration, not a completed OPS-06 workflow.

## Contract

POST `/contracts/commands/msg.message.create_from_canned_preview.v1`. Strict envelope decoding rejects unknown fields and trailing JSON. The actor must be a canonical positive signed-64 decimal `User` with `auth_source: module`; account ID is positive and at most MaxInt64. Target is `conversation`, canonical positive signed-64 decimal ID, expected_version zero. Input has exactly three string fields:

- `canned_response_id`: canonical positive decimal at most MaxInt64.
- `object_version`: canonical nonnegative decimal at most MaxInt64, including legacy `0`.
- `resolved_content`: valid UTF-8, at most 100000 raw bytes; after Go TrimSpace it must be nonempty. Stored content is the trimmed text. The raw bound is deliberately stricter than legacy MSG create.

No variables, implicit contact lookup, interpolation, HTML interpretation, alternate sender, private flag, echo ID or content-type selection. Content is authoritative caller-confirmed text, including literal placeholder delimiters. Source ID/version are **unverified hints bound into the intent digest**, not an audit record or proof that OPS preview was called. They do not establish template existence, ownership, current version or correspondence to the text. No OPS dependency is introduced. HTTP uses the existing Go JSON decoder, which repairs invalid wire UTF-8; direct invocation rejects invalid strings.

Every attempt, including receipt replay, checks current TEN membership. Direct handler entry repeats admission and requires the MSG receipt transaction. Sender equals actor, so this fresh exact-ID membership check covers both. New creation separately queries current CON state with a decimal-string ID, validates returned conversation ID/account and rejects deleting state. Missing, foreign or mismatched responses fail closed. Membership and CON checks are point-in-time reads, not distributed locks.

## Atomicity, replay and scope

New messages are fixed User/outgoing/text/nonprivate, empty echo, and local `queued`. One MSG transaction writes message, command receipt and `msg.message.created.v1` outbox event. The existing legacy MSG handler and phase65 renderer remain unchanged. There is no provider call, delivery proof, outbound attempt, UI or production deployment.

New capability opt-ins bind the complete actor, account, target, raw input and source hints in the receipt digest, attach cancellation context to the transaction and decode replay numbers precisely. Existing receipt formats and digests are unchanged. The global idempotency-key namespace is retained: a key already used for old/new/different capability conflicts, in either direction; keys do not silently cross capabilities. Same key with different actor, tenant, target, raw content or hints conflicts. A new key explicitly requests another message. IDs above 2^53 stay exact in dependencies, storage, first response and receipt replay.

Committed receipt replay requires current TEN admission, but does not requery CON or any source template and never recreates a message. A timeout may occur after commit; retry only the same key and intent to recover. Pre-commit cancellation or failure rolls back local effects. No claim of provider cancellation or cross-owner atomicity is made.

## Validation boundary

Only new and directly affected behavior is targeted: strict shapes, content boundaries, actor-bound replay, old/new key conflicts, fresh membership/revocation/restoration, exact large IDs, CON mismatches, event-write rollback, cancellation and concurrent PostgreSQL duplicate requests. New-path race tests and affected legacy MSG admission/receipt compatibility are included. No full-repository core/native gate is implied. Phase65 renderer is checked unchanged by hash, without rerunning unrelated preview tests. OPS-06 remains partial: UI and server-side current-template verification are not implemented here.
