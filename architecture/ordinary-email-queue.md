# MSG ordinary SMTP queue

This is a bounded CONS-01/03–06/12/16–17/19/22–25 and OPS-18 email slice.
It does not complete email/push, CHAN-15, inbound parsing, threading, CC, bounces,
provider reauthorization, or all REST/direct ordinary-mail producers.

## Admission and deployment

Only **new** `msg.email.send.v1` commands with the explicit normalized type
`transcript` or `notification` create `OrdinaryEmailIntent` and
`OrdinaryEmailAttempt` state. The command's existing tenant/target/dependency
checks and idempotent receipt transaction apply. EmailLog, immutable intent,
pending delivery event, and successful receipt commit together. Exact replay
returns the original admission result; use `msg.email_delivery.get.v1` for the
current outcome. Conflicting input under that key fails.

`ORDINARY_EMAIL_WORKER_ENABLED=false` is the default. Ordinary admission fails
unavailable before writes unless explicitly enabled and a global SMTP identity
is configured. Deployment requires `SMTP_ADDRESS`, valid `SMTP_PORT` (default
587), and `MAILER_SENDER_EMAIL` (or `SMTP_FROM_EMAIL`). The input From must match
that exact configured bare address. There is no fallback sender, From grant,
inbox routing, SYS query, inbox credential lookup, or automatic legacy adoption
in the external command. The separate authorized REST-only Inbox extension is
described in [inbox-transcript-queue.md](inbox-transcript-queue.md).
The frozen routing fingerprint covers host, port, username, and From. Password
rotation preserves routing; another sender/host/port/username stops existing work
with `invalid_intent_or_transport` rather than rerouting it.

Both `cmd/module-server` (MSG) and `cmd/server` start the same opt-in worker and
cancel/join it on shutdown. Only the combined server injects the CHN Inbox SMTP
capability; standalone module-server leaves Inbox queue rows untouched. The combined worker handles these managed intents;
it does not convert `EmailService.SendTranscript` or legacy notification senders.
Account and Widget REST have separate bounded producers below. A later deployment
shutdown/disable does not erase already accepted records; re-enabling processes
only their proven safe pending work. No production enablement is included here. Mixed old/new binaries are not a
supported worker rollout: quiesce old writers and workers before migration and
activation; the legacy-selector zero is only additional protection.

The sensitive reset/invitation/confirmation submission paths are unchanged.
Existing reset-password compatibility records remain unmanaged. Confirmation
continues to be rejected by this generic command. A type allowlist identifies
ordinary work; arbitrary body content is not scanned to infer a different type.

## Account transcript REST producer

`ACCOUNT_TRANSCRIPT_QUEUE_ENABLED=false` is a separate default-off opt-in for
`POST /api/v1/accounts/:account_id/conversations/:id/transcript`. The existing
account membership and `conversation_manage` middleware still run on every
request, including replay. This adds no inbox/team authorization. The existing selected recipient (explicit email, else contact),
first 200 messages including private notes, sender names and branded renderers
are preserved. Missing/unreadable source data cannot create managed work.

Eligibility is actual sender provenance: `NewEmailService` must have constructed
the selected global SMTP object from environment variables, with an explicitly
configured From. The selection function still prioritizes inbox ProviderConfig.
The constructed SMTP object must still exactly match its captured configuration;
mutating it or requiring host/port whitespace normalization is ineligible.
Any injected sender (even an SMTP instance with equal host/From), mock or fallback
From is ineligible and retains its existing direct path. Inbox-selected requests
also retain the direct path unless the separate Inbox admission flag is enabled;
that extension has its own CHN-owned route proof and never inherits global identity.
No CHN/SYS credentials are copied to MSG and no Inbox mail is silently rerouted.
This does not settle the older API-35 global-prerequisite versus inbox-only
positive-test discrepancy or repair the legacy senders' retry-identity gap.
A new eligible request needs both producer and worker opt-ins; if the worker or
identity is unavailable it fails before admission and never falls back to SMTP.
With the producer disabled, fresh requests retain existing direct behavior.

Clients must generate one `Idempotency-Key` for each intentional managed send,
retain it across timeouts/retries, and use a new key for another send. Exactly one
header is accepted, containing 8–128 ASCII letters/digits or `._:-`. The key is
scoped by account, authenticated requester, action and conversation. Its logical
input is the literal `email` request value; omitted/empty email means resolve the
contact address once. A different explicit email under the same scope returns
409. Malformed managed JSON or keys return 400. Identical content with different
keys is not deduplicated. No raw client key is persisted. Internal journal identifiers retain the full
256-bit scope using a compact encoding within PostgreSQL varchar(64) bounds.

