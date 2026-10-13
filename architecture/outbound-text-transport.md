# Bounded outbound text transport

## Activation and scope

`CHANNEL_OUTBOUND_ENABLED=false` is the safe default. `true` enables capture of
new explicit public text requests and starts the worker only when the server
composition supplies its lifecycle context. Normal server startup supplies that
context. Merely creating a router in a test does not start provider work.

Supported provider channels are the existing `Channel::Whatsapp` Cloud API and
`Channel::TwilioSms` SMS bindings. Existing CHN authorization/number verification
and AES-GCM-encrypted credentials are reused. No credentials are duplicated in
MSG, public inbox configuration, attempts, messages, journals or responses.

The ordinary authenticated conversation-message endpoint queues a durable
intent for these providers. The phase18 explicit retry endpoint queues a new
attempt only after durable proof of a definite, retry-safe non-delivery outcome.
A historical failed row may be adopted only through that new explicit retry
request. Existing queued rows, AUT output, campaigns, imported records and
historical receipt/journal rows are never scanned or adopted as send intents.
Disabling the flag stops dispatch; signed callbacks can still reconcile already
in-flight attempts. Re-enabling resumes only previously captured new-style
intents. No installation, upgrade, schema migration or snapshot restore opts in.

Website/WebWidget/Api channels retain their existing local real-time behavior,
including the legacy `web_widget` alias. Private notes/activity remain saved
locally. Unsupported external channels and provider media/attachments return an
explicit error. There is no media, template, bulk, campaign, email, account-change
or provider-configuration implementation in this stage.

The standalone MSG command runtime has no external transport orchestration. It
now reports queued for local public outgoing creation and saved for local
incoming/private creation, without adopting those rows for dispatch. Full
standalone RTM extraction/remote wire dispatch is separate architecture work.

## Ownership and durability

MSG owns `OutboundMessageAttempt`, its immutable text and recipient snapshot,
claim/fencing token, provider identity, evidence and message state. CHN owns its
existing encrypted connection. The message-owned orchestrator reads CHN through
its repository port and CON through `OutboundMessageTarget`; CON asks CUS through
its narrow `OutboundChannelRecipient` port. CUS requires the exact tenant,
contact, inbox and unique source relation, never a generic current phone number.
There is no foreign SQL, shared cross-owner transaction or network call inside a
DB transaction. Existing dependency/contract fingerprints remain guarded.

MSG atomically commits the message and first attempt. An optional client
`Idempotency-Key` scopes a stable intent to tenant, conversation and actor; exact
content/binding replay returns the same message without repeated fanout. Altered
content or routing conflicts. Without that header each create is a new explicit
intent. The header is NOT forwarded to either provider.

A durable compare-and-swap claim has a token and lease. Before dispatch, the
worker rechecks CON/CUS routing, block/deletion state and the CHN/config binding.
MSG then validates the unchanged message and commits dispatch-start with the
same unexpired token before HTTP. No other worker may dispatch that attempt.
Expired pre-dispatch claims may be reclaimed. Expired started claims become
unknown and are never sent again. A delayed result from the original fenced
attempt can still settle unknown. Callback evidence wins over a subsequently
lost/uncertain create response. Snapshot restore preserves every hidden field,
including body, callback URL, binding fingerprint, claim and failure evidence;
malformed/missing private checkpoint fields fail atomically. Snapshot files are
private, mode 0600, and credentials still require the separate CHN encryption key.

## Status meaning and failure visibility

- queued: durable local intent only
- provider_accepted: provider accepted creation/queued it; no carrier delivery claim
- sent: provider confirms its sent/upstream-carrier-accepted stage
- delivered: provider confirms delivery
- read: WhatsApp read confirmation; never fabricated for SMS
- unknown: dispatch may have happened but no authoritative matching result is stored
- failed: definite rejection or authoritative failure before confirmed sent
- cancelled: invalidated locally before dispatch; no provider recall claim

Confirmed sent/delivered/read progress never goes backward. A failure after sent
sets `delivery_failed: true` on the message while retaining sent progress and a
private attempt failure latch. This means carrier acceptance was confirmed but a
later delivery-failure report arrived. Delivered/read clear and supersede that
failure flag; stale failures cannot erase confirmed delivery. Client rendering
must display delivery_failed as a failure warning, not successful delivery.

Retry eligibility comes from durable attempt evidence, never status or the
public boolean alone. API acceptance and unknown do not satisfy SLA timelines.
Only the existing sent/delivered/read filter can count an outgoing reply. Existing
CreatedAt-based SLA timestamp semantics remain unchanged; this work does not
retroactively roll back earlier SLA timing when a later sent-stage failure arrives.
No old message status or historical receipt is rewritten as provider evidence.

## Exact no-automatic-resend policy

Neither adapter supplies a provider idempotency key. Local deduplication cannot
promise provider exactly-once delivery. The adapter performs one POST, disables
redirects/cookies and body replay, and never retries automatically. A local-server
response-loss test confirms one physical POST, including reused connections.

