# Phase 68: prepared canned-response send orchestration

This is a two-step local OPS-to-MSG contract slice. It does not complete OPS-06, a sending UI, provider dispatch, deployment or cross-owner recovery coordination. Phase 65's read-only preview, phase 66 User admission and phase 67 confirmed-text command remain unchanged. OPS owns its intents; MSG never depends on OPS.

## Common admission

Both commands use the existing authenticated, allowlisted module transport. Actor must be a canonical positive signed-64 decimal `User` with `auth_source: module`, account is positive and at most MaxInt64, target is `conversation` with a canonical positive signed-64 decimal ID and expected_version zero. Unknown envelope fields and trailing JSON are rejected. Idempotency key cannot have surrounding whitespace. Input shapes are exact. Each command binds actor into its own receipt digest, uses request context for its transaction, checks fresh TEN membership before cached receipt output and again at handler entry, and refuses direct invocation without the receipt transaction.

## Prepare, then explicitly confirm

`ops.canned_response.prepare_send.v1` input has exactly:

- `canned_response_id`: canonical positive signed-64 decimal string.
- `object_version`: canonical nonnegative signed-64 decimal string, including legacy zero.
- `variables`: explicit non-null string-valued object following the unchanged phase 65 variable rules.

Preparation invokes the registered read-only OPS preview capability with its existing exact shape and precise source ID. The single source/version read must match the requested source, account and version. Resolved content must pass the phase 67 raw-text limits: valid decoded UTF-8, at most 100000 bytes, and nonblank after TrimSpace. As with the existing contracts, Go JSON decoding repairs invalid wire UTF-8 before validation; direct invalid strings are rejected.

Preparation commits only an OPS command receipt containing the immutable typed snapshot: `intent_id`, `account_id`, `actor_id`, `conversation_id`, `canned_response_id`, `object_version`, `resolved_content`. All IDs and versions are strings. `intent_id` is the full lowercase SHA256 hex of the preparation idempotency key. No MSG request, message or business outbox event is made during preparation. Replaying a successful preparation returns the original snapshot, even if the source later changes or disappears. A new preparation at a stale source version conflicts.

`ops.canned_response.send.v1` input has exactly `intent_id` (64 lowercase hex) and `resolved_content` (the exact raw text the caller confirmed). The conversation target, account and actor must match the prepared intent. Confirmation equality is checked before the unchanged MSG TrimSpace behavior; differently padded text does not authorize a different intent.

OPS looks up its prepare receipt by `cmd-` plus the first 48 hash characters and also checks the full IdempotencyKeyHash, OPS source, prepare capability, command_receipt action, applied result, delivered status, actor and account. Strict typed snapshot decoding rejects unknown fields and trailing JSON. Snapshot IDs/version/content are revalidated, and its intent/account/actor must match the row and request. Missing or foreign intents fail closed without text disclosure; incompatible stored data is unavailable. Client-supplied source/version, sender or downstream key are not accepted.

## Independent-owner failure boundary

The prepare transaction has already committed when the separate send transaction starts. The send handler derives MSG's key from `ops.canned_response.send.v1:` plus the immutable intent ID, never from the send request's key. It calls phase 67 with the stored source/version/text and the same User actor. The generated downstream command ID fits the existing 64-character storage columns. Multiple send keys referencing one intent therefore resolve to the same MSG receipt/message/event.

The MSG reply is decoded into typed integer fields and checked for valid message ID, matching account/conversation/actor, trimmed content and queued/outgoing/text/nonprivate/nondeleted semantics before OPS records completion. Unknown transport/invalid-success responses and noncontract OPS receipt transaction failures return `result_unknown`, never a claim that MSG did not commit. Other explicit contract errors remain visible. An outer request timeout can also return `result_unknown`.

If MSG committed but OPS lost the response, was canceled, or failed its receipt update/commit, the preparation survives. Retry that same intent with the same confirmed text; it uses the same MSG key and never re-renders a changed/deleted template. There is no automatic retry. A successful OPS send receipt replays only after fresh TEN admission, without requiring template, MSG or CON availability.

The source/version checks establish a preparation-time snapshot, not current-template verification at send time. Preparation is not itself confirmation to send. There is no expiration, revocation workflow or provider success guarantee. Queued means local MSG enqueue only. Existing owner snapshots retain the prepare receipt's AfterState string; standalone OPS restore is tested, but independently rolling back/restoring OPS and MSG is not a coordinated recovery protocol. New send execution fails closed for missing or incompatible intents. Successful send-receipt replay returns the already committed result without rereading the prepare receipt.

## Verification scope

Focused tests cover independent owner stores, prepare-before-MSG visibility through another connection, strict validation and provenance, actor/account/conversation and exact confirmation, membership replay admission, precise large identifiers, snapshot preservation, lost committed response, invalid committed response, canceled request, OPS receipt rollback and PostgreSQL deferred commit failure. Native PostgreSQL tests cover concurrent prepare and same/different send keys. Race and narrow receipt/manifest compatibility checks cover affected paths only; this is not a full historical core/native pass.
