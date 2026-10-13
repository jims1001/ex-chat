# NTF local notification-state mutations (phase60)

This bounded correction applies to the existing authenticated notification
mutation routes. Notifications remain NTF-owned and scoped by account, current
user and notification ID. It introduces no foreign-table access, permission
changes, remote owner contract, provider integration or deployment.

## Atomic state and truthful errors

PATCH collects the existing `read`, `read_at` and `snoozed_until` meanings into
one account/user/ID-scoped UPDATE. The response is read inside the same local NTF
transaction. UPDATE, readback or commit failure returns no successful object;
failed readback rolls back the local UPDATE. Single read/unread/snooze/unsnooze
routes use the same transaction and response rule. No Save/upsert is used for
these mutations, so missing/deleted targets cannot be recreated.

A failed required readback after a positive UPDATE is classified as an internal
readback failure even if its underlying error is record-not-found. It returns
500 and rolls back. An initially zero-row UPDATE or genuinely absent no-op PATCH
retains 404. This distinguishes failure stages without claiming a deletion race.

A genuinely missing or out-of-scope notification returns the existing 404
`Notification not found`. Storage errors return generic 500
`Notification storage unavailable`, including single delete, read-all and both
batch-delete routes. Raw SQL/storage diagnostics are not included in mutation
responses. Existing successful notification/status/deletion envelopes remain.
Delete is still one scoped DELETE; read-all is still one scoped unread UPDATE.
No cross-store or external-effect atomicity is claimed.

## Request compatibility and validation

Snooze, PATCH and batch-delete requests must be a complete, type-valid JSON value
before any mutation. Malformed JSON, wrong field types, invalid timestamp syntax,
trailing garbage and a second JSON value return 400 `Invalid request payload`.
Unknown fields remain ignored, matching the prior decoder's behavior.

Existing valid-body semantics remain:

- Explicit `read` takes precedence over `read_at`; false clears read state.
  Non-null `read_at` without `read` marks read at the current server time; it does
  not store the caller's timestamp.
- Omitted/null PATCH fields are unchanged, including `snoozed_until: null`.
  Empty-object/null PATCH is a scoped read-only response; an empty PATCH body
  remains invalid. Unsnooze explicitly clears the snooze field.
- A future explicit snooze timestamp takes precedence. Past timestamps fall
  through to the existing duration aliases (20m/20min/20_minutes,
  3h/3_hours, tomorrow/24h/1d), case/whitespace normalization and one-hour default.
  Empty/whitespace bodies, null/empty objects and unknown durations retain the
  one-hour default. This is no new snooze policy.
- The existing batch aliases are POST `/notifications/batch_destroy` and DELETE
  `/notifications`. IDs and `only_read` intersect when both are supplied.
  Missing/empty IDs impose no ID filter. Empty/whitespace bodies and null/empty
  objects still delete all of the current user's notifications in the account.
  The documented `destroy_all` route is not added by this bounded correction.

## Verification obligations and limits

The owner fixture contains only `notifications`. Proposed SQLite/native cases
cover executed UPDATE/DELETE faults, rows-plus-error readback, combined PATCH
rollback/recovery and one-write accounting, genuine missing/deleted/out-of-scope
404, no resurrection, malformed bodies before writes, valid defaults/precedence,
read-all scope, batch intersection/defaults, and unavailable-table SQL failures.
A second fixture uses the full real router, generated authenticated user tokens,
agent/admin ownership, membership rejection, both batch aliases and local
fault/recovery assertions. PostgreSQL uses unique disposable loopback schemas.

The reviewed inventory adds 205 proposed native root/subtest paths under two
roots. Inventory and static checks are obligations, not execution evidence.
At source preparation, phase59 acceptance and parent runtime release are pending:
Go compilation, ordinary/race/native focused runs, full core/native gates and
independent final-source review are NOT RUN. Status: INCOMPLETE / UNVERIFIED.

This does not complete OPS-16's multi-device synchronization or the full NTF/OPS
module. Live push/realtime events, provider delivery, retention, pagination,
preferences, concurrent conflict policy and general read-endpoint errors remain
outside this correction. Local transaction error handling does not establish a
replay/idempotency protocol or certainty after an ambiguous network commit.

## Collaboration inventory correction

The first full core attempt on 7f61aa3 failed the strict collaboration guard:
524 current method edges versus 528 registered edges. Its failed evidence is
retained; native did not start. The bounded correction removes only five stale
OPS-to-NTF handler method edges (FindByID, MarkRead, MarkUnread, Snooze, Unsnooze)
and registers UpdateState under the existing ntf.notification.update.v1 command
and unchanged event binding. The approved registry digest is refreshed and a
positive semantic assertion is added; exact source/registry equality remains
mandatory. This registration does not assert live event emission. Fresh focused
architecture tests, complete gates and independent review are required.
