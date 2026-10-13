# Data Import admission, persistence and truthful progress (phase58)

This correction is limited to the existing local Data Import REST flow. It does
not implement an independent MIG deployment, Intercom/provider import, a new
worker, restart protocol, idempotency, concurrency arbitration or cancellation
races. Message-template imports are a separate feature and are unchanged.

## Admission before work

Every DataImport creation must persist successfully before the handler starts
import execution. Failure returns the existing JSON failure envelope with HTTP
500 and `Failed to create import task`. This includes staged tasks, count-only
tasks and registration of a failed task for invalid JSON. Invalid JSON still
returns its existing HTTP 400 validation result when that failed record can be
saved. Prevalidation may perform read-only contact checks before admission.

Execution must persist `processing` before importing contacts or invoking the
existing migration service. If that write fails, the service returns the storage
error immediately and performs no import work. The empty-input `failed` save is
also checked, rather than replacing its failure with an ordinary empty-input
error. Handlers expose execution/storage failures as generic HTTP 500 messages;
raw storage diagnostics are not included in these failure responses.

Successful execution saves its final status and counts through the existing
service and handler saves. Both are checked. Task prevalidation, cancel and
discard also acknowledge success only after their mutation is saved. A failed
save cannot be disguised by a later save in the same request.

## Count-only requests and response compatibility

A default/auto-start request with no source data and positive `total_records`
still creates a task and returns HTTP 201, now with `status: pending`, the
submitted estimated total and zero processed/failed/skipped counts. No contacts
are imported and no completion is implied. Explicit staging retains its existing
`staged`/`validated` behavior. `pending` is the pre-existing model default.

Existing CSV/JSON validation, contact ownership, duplicate handling, source data,
validation and error/skip fields, downloads, HTTP envelopes, permission checks and
V1 route aliases remain. No Data Import V2 route is currently registered.

This batch deliberately retains the implementation's existing result semantics:
any successfully imported records yield `completed`, even when some rows failed
or were skipped; those counts and error/skip details remain visible. Zero
successful imports yield `failed`. It does not introduce the broader documented
`completed_with_errors` state. Empty execution returns HTTP 500 with a durable
failed task when that save succeeds. Other zero-result executions retain HTTP
201/200 with a saved failed result; HTTP success does not imply imported rows.

## Uncertain outcomes and recovery limits

The MIG task store and contact owner are not one atomic transaction. Contacts
already written remain if the final task save fails. HTTP 500 therefore does
not promise rollback or permission to retry. The persisted task can remain
`processing` with earlier counts, or can already be `completed` if only the
handler's final save fails. Read the task and actual target data before deciding
on repair. No automatic rerun is added. Existing explicit start/state guards
are unchanged and are not claimed to be a safe general restart protocol.

Tests prepare failure injection at every save boundary, real database write
errors, separate MIG/CUS stores, preserved partial-result behavior and native
PostgreSQL equivalents. Real-router regressions cover signed principals,
settings permission, cross-tenant exclusion, V1 aliases, downloads and generic
failures. The native manifest is a coverage obligation, not execution evidence.
Full ordinary/race/native and independent acceptance must be reported separately.