The combined-server MSG bridge takes the existing account lifecycle fence,
checks the durable request mapping, reloads render metadata via existing owner
ports and messages via the MSG transaction, and renders once for a new accepted
request. It synchronously validates and invokes the existing `msg.email.send.v1`
handler and receipt executor in that same MSG transaction. EmailLog, private
intent, delivery event, receipt and hashed request mapping commit together. No
open transaction is passed to the HTTP timeout goroutine. The same configured
1 MiB serialized command bound is enforced without truncation, and the configured
5-second MSG deadline bounds the fenced transaction; timeout rolls it back. CON/TEN dependency
reads still use local contract transport; this is not a remote-module cutover or
a coherent snapshot across other owners. PostgreSQL account locking serializes
competing requests and cleanup; SQLite contention can require retrying the same
key. There is no persisted pre-admission body or separate private capture table.

Request metadata on the MSG intent contains a nullable unique scoped-key hash,
logical-input hash and frozen message count. A known key is resolved before
producer/worker configuration or sender eligibility, so disabling the producer,
changing inbox routing, editing messages/contact details, or changing the clock
cannot rerender or direct-send that request. A lost HTTP or commit acknowledgement
is resolved from this mapping; an unresolved failure returns 503 and instructs a
same-key retry. There is no synchronous fallback after a managed attempt.

The existing HTTP 200/success envelope keeps `email` and `messages_count`, adds
`queued: true`, `status: pending`, `email_log_id`, and `status_url`, and says
`Transcript queued`. This is the immutable admission result, including on replay;
use `GET /api/v1/accounts/:account_id/email_logs/:id` for current delivery state.
That route uses the same account membership authority as the existing EmailLog
list and projects `msg.email_delivery.get.v1` without body, recipient, From or
subject. It adds no permission and does not claim mailbox delivery.

Anonymization shares the fence with capture, cancels safe queued work and clears
body, input hash and count. It retains the scoped hash as a no-resend tombstone:
the known request returns 409 after scrub, including with the producer disabled.
Conversation cleanup deletes the request metadata with the existing private
intent, and the conversation tombstone blocks its recreation. A request whose
sources are read after a completed MSG scrub cannot recapture old MSG body text;
a scrub after capture clears that newly admitted intent in the same lifecycle.
Private capture cannot survive in an extra receipt or staging table.

MSG owner restore targets an owner-isolated schema; a combined database also
requires its referenced CON/CUS/IAM owner data to be present under the existing
restore ordering. The fixture does not establish a coherent multi-owner restore.
Account global owner snapshots use `msg.ordinary_email_intent.v2` with explicit nullable
request metadata. Strict phase38 v1 snapshots remain readable only with all
original private evidence and no request metadata; old queue rows never acquire
REST requester ownership. A new scoped command cannot be downgraded into that
legacy empty-metadata case. Restored pending/leased/submitting work still becomes
unknown; replay returns its original admission ID and cannot reactivate it.

## Widget transcript REST producer

`WIDGET_TRANSCRIPT_QUEUE_ENABLED=false` is a separate default-off flag for
`POST /api/v1/widget/conversations/transcript`. Both this flag and the ordinary
worker opt-in are required for new eligible admissions. Sender provenance uses
exactly the same original-global-SMTP selector described above, preserving inbox (unless separately opted in),
injected/custom/mock and fallback behavior. Equal SMTP tuples do not qualify.

The existing Widget revocation middleware checks every supplied visitor token
alias, including secondary query/header credentials. The handler then requires
an unexpired signed `visitor_session`, the selected inbox, a live inbox/source to
contact mapping matching signed contact and account, and exact account/inbox/contact
conversation ownership. A website token, raw source hint, email, or upload token
is insufficient. These complete checks run before admission and replay; the
application bridge also requires an authorization callback and repeats current
checks before private lookup, status and lost-commit recovery. Generic MSG email
command and query contracts alone do not validate a visitor.

The stable request scope is `widget-transcript-v1`, account, inbox, contact,
signed SourceID, conversation and the original `Idempotency-Key`. JWT bytes and
JTI are excluded, so normal session refresh preserves that mapping's request.
A new or reassigned source mapping does not inherit it. This is mapping authority,
not proof of a particular human or device. Raw keys, source IDs and tokens are
not persisted with the intent. Full 256-bit identity is retained in compact
`wdg-transcript-v1:` journal command IDs; receipt keys have a separate
`widget-transcript-v1:` namespace. The audit actor is the truthful `Contact`
with `visitor_session` provenance; no fictitious `user_id` is submitted or stored.

