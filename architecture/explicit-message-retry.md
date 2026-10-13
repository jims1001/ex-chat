# Explicit local message retry

This bounded repair covers the ordinary HTTP retry path and truthful status in the
standalone `msg.message.retry.v1` command. It does not dispatch a channel/provider
request. `AUTOMATION_EXECUTION_MODE=legacy` remains the default; this status
correction intentionally applies in both modes. It does not enable durable mode,
backfill historical imports, or change activation/rule migration gates.

## Client-visible local outcomes

The same existing undeleted failed Message is updated. Its ID, body, message type,
private flag, and creation time are unchanged. HTTP response, WS `message.updated`
and compatibility `message.created`, and `message_created` webhook carry:

- `queued`: public outgoing message accepted locally, still pending transport
- `saved`: private notes, incoming records, and activity records accepted locally

Incoming/activity retry is local persistence/acceptance, never a resend to a
provider. A saved incoming retry still follows the existing incoming/resolved
reopen behavior. The standalone command retains its outgoing-only validation and
uses queued for public outgoing, saved for private outgoing. Its unmarked status
outbox remains excluded from AUT. No entry point changes to `sent` merely by
accepting a retry. Genuine transport acknowledgements remain separate.

There is no new dispatcher in this repair: public work can remain queued until an
existing or future authorized transport workflow actually handles it. Webhook
mock success proves webhook fanout, not recipient transport delivery. Clients
must render queued/saved truthfully and must not infer sent/delivered from the
legacy message-created event name. UI transport treatment is not implemented here.

Retry does not satisfy agent reply SLA clocks. The HTTP path skips reply SLA
completion for queued/saved. MSG's SLA timeline excludes outgoing rows until their
status is sent/delivered/read, including during later periodic SLA evaluation.
It leaves incoming observations eligible. This also excludes failed outgoing
rows, which are not successful replies.

## Durable ownership and atomicity

MSG observes CON's current version and status through an owner port before its
transaction. Under a Message row lock and failed/deleted/account/conversation CAS,
MSG commits the status, one `retry_attempt` journal, and one `automation_trigger`
fact atomically. Journal failure rolls the Message back to failed. No CON call,
provider call, or external fanout is made inside that transaction.

Each accepted attempt has a UUID and a per-message increasing retry sequence.
The sequence is explicitly not a Message object version. Its deterministic event
identity is domain-separated from the CON receipt identity. A later real failure
and retry creates a new UUID/sequence; a repeated accepted durable HTTP request
returns the same local outcome without activity, automation, or fanout repetition.
Replay requires the latest immutable attempt, same outcome, and exact accepted
update timestamp. Other non-failed requests remain invalid. Legacy requests retain
the previous non-failed rejection behavior; their SQL update now uses a row lock
and failed-state CAS. The legacy retry repository needs no journal table.

CON separately locks its conversation, validates the frozen intent, and writes one
`retry_activity` receipt and activity/version mutation atomically. No unread or
participant counters change. Incoming/resolved retry reopens and clears snooze,
even if muted or contact-blocked, preserving retry-specific legacy behavior.
This intentionally differs from ordinary new incoming-message reopen eligibility.
No duplicate CON automation fact is emitted. The MSG fact owns the combined alias
batch. Outgoing/private/activity or other statuses do not invent opened aliases.

A changed CON version produces a terminal `stale` receipt without overwriting
status, snooze, activity, or version. Its AUT target is the original expected
version, never the later live version. This is a deliberate safety improvement
for manual interleaves. AUT records stale-target failure and dispatches no action.
Receipt replay validates account, message, conversation, attempt, metadata,
versions, status observation, occurrence time, and outcome. Deleted/deleting
conversations cannot be resurrected. Missing/deleted/wrong-tenant messages cancel
or quarantine pending facts without deleting the existing Message or reverting
its accepted status. Cross-owner deletion is not a distributed transaction; the
source checks existence before requesting activity and owner CAS controls CON.

## Recovery, automation, and compatibility observations

A CON outage after MSG commit does not change the successful local acceptance
response to a false rollback. The existing MSG pending-fact source drives the
idempotent retry activity port when the composed cancellable durable bridge drains
facts. Thus the existing bridge lifecycle owns recovery; no detached retry worker
or new hidden background context is added. Embedded callers can explicitly drain
the source. The standalone command does not gain this monolith compatibility
workflow.

Owner-source scanning keeps its existing fixed-horizon paging. A temporarily
unavailable retry owner is logged and skipped for that pass so unrelated ordinary
or runtime families can progress; the next bridge pass retries it. Invalid proof
is quarantined. A lost CON response reuses the same receipt on restart. A lost AUT
response reuses its frozen event/batch receipt. Acknowledgement writes only MSG
local receipts and requires the immutable attempt to have completed owner activity.

Only a receipt-verified `msg.message_status.changed.v1` envelope marked
`operation=retry`, profile `msg.retry_attempt.v1`, identity/sequence/outcome and
expected/resulting CON version is mapped to the existing `message_created`
compatibility view. Generic provider status events, ordinary unmarked standalone
retry outboxes and imports remain excluded. Public facts contain no message body
or provider response. Message conditions read the actual MSG-owned message.

Legacy invokes its message-created callback with the CON snapshot loaded before
activity/reopen. Durable retry preserves the receipt-backed pre-reopen status for
that message rule view while actual `conversation_updated`/`conversation_opened`
aliases use post-transition status. Status is the only retry-mutated field among
currently supported condition attributes. The committed CON version still drives
CAS. Each view's observation is frozen in existing AUT checkpoints and validated
against the admitted fact on replay/snapshot restore. Shared rule-ID order,
deferred sibling evaluation and causal predecessor versions remain in force.

Legacy immediate WS/webhook/push/automation fanout remains best effort following
accepted local mutation. Durable local CON/AUT recovery is stronger, but external
WS/webhook/push/SLA callbacks are not replayed in the background: no idempotent
external-fanout contract was introduced. A crash after MSG commit can therefore
lose best-effort external notification while local activity/AUT recovers. A normal
duplicate durable HTTP request does not attempt to reconstruct that fanout.

## Verification boundaries

Tests use isolated owner stores and local fakes/disabled dependencies. They cover
message input/status matrix, old callback comparison, rollback at each journal,
response-loss replay, genuine later attempts, owner outages, stale manual writes,
quarantine/deletion, cancellation, paging past blocked attempts, immutable receipt
validation, alias order, snapshot restore and standalone status behavior. Native
PostgreSQL contention tests are explicit opt-in, loopback-only, disposable-schema
fixtures with independent repository pools. No provider sends, production data,
deployment, frontend claim, or throughput/soak validation follows from these tests.
