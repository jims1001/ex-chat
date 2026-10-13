# Bounded Inbox SMTP transcript queue

This extends the ordinary MSG queue only for new, explicitly opted-in Account
and Widget transcript requests. It does not repair or adopt historical EmailLog
retries, complete inbound email/CHAN-15, or authorize a production rollout.
The common lease, DATA marker, retry, status and scrub rules remain in
[ordinary-email-queue.md](ordinary-email-queue.md).

## Admission and deployment

`INBOX_TRANSCRIPT_QUEUE_ENABLED=false` is a separate default-off admission flag.
A new Account request requires both this flag and
`ACCOUNT_TRANSCRIPT_QUEUE_ENABLED=true`; a new Widget request requires both this
flag and `WIDGET_TRANSCRIPT_QUEUE_ENABLED=true`. Both also retain the ordinary
worker opt-in and its existing valid global SMTP configuration prerequisite.
This bounded extension does not resolve the older API-35 global-prerequisite
versus positive Inbox-only direct-send test discrepancy. With Inbox admission
disabled, fresh Inbox-selected requests retain their existing direct behavior,
including its retry-identity gap.

Managed selection follows the actual legacy selector branch and priority, not
equality of From addresses or SMTP tuples. Inbox `smtp_address` precedes `address`,
`smtp_username` precedes `smtp_login`, string `smtp_port` precedes positive numeric `port`,
and a missing port selects 587. Missing `smtp_enabled` means enabled. A selected
username containing `@` supplies From; otherwise the existing service From is
used. Unrelated provider fields do not define the route. Managed delivery supports
the existing PLAIN authentication and fixed ordinary SMTP transport policy;
explicit unsupported OAuth, authentication or transport overrides are ineligible.
Opted-in requests with
an invalid selected Inbox binding fail before admission, without global or
legacy direct-send fallback. Custom, injected, mock and fallback senders retain
their existing eligibility rules.

A known request scope is resolved before new producer, worker, configuration or
route checks. Replay preserves its original admission, payload, count and route;
changed email input conflicts. Account membership and `conversation_manage`
still apply to every request. Widget replay and status still require current
signed visitor authority, live Inbox/source/contact mapping and exact
conversation ownership. Account freezes the first 200 messages including private
notes; Widget freezes only the first 100 public messages. This adds no general
visitor/contact revocation cancellation policy for already accepted mail.

Turning off Inbox, Account or Widget admission does not remove the worker's
Inbox capability. Already accepted safe pending work continues on its frozen
route while the ordinary worker is enabled and the CHN adapter is installed.
Turning off the ordinary worker pauses all managed delivery. The combined
`cmd/server` injects the CHN capability independently of the admission flag.
Standalone `cmd/module-server` has no adapter: its worker can process global work
but must not claim, expire, cancel or otherwise mutate Inbox queue rows. Standalone
remote Inbox dispatch and mixed old/new binaries are unsupported. Quiesce old
writers/workers before migration and activation.

## Owner boundary and route identity

CHN owns `service.NewInboxTranscriptSMTP`, the local adapter implementing
`domain.InboxTranscriptSMTP`. Its narrow
`service.InboxTranscriptSMTPRepository.FindInboxSMTP(ctx, tenant, inbox)` port
is implemented by CHN's `repository.InboxRepository`. It resolves the current
same-tenant Inbox and fresh credentials for each safe attempt. Credential reads
and the SMTP sender stay inside that adapter. The combined runtime injects it
as `Runtime.InboxTranscriptSMTP`; MSG invokes `SendInboxTranscript` through the
local capability. This is an explicit MSG-to-CHN synchronous owner-port edge,
registered separately from remote module contracts in the architecture sources.
No CHN SMTP remote API exists and `chn.inbox.get.v1` remains credential-free.

MSG stores only private route kind, canonical decimal tenant and Inbox IDs,
Inbox incarnation (`CreatedAt`) and a versioned fingerprint, alongside its
existing immutable content and attempt evidence. Identifiers never pass through
floating point. The binding fingerprints the selected host, port, username,
effective From, fixed transport policy and owner identity; it excludes passwords
and unrelated ProviderConfig fields. A password rotation preserves the binding
and is picked up from CHN on the next safe attempt. Changed host, port, username,
From or incarnation, missing/disabled SMTP, deletion/recreation, tenant mismatch
or credential loss stops delivery before DATA without global fallback.

The current configuration is checked again at the final pre-DATA boundary.
There is no coherent transaction or snapshot spanning CHN and MSG, and no atomic
revocation promise: a configuration change after the final check cannot recall
an in-flight send. MSG owns the lease, submission marker, attempt outcome and
private transcript; CHN cannot authorize a resend after uncertain submission.

## Trusted route admission and private persistence

Only authorized Account/Widget REST bridges supply a typed internal route.
External `msg.email.send.v1` stays global-only and rejects caller-supplied route
or private route identity input before receipt replay. A caller-chosen command
prefix alone remains a legal global identifier and grants no Inbox ownership. New
`acct-inbox-v1` / `wdg-inbox-v1` command identities bind the frozen route to the
receipt input hash using an internal-only reserved identity. Historical global
command identities and transport keys retain their existing meaning; later
Inbox configuration cannot turn them into Inbox work.

Inbox snapshots use the strict private `msg.ordinary_email_intent.v4` codec.
Exact historical v1–v3 global codecs remain readable only with their original
scope and evidence. Retained Inbox request or route evidence cannot be relabeled
as an older global format. Proven nonempty global transport keys with no route
binding remain global even when their caller-chosen names resemble Inbox IDs.
Scrubbed legacy rows without request, route or body proof remain inert historical
records and gain no inferred REST requester ownership. If every distinguishing
marker is removed from an anonymized checkpoint, its origin can be indistinguishable
from such legacy history. These unsigned snapshots do not authenticate erased
provenance; a stronger anti-tampering claim would require a separate integrity
contract. Actual new Inbox tombstones retain their scope and stay non-resendable. Restored pending, leased or submitting work becomes unknown;
restoring a checkpoint never authorizes retransmission. Owner restore requires
quiesced writers/workers and the existing referenced-owner restore ordering;
it does not establish a coherent multi-owner backup.

Account anonymization scrubs private route and content while retaining the
hashed request scope as a no-resend tombstone. Conversation deletion retains
its existing fence against recreation. There is no extra private route or
content copy in receipts, events or staging records. Responses, public status,
errors, logs and MSG snapshots never receive SMTP credentials. Public status
continues to omit private route identity and transcript content. SMTP acceptance
means sent/submitted, not mailbox delivery.

## Remaining boundaries

Only proven pre-DATA transient failure permits bounded retry. Durable marker
uncertainty, response loss, stale workers, post-DATA crashes and failed outcome
saves remain unknown and cannot be manually or automatically resent. Historical
unmanaged EmailLogs lack the private route proof and are not adopted. Legacy
manual/automatic retry routing remains unresolved for those logs.

No provider/account changes, production enablement, real mailbox delivery,
client acceptance, remote Inbox SMTP contract, mixed-binary rollout, coherent
cross-owner restore or atomic configuration revocation is claimed.