The recipient remains explicit email or contact-email fallback. No new verified
recipient restriction is introduced. The literal email choice is fingerprinted;
a changed choice under the same key conflicts. First admission renders/freezes
recipient, From, subject, branding, names and only the first 100 public messages
in ascending ID order, filtering private rows before applying the limit. It
never invokes Account's private-inclusive 200-message preparation. Known keys
are resolved after current authority and before configuration/sender checks.
Admission is one MSG transaction for log, intent, event, receipt and scope mapping,
under the existing account/conversation fences and command size/deadline bounds.
No extra private transcript copy is created. A failed or uncertain managed
admission never falls back to direct SMTP.

Queued HTTP 200 replies retain the immutable original admission fields even after
the worker settles: `Transcript queued`, `queued: true`, `status: pending`,
`email`, `messages_count`, `email_log_id` and `status_url`. For current state use
`GET /api/v1/widget/conversations/:id/transcript/:email_log_id`, supplying the
website token, current visitor session and original `Idempotency-Key` header.
No email body or email input is needed for status. The key selects the authorized
request hash; it never grants access. Status additionally matches exact log,
account, conversation, Widget command namespace and managed marker. Account,
ordinary contract and other visitor logs are not accessible through this route.
It projects only the body-free MSG status, with no recipient, sender, subject,
payload, key, token, source or internal routing/fence data.

Revocation or mapping removal prevents subsequent replay/status, but does not
cancel already accepted mail. Contact/inbox deletion does not add a new queue
cancellation mechanism. The worker does not reauthorize visitors. Existing
conversation deletion and account-message anonymization fences remain effective:
conversation cleanup removes queue/log state; anonymization scrubs content/input
and retains a hashed no-resend tombstone. Widget replay of that tombstone returns
409; Widget status is not found. Already submitted mail cannot be retracted.

Snapshots containing global Widget commands use `msg.ordinary_email_intent.v3`, with
exactly the existing request metadata shape and no new requester columns. v3
validates Widget's reserved hash-derived command identity, matching correlation,
100-message cap and absent log user ID. Account-only exports retain v2; strict
historical v1/v2 imports remain readable and cannot acquire Widget ownership.
Widget rows are rejected if relabeled v1/v2, including stripped request metadata.
Restore retains the scoped request mapping and quarantines pending/active work as
unknown; replay never renders or reactivates it. This remains an owner-isolated
checkpoint contract, not coherent restoration of every referenced owner. Inbox
commands instead require strict private v4 route proof; v1–v3 remain exact
historical global codecs. Retained Inbox route or requester evidence cannot be
relabeled as an older global codec. Command IDs were arbitrary for global callers,
so an Inbox-looking prefix alone neither reserves public names nor proves origin.
Proven global TransportKey and empty route evidence remain global; scrubbed legacy
records without request/route/body proof remain inert, without inferred REST
requester ownership. Completely stripping all distinguishing metadata from an
anonymized checkpoint can make it indistinguishable from legacy arbitrary-ID
history. These unsigned checkpoints do not authenticate erased provenance; no
stronger downgrade-integrity guarantee is claimed. Genuine new Inbox tombstones
retain their requester scope, and unknown/cancelled work is never reactivated.

## Persistence and observable states

MSG owns `ordinary_email_intents`, `ordinary_email_attempts`, and the
`EmailLog.managed_queue=ordinary_smtp_v1` marker. Migration creates schema only;
it never turns a historical log into new work. Immutable payload and its hash
live only in the private intent, not in the public EmailLog body, events,
receipts, status response, errors, or transport logs. New private SQL operations
use a queue-scoped silent GORM logger (including combined-server writes and owner
restore) so expanded failing/slow SQL cannot expose body or recipient. This does
not change the global logger or legacy mail policy. No SMTP password is stored
in these rows. Recipient/From/subject remain ordinary EmailLog audit fields.

| State | Meaning | Automatic continuation |
|---|---|---|
| pending | Admitted, or proven pre-submission transient retry scheduled | Eligible only when due |
| leased | One worker owns a durable bounded attempt | Expired pre-DATA lease can be reclaimed |
| submitting | Durable marker committed before SMTP DATA | Never re-submit this attempt |
| sent | Final SMTP DATA acceptance observed and outcome committed | None; does not mean mailbox delivery |
| failed | Authentication/permanent/configuration failure or attempt budget exhausted | None |
| unknown | Submission may have happened, response/save/crash was uncertain, or restored backlog is unverified | Never auto-resend; investigate the existing ID |
| cancelled | Account anonymization fenced work before submission | None |

Status query uses one joined statement for managed status, attempts and timestamps;
`status`, `delivery_status`, and `queue_state` therefore describe the same
committed version. It also returns `attempt_count`, `max_attempts=4`,
`latest_attempt_id`, a sanitized `error_code`, `automatic_retry_scheduled`, and
`manual_retry_allowed=false`. `retry_count` is max(attempt_count−1,0), and
`max_retries=0` disables the legacy selector; the queue independently allows up to
three retries via `max_attempts=4`. Crashes consuming claims count toward that bound.
Public `last_attempt_at` is the latest claim time, not a mailbox delivery time.
The existing EmailLog list/detail status mirrors the same transaction.