Explicit retry is permitted after local pre-send rejection or these documented
Twilio error envelopes with a matching HTTP status and numeric provider code:
20003 (400/401/403 authentication), 21211/21606/21608 (400 destination/sender/trial
validation), and 20429 (429, explicitly unprocessed). Malformed errors, other
4xx, 408, timeout/reset, partial or malformed success, mismatched identity,
oversized response and 5xx remain unknown. Current Meta error semantics could
not be freshly verified because its reference returned 429, so Meta HTTP errors
are conservatively unknown. A signed/provider-returned failed/undelivered status
is not by itself authorization to create another message and is never retry-safe.
No automatic retry/backoff loop sends either provider another POST.

## Callbacks and reconciliation

Twilio receives a per-attempt fixed HTTPS StatusCallback path:
`/api/v1/accounts/{account}/twilio/status/{attempt}`. The official Twilio Go SDK
validates the exact configured URL and every single-valued form field. Duplicate
fields/signature headers, noncanonical account/path, query strings (including a
bare `?`), unexpected provider account, sender, recipient, attempt or provider ID
are rejected. Incoming Host/forwarded headers never choose the signature URL.
Signed callbacks may bind the first provider SID after create-response loss, but
only to an already-started matching attempt. Duplicate/out-of-order callbacks
are monotonic and do not duplicate audit transitions.

WhatsApp reuses its existing raw-body X-Hub-Signature-256 validation and callback
route. All entries/changes/statuses are validated and tenant/WABA/phone-number/
recipient/provider-ID bindings are checked before state writes. This bounded
parser rejects mixed inbound/status/media envelopes rather than partially apply
them. Confirmed provider IDs work even if opaque callback data is absent.

Meta's current `biz_opaque_callback_data` echo semantics/limit were not accessible
in official reference material during implementation. A short opaque attempt ID
is sent as correlation only, but an opaque-only callback cannot bind a previously
unknown provider ID in this release. Such WhatsApp lost-response attempts remain
unknown and unsent again. There is no invented Meta GET-message-status API.

Authenticated POST
`/api/v1/accounts/{account}/conversations/{conversation}/messages/{message}/reconcile`
performs only Twilio's documented GET-by-known-SID, verifies account/from/to/SID,
and applies matching status through MSG. It never sends or searches by ambiguous
body/recipient/time matching. Missing/uncertain lookup cannot authorize resend.
No background polling or recurring billable sends are introduced.

Changing/deleting the message, contact route or connection before dispatch
cancels or blocks pending work. Changes after the dispatch check cannot recall
an in-flight provider send: the frozen original request remains authoritative.
Local delete/edit does not resurrect old content when callbacks arrive; attempt
evidence is retained for reconciliation. Connection/credential/config replacement
blocks callbacks that cannot be verified against the frozen binding; operators
must retain the original CHN configuration until in-flight reconciliation is
resolved. There is no fabricated rollback or provider cancellation.

Status/failure transitions have bounded, body-free local audit records. They do
not invent new AUT triggers. Existing message reads are authoritative; new
provider status transitions do not add a separate durable realtime fanout plane.

## Verified primary references

- Twilio Messages create/read/status resource:
  https://www.twilio.com/docs/messaging/api/message-resource
- Initial status and callback lifecycle:
  https://www.twilio.com/docs/messaging/guides/outbound-message-status-in-status-callbacks
- Callback ordering:
  https://www.twilio.com/docs/messaging/guides/track-outbound-message-status
- Callback correlation and GET-by-SID reconciliation:
  https://www.twilio.com/docs/messaging/guides/outbound-message-logging
- Official signature verification:
  https://www.twilio.com/docs/usage/webhooks/webhooks-security
- Explicit unprocessed 429 and documented rejected errors:
  https://www.twilio.com/docs/api/errors/20429
  https://www.twilio.com/docs/api/errors/20003
  https://www.twilio.com/docs/api/errors/21211
  https://www.twilio.com/docs/api/errors/21606
  https://www.twilio.com/docs/api/errors/21608
- Meta-owned Cloud API collection and status example:
  https://www.postman.com/meta/whatsapp-business-platform/folder/13382743-ba8d099d-007e-4b52-b9f2-3cf3c60e4fbc
  https://www.postman.com/meta/whatsapp-business-platform/request/p1qdvb2/status-message-sent-business-initiated
- Meta-owned message types/signature sample (SDK archived):
  https://github.com/WhatsApp/WhatsApp-Nodejs-SDK/blob/main/src/types/messages.ts
  https://github.com/fbsamples/whatsapp-api-examples/blob/main/signature-validation-with-webhooks-payloads/app.py

No current Graph version was guessed. The existing explicitly configured
WHATSAPP_API_VERSION is required and frozen in each attempt. No authenticated
provider verification, live send, live callback registration, credential setup,
push or deployment was performed. All transport tests use injected local mocks.