Both legacy manual and automatic failed-mail retries exclude every nonempty
managed marker, including failed/unknown queue entries. Managed records also persist
`max_retries=0`, so the pre-upgrade failed-only selector cannot take new work;
private restore preserves that zero despite the legacy model default. The legacy manual bounce
mutation also excludes them: this queue makes no new provider bounce claim.
No generic manual retry or cancel endpoint is added.

## Submission, recovery and cancellation

PostgreSQL row locks plus compare-and-swap predicates fence account, intent,
attempt ID, lease token and state. A 60-second lease exceeds the whole SMTP
operation's 30-second maximum. Before DATA, the worker atomically records the
submitting marker, attempt timestamp, status and event. Competing workers and
old leases cannot own that attempt. A stale worker cannot overwrite a settled
outcome. Outcome, EmailLog status, attempt and delivery event commit together.

Only proven pre-DATA transient failures retry, with 5/10/20 minute delay plus
0–30 seconds of deterministic attempt jitter. Authentication/permanent failures
stop. Context cancellation before submission can retry within the same bound.
Once the persisted submitting marker exists, uncertain completion stays unknown,
including a lost marker commit acknowledgement. Lost DATA acknowledgement,
process death after the marker, and outcome/event save failure never authorize
resend. If saving the outcome fails, the durable submitting state remains until
lease recovery records unknown. SQL errors and provider response text are not
returned to clients or worker logs.

SMTP uses verified implicit TLS for port 465 and STARTTLS elsewhere, with a
literal loopback/localhost development exception. AUTH failures do not retry;
TLS verification cannot downgrade to plaintext. Context cancellation closes the
owned socket. Final DATA 250 means accepted/submitted; a later QUIT failure does
not erase that evidence. SMTP provides no universal exactly-once, idempotency,
status-query or delivery-confirmation API.

The supported cancellation surfaces are worker context/shutdown and existing
`msg.account_messages.anonymize.v1`. Anonymization and new admission share an
account transaction fence to prevent phantom intents escaping a concurrent log
scrub. Pending/leased work is fenced and cancelled before private content is
cleared. Active submitting work causes state_conflict, with no partial scrub;
query after its bounded operation settles/expires. Already submitted mail cannot
be retracted. Expired uncertain submission becomes unknown and can then be
scrubbed. The account deletion-status query includes intent/attempt counts and
remaining private-intent count. It does not assert that external mail was erased
or that another owner has disabled future account activity.

Private mode-0600 owner snapshots use versioned codecs preserving immutable
payload, routing binding, lease and attempt evidence. Strict restore rejects
missing private state, contradictory statuses, orphan/unfinished earlier
attempts and mismatched active fences. Existing sequence repair runs before new
quarantine events and rolls back with a failed restore. Every restored pending,
leased or submitting intent becomes unknown; restored backlog never activates
by itself, even if its old checkpoint predates a later provider acceptance.
Restore requires quiesced writes and workers. Snapshots are trusted operator
backups, not provider evidence or cryptographic authenticity proofs.

Existing conversation deletion also uses the same account fence followed by the
MSG conversation tombstone. Managed transcript admission takes that tombstone
lock after CON validation, so an earlier remote grant cannot recreate deleted
conversation work. Cleanup rejects active submitting attempts; once safe, it
hard-deletes the conversation's intents/attempts with its EmailLogs and supersedes
pending email outbox facts. A stale worker loses its CAS ownership, and later
unrelated jobs remain runnable. Already accepted external mail is not retracted.
After this existing hard-delete lifecycle, the old email status ID is not found.

## Verification

Synthetic local tests cover actual command enqueue → loopback SMTP → query,
immutable replay/conflict, disabled/configuration admission, historical and
sensitive exclusion, owner/tenant isolation, sender binding, bounded transient
retry, permanent/authentication failure, pre/post-marker crash and stale worker,
lost DATA/marker acknowledgement, outcome/event rollback, cancellation/shutdown,
account scrub, concurrent admission/scrub, consistent status, and restore
quarantine/rollback. Transport tests cover TLS trust, protocol failures, MIME,
header rejection, resource deadlines and secret-safe outcomes.
Real local PostgreSQL tests run competing workers/manual retry, unknown exclusion,
crash recovery, outcome/event rollback, anonymization races and private restore.
See the exact native manifest; this document is a behavior contract, not a claim
that an unrun aggregate gate passed. No external provider mail is used.
