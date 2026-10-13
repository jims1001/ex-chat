# ex-chat

Customer conversation and live chat support platform backend in Go.

## Overview

`ex-chat` provides a multi-tenant, real-time customer messaging and support system modeled after modern support platform standards (Chatwoot compatible). It includes support for website widgets, customer relationship management, inbox routing, agent auto-assignment, real-time WebSocket communications, canned responses, and conversation analytics.

## Features

- **Multi-Tenant Accounts**: Complete data isolation across accounts, workspaces, and teams.
- **IAM & Permissions**: Role-based access control (Administrator, Agent) and secure JWT authentication.
- **Channels & Inboxes**: Support for website live chat widget, email, and API channels.
- **Customer CRM**: Contact profiles, channel identity mapping, and custom attributes.
- **Conversations & Messaging**: Rich conversation state machine (Open, Snoozed, Resolved, Pending), incoming/outgoing messages, and private internal agent notes.
- **Real-Time Communication**: WebSocket hub providing live event streaming for agents and visitors.
- **Auto-Assignment & Routing**: Automatic round-robin conversation routing based on agent availability.
- **Productivity & Reporting**: Canned responses, labeling system, and performance reports.

## Audit execution limits

Audit export generation and integrity verification POSTs return HTTP 501 until real execution is implemented. Stored result reads are historical unverified claims, qualified by response metadata; empty integrity-list GETs never create results. See [the REST and client contract](Chatwoot完整文档/Chatwoot日志审计与变更追踪设计文档/04-日志查询API权限导出与展示.md#13-当前-rest-实现边界禁止伪造导出与验证成功). This is not completion of the isolated AUD module.

## Getting Started

### Prerequisites

- Go 1.25.4 (the exact version declared in `go.mod` for reproducible CI)
- Docker Desktop or Docker Engine with Compose (for PostgreSQL integration tests)

### Running the Server

```bash
go run ./cmd/server
```

### Authentication configuration

For production or staging, set `ENV=production` (or `staging`) and provide a
unique, cryptographically random `JWT_SECRET` of at least 32 bytes. For example,
use `openssl rand -hex 32` and store the result securely in the deployment
environment. Missing, short, default/placeholder, and trivially repeated keys
are rejected before database initialization or server startup. Never reuse a
published example as a signing key.

In `development`, `dev`, or `test` mode, an unset `JWT_SECRET` generates a random
ephemeral key. Sessions and signed links then become invalid after each restart;
configure a persistent random key when needed. The default environment is
`development`; production deployments must explicitly set `ENV`.

`JWT_EXPIRATION_HOURS` defaults to 72 and must be an integer from 1 through 720.
Malformed or out-of-range values stop startup instead of silently falling back.
For Google sign-in, set `GOOGLE_CLIENT_ID` to the application’s expected OAuth
client ID; sign-in is disabled when it is missing.

### Agent invitations

Agent creation now uses recipient-controlled activation, never an administrator-
chosen default password. The backend invitation feature is disabled by default.
Before setting `AGENT_INVITATIONS_ENABLED=true`, implement the configured frontend
invitation page and mail setup described in
[the API and rollout contract](architecture/agent-invitations.md). Existing clients
must handle HTTP 202 invitation metadata instead of immediate membership creation.

### Bound assistant lookup

The documented conversation `inbox_assistant` route returns only assistant
`id`/`name`, or null. Existing conversation/inbox `assistant` aliases retain their
envelopes and unbound 404, but now omit full assistant configuration and prompts.
See the [consumer and client contract](architecture/aic-bound-assistant-read.md)
for the deliberate payload narrowing and shared AIC owner read. This does not
complete independent AIC deployment or generation acceptance.

### Running Tests

```bash
make test
```

`go test ./...` intentionally does not discover every integration test in this
workspace. Use the Make target above or the isolated script:

```bash
./scripts/test.sh
```

Run the backend suite with local PostgreSQL 16 available for DSN-based tests,
while also verifying that the local Redis container starts and passes its
authenticated health check:

```bash
make test-docker
```

The Docker test target uses its own Compose project, ports, and disposable data
volumes. It cleans them up when the test finishes and does not touch development
environment data.

Most backend tests still use their own SQLite fixtures. This older Docker target
does not enable every PostgreSQL opt-in; use the strict `ci-postgres` gate below
for all currently named PostgreSQL regressions.

Additional concurrency and stability checks:

```bash
make test-race
make test-stress STRESS_COUNT=10
make test-soak SOAK_DURATION=10m
```

The Docker services bind only to localhost. For manual startup, copy
`.env.example` to `.env`, replace every placeholder secret, then run:

```bash
docker compose up -d --wait
```

### Local and CI backend gates

Run the same core checks used by `.github/workflows/backend-ci.yml`:

```bash
make ci
```

This requires Bash, Python 3, the exact Go version in `go.mod`, a C compiler,
and `CGO_ENABLED=1`. It verifies downloaded modules, runs `go vet`, builds
`server`, `module-server` and `module-migrate` with `-trimpath`, runs ordinary
and race-enabled backend tests, and explicitly checks the staged module-isolation
acceptance test. Package discovery stays limited to `cmd`, `internal`, `pkg`
and `test`. It does not recurse into prototype dependencies. Core checks clear
inherited PostgreSQL test settings and leave PostgreSQL-only tests for the
separate required gate. The legacy Docker smoke test, which otherwise tries a
fallback local database, is also run only by that PostgreSQL gate.

For existing **disposable** loopback PostgreSQL and Redis test services:

```bash
export EX_CHAT_CI_DISPOSABLE_DB=1
export DB_DSN='host=127.0.0.1 port=55433 user=postgres password=ex-chat-local-test-postgres dbname=ex_chat sslmode=disable connect_timeout=5'
export REDIS_ADDR='127.0.0.1:6380'
export REDIS_PASSWORD='ex-chat-local-test-redis' # Omit only for a passwordless disposable Redis
make ci-postgres
```

Use these example credentials only with disposable local services you control.
This gate does not start or stop services. It refuses a missing acknowledgment,
missing DSN, non-loopback target or an administrative default database. Use a
libpq keyword DSN, not a URL: several existing regressions append `search_path`.
Tests create/drop isolated schemas; the legacy Docker smoke test also writes to
the specified database's public schema. Never use a development database whose
contents matter, staging or production, even through a local tunnel.

The PostgreSQL gate enables the authentication, automation, payment, channel,
history and SAML opt-ins as well as DB_DSN-based module/Copilot tests. Its reviewed
`scripts/ci/coverage-manifest.json` requires 161 native roots and 2760 exact
root/subtest results, including `NativePG`, the snooze `Postgres` branch and all
26 module-migration `postgres` cases. Conventional roots run together under the
race detector; the two nested branches run with exact, segment-anchored selectors.
Missing cases, new unreviewed native children and selected skips fail the gate.
New `Postgres`/`PostgreSQL`/`NativePG` roots require an explicit manifest update.
`CI_POSTGRES_EXTRA_PATTERN` can add top-level tests but never remove required
cases; extras must also pass without skips. Redis is checked with PING (and AUTH
when configured); this is service readiness, not a Redis-backed feature claim.

Core tests use exhaustive deterministic batches, ordinary and race discovery,
and streamed Go JSON evidence. Benchmarks are excluded just as in ordinary
`go test`; Tests, runnable Examples, fuzz seeds and zero-test package compilation
are included. Every started child and package must finish, and every expected
case must have a terminal. Core skips are allowed only for exact native/isolation
manifest cases. The final core report maps those obligations; it does not claim
the separate native job ran. Both workflow jobs are required for full gate coverage.

Concurrency flags default to two Go packages and four parallel tests per package;
`CI_GO_PARALLELISM` and `CI_TEST_PARALLELISM` may be set from 1 to 32. Each invocation
keeps `-count=1`, the 20-minute per-package timeout and the existing ordinary/race
modes. Core batching runs one package at a time, at most 12 root cases per batch;
the two known slow cases run alone. Native conventional cases remain grouped.
Logs, JSON evidence and temporary binaries go to a fresh ignored `tmp/ci-<mode>.*`
directory. Partial output survives an interrupted runner, but partial batches
never count as passed. CI always starts fresh; no hosted cache supplies results.

For an interrupted local core run, explicitly trust and resume your own evidence
on the same machine and in the same environment:

```bash
./scripts/ci.sh core --resume tmp/ci-core.ABC123
```

Only completed, successful ordinary/race batches can be reused after their JSON
is revalidated. Source bytes/modes, separate test inventories, exact flags,
compiler/tools and inherited environment identity must still match. Module
verification, vet, builds and isolation run again; native always starts fresh.
Changed or failed evidence requires diagnosis and a fresh run. These checkpoints
are not signed or safe to import from another person, CI artifact, checkout or
machine. A recovered run establishes complete component coverage, not a claim
that every component ran uninterrupted in that invocation. See
[`scripts/ci/README.md`](scripts/ci/README.md) for the manifest/update and recovery
contract, exclusions and limits.

The gates do not load `.env`, install software or change module dependencies.
They set `GOWORK=off` and reject local module replacements outside the repository
checkpoint contract. Environment values, database passwords and DSNs are not
written to checkpoint metadata.

The workflow has separate core and PostgreSQL/Redis jobs on Ubuntu 24.04, reads
the compiler version from `go.mod`, uses only `contents: read`, disables persisted
checkout credentials and references official checkout/setup-go actions by full
commit SHA. The action pins were checked against the official
[checkout v7.0.1](https://github.com/actions/checkout/releases/tag/v7.0.1) and
[setup-go v7.0.0](https://github.com/actions/setup-go/releases/tag/v7.0.0) releases.
Service images use explicit PostgreSQL 16.15 and Redis 7.4.11 patch/Alpine tags;
these tags and the hosted runner image can still receive upstream rebuilds.
The workflow uses only disposable local fixture credentials, exposes service
ports on runner loopback and has no publish or deployment step. Its Redis
service is passwordless inside that disposable job only. Existing development
Compose authentication remains required.

Adding this workflow is not evidence that hosted CI has run. A passing gate also
does not establish frontend/browser/mobile acceptance, a real provider delivery,
production readiness, high-load/72-hour soak targets or full ARCH-04/ARCH-08
completion. No application Dockerfile or release deployment is introduced here:
runtime topology, persistent uploads, migrations, secrets, health checks,
backup/rollback and deployment destinations need a separately agreed design.

### Frontend prototype checks

```bash
cd Chatwoot客服系统界面原型
npm test
```

### Module schema preparation (ARCH-08 work in progress)

The runtime inventory is in `architecture/module_runtime.json`. It covers all
26 logical modules and records their existing storage models, declared
dependencies, default U1–U5 units, and stop/recovery policies. It is a draft;
the current server still starts all modules in one process with one database.

To prepare a **separate** module database, explicitly select its target:

```bash
go run ./cmd/module-migrate -module MSG -sqlite-path /path/to/isolated-message.db
# For PostgreSQL, supply MODULE_DB_DSN securely in the environment:
go run ./cmd/module-migrate -module MSG -postgres
```

This creates only that module's registered tables and local journal. Association
migration is disabled so GORM cannot implicitly create other owners' tables.
It does not migrate existing business records, implement public module calls,
or start an independently working business service. The combined server's
migration behavior is unchanged. API, MED and ORC currently have no registered
persistent domain model in this migration list; their local journal alone does
not establish runtime readiness.

Run the independent-store acceptance gate separately:

```bash
make test-module-isolation
```

The staged gate now passes for message creation with separate CON/MSG databases:
MSG records an atomic local change, invokes a CON-owned activity command, and
replays a pending command after a simulated CON outage. It also checks CON
mark-unread against a separate MSG store and MSG sender enrichment against
separate IAM/CUS stores. These are in-process owner ports, not deployed public
module APIs. A source inventory remains in
`scripts/audit-module-storage.py`; other repository operations still access
foreign-owned tables. Conversation deletion now uses a durable CON deletion
marker and retryable commands against each owner store; those commands remain
in-process adapters. Conversation detail reads now hydrate owner data through
CHN/CUS/IAM/TEN ports; conversation lists and label filters use the same owner
boundary. The staged gate also covers label relation validation through CUS
and attachment listing through MSG. BIL order updates validate conversation
references through CON. Ordinary `make test`, `make test-docker`,
and this staged gate do not establish ARCH-08 completion. Independent module startup, all
business paths, failure handling, scaling, upgrade, backup, rollback, and
topology acceptance still need implementation and verification.

Current evidence and the next repair order are recorded in
[ARCH-08 current evidence](Chatwoot完整文档/验收证据/E-ARCH-08-v30.md).

### Data Import durability

Local Data Import creation now requires a durable task before execution and a
saved processing state before contact writes. Failed task saves return generic
HTTP 500 responses instead of claiming successful execution or discard.
Count-only requests keep HTTP 201 but return pending with zero processed rows.
Final-save failures can leave already-imported contacts in place; do not retry
blindly. See [the current contract](architecture/data-import-durability.md).

### Authentication security compatibility

- Existing access JWTs issued before purpose separation are deliberately invalid;
  users must sign in again. MFA challenges now expire after five minutes and
  cannot authorize API, upload-download, or agent WebSocket requests. Drain all
  pre-fix server instances during rollout (or coordinate signing-key rotation):
  old validators ignore purpose and must not remain serving MFA login traffic.
- Google Identity Services login requires `GOOGLE_CLIENT_ID`, an HTTPS-capable
  server connection to Google's certificate endpoint, and a POST to
  `/omniauth/google_oauth2/callback` with `credential` and matching
  `g_csrf_token` cookie/form values. GET credential callbacks are rejected.
  RS256 signatures, issuer, audience, expiry, subject, issued-at and verified email
  are checked. Existing email-based linking is restricted to Gmail/Workspace
  identities for which Google is authoritative. Third-party email identities
  require a future explicit account-linking flow. Local MFA is still required.
- SAML settings can be maintained, but login initiation and callbacks return
  HTTP 503. SAML is intentionally unavailable until a complete trusted service
  provider validates XML signatures and assertion references, time conditions,
  audience/recipient, request correlation and replay. Parsed XML and signature
  tags are never proof of identity. Do not bypass this guard to enable SSO.
- Mock Google tokens and unsigned SAML fixtures are negative-test inputs only.

### Distributed MFA login protection

MFA login now uses the shared IAM database transaction, not process-local
counters, to allow at most five verification attempts per user in a fixed
five-minute window. Successful attempts also count. Fresh challenges and switching
between inline password+OTP and two-step login do not reset the limit. The HTTP
layer also rate-limits `/auth/mfa/verify` as defense in depth.

Each transaction acquires the MFA profile's write lock before reading it,
records attempts, rejects previously accepted TOTP time steps and successful
challenge IDs, and removes a matching backup code atomically. Saving MFA
configuration cannot restore old backup codes or overwrite replay/limit state.
Storage errors never create a session. Recovery codes cannot enroll MFA;
enrollment requires the authenticator's TOTP.

The existing startup AutoMigrate adds these IAM-owned `mfa_profiles` columns:
`verification_window_started_at`, `verification_attempts`, `last_totp_step`,
`used_challenges`, and `auth_version`. Back up and rehearse this migration before
production rollout; all serving instances must use the same writable IAM store
and reasonably synchronized clocks. Do not run old and new login handlers
simultaneously. SQLite requires a suitable busy timeout for concurrent writers;
lock contention/storage failure returns an error rather than bypassing checks.
PostgreSQL uses the same write-before-read transaction pattern. Native PostgreSQL
16.15 transaction/concurrency and existing-row upgrade checks have now passed,
alongside file-backed SQLite tests.

MFA settings are now status-only on GET. POST `/api/v1/profile/mfa/setup`
requires `current_password` and returns a new pending secret only for a disabled
factor. Enabling requires that password plus the pending TOTP. Regenerating
backup codes or disabling requires the current password and a fresh TOTP or
unused backup code; factor consumption and the setting change are atomic.
Passwordless provider accounts fail closed until provider reauthentication is
implemented. Password-reset delivery is implemented below; real sender configuration is still required.

### Visitor token compatibility and remaining boundaries

Visitor session and upload-capability JWTs now have distinct purposes and strict
HS256, expiry, issued-at, token ID, and inbox/contact/source identity checks.
Old untyped visitor JWTs must be refreshed through widget bootstrap. A negotiated
upload capability can authorize its bound upload only; it is not a session.
Visitor WebSocket subscriptions require a valid scoped session.

Protected widget and public contact/conversation/message APIs now require scoped
signed sessions. Bootstrap creates a new random anonymous identity unless a
valid session resumes its existing contact; raw source IDs, email or identifier
hints no longer recover an existing contact. Clients must persist the returned
source ID and visitor token. Mandatory identity HMAC checks apply to every
identifier-writing contact path.

Transcript delivery verifies contact/inbox/conversation ownership before reading
public messages. New uploads use durable contact-scoped filenames; downloads
verify current ownership or exact legacy attachment/message/conversation metadata.
Unprovable ownership, private/deleted records, revoked credentials, noncanonical
paths, symlinks and hardlinks are rejected. Old unbound files may need a controlled
migration or an authorized per-file signed URL; there is no inbox-wide fallback.

CSAT permits current scoped visitor sessions, current account-member access
sessions, or the intentional random-v4 conversation UUID survey-only capability.
Per-file signed URLs are also bearer capabilities; protect them as secrets.
Revocation lookup errors fail closed on protected HTTP/file/new-WebSocket
handshakes. Existing open WebSocket connections are not proactively terminated
by a later revocation or token expiry; session-lifecycle hardening remains future
work. Real IdP integration, frontend client migration, and production verification
are not established by local tests.

### Password reset and call-recording business flows

Password reset now calls the configured EmailService sender. `FRONTEND_URL` is
required (HTTPS, or loopback HTTP for local development), and SMTP/sender settings
must be usable. Configuration errors return 503 before account lookup; eligible,
unknown, provider-only and delivery-failure requests otherwise receive the same
generic response without claiming successful delivery. The frontend link uses
`/app/auth/password/edit?reset_password_token=...`, the Chatwoot-compatible route.
Only a token digest is stored. A link becomes usable after provider acceptance,
expires after one hour, and is consumed atomically with the password update,
sibling invalidation, session deletion and `users.auth_version` increment. MFA
remains enabled. API, file, CSAT and new WebSocket authentication and pending MFA
proofs reject pre-reset user versions; existing open WebSockets are not forcibly
closed. Stale profile/confirmation saves cannot restore old credentials. Request
logs redact reset query credentials; raw reset links are not persisted in email
audit/retry content. SMTP ambiguity requires a fresh reset request.

Confirmation, reset and invitation SMTP share a [30-second bounded transport](architecture/sensitive-email-transport.md)
with verified TLS required outside explicit loopback development hosts. Both the
DATA acknowledgement and QUIT must complete before the deadline; cancellation,
unknown outcomes and late success leave the proof inactive. Ordinary mail retains
its existing transport and retry behavior.

Startup migration adds nullable `password_reset_tokens.issued_at` and non-null
`users.auth_version` defaulting to zero. Legacy reset links intentionally stop
working. PUT `/auth/password` accepts `token` or `reset_password_token` (conflicting
values rejected), an 8–72-byte password, and optional matching confirmation.
A passwordless provider account cannot gain a local password through this flow.

Multipart call recordings now save actual validated audio/video bytes (up to
25 MiB) into exclusive tenant-owned `account_<id>/recordings/` paths and update
call metadata only after the write. Failed metadata updates clean up the new
file; old recordings are preserved. Tenant/call/contact/inbox/conversation checks
apply. Status updates cannot overwrite a newly persisted recording reference.
Explicit HTTP(S) provider URL input remains metadata only: no remote bytes are
downloaded and provider availability is not claimed. Local file durability,
cleanup and concurrent update regressions are tested; power-loss consistency,
external provider behavior and real SMTP inbox delivery are separate deployment
checks.

Native-cloud verification additionally passed PostgreSQL 16.15 connection/CRUD
and all isolated-module repeatable migrations, with Redis 7.4.11 responding to
PING. These checks used loopback-only disposable native services, not Docker or
production. Reset/MFA transaction, replay, settings, rollback and legacy-row upgrade checks
have additionally passed on native PostgreSQL with independent connections.

### Verified top-ups, real Copilot completions, and AUT lifecycle scope

One-time USD top-ups use the real Stripe-compatible Checkout adapter. Configure
`STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET`, `PAYMENT_SUCCESS_URL`,
`PAYMENT_CANCEL_URL`, and an exact comma-separated HTTPS
`PAYMENT_ALLOWED_RETURN_URLS` allowlist. Supply a stable 8–128 character
`Idempotency-Key` on retries. Prices come from the server catalog in integer
minor units; credit units are separate. No billing or entitlement is granted
until the signed event and provider-retrieved paid session match the persisted
intent. BIL and ENT commit their own durable receipts/journals; unacknowledged
verified grants retry at server startup and each minute using the graceful-shutdown
context. Unconfigured checkout returns 503. Paid recurring subscription selection
and generic subscription checkout remain unavailable. No monthly one-time plan
product was introduced. Manual credit adjustments require the existing live
platform-superadmin policy; tenant administrators retain reads but cannot self-
credit through POST /billing.

The two new models PaymentCheckoutIntent and PaymentWebhookReceipt belong to
BIL and are included in module migrations. The optional native PostgreSQL test
is selected with PAYMENT_TEST_POSTGRES_DSN. Local provider tests validate protocol
and signatures, not a real Stripe sandbox/live payment. Recurring subscriptions,
renewal/cancellation, refunds, disputes, proration, multiple currencies and a
billing portal are not implemented; old unverified billing data is not reconciled.

Copilot thread messages now invoke the configured active assistant's OpenAI or
Gemini adapter, persist actual replies and provider usage atomically, and reject
unconfigured/heuristic fallback generation. Missing quota rows receive existing
default limits; exhausted limits reject before calls and racing quota commits
cannot persist beyond the account budget. Latest conversation context and prior
thread turns are ordered correctly. Parent-first locking serializes generation,
clear and deletion. Upstream provider spend is not reserved/refunded for racing,
failed or cancelled calls, and no new periodic quota-reset scheduler is provided.
Tests use local transports, not paid provider calls. See the delivered Copilot
contract notes for client limits and error statuses.

AUT module commands persist execution identity, frozen rule version,
receipt/outbox state and bounded cancellation. The AUT module server now runs
queued local status and priority changes through authenticated CON commands,
with durable leases, stable receipts, optimistic versions and crash recovery.
Phase10 adds legacy condition evaluation and bounded local action parity; unsupported actions fail before any effect. Unknown
owner outcomes remain waiting for reconciliation, and corrupt checkpoints are
quarantined without blocking later work. Cancellation skips future actions and
does not undo completed changes.

Enable the AUT module server business contracts and configure its authenticated
CON dependency. Submit `aut.execution.start.v1` and query
`aut.execution.get.v1` for progress. The opt-in ordinary monolith bridge preserves
legacy as the default until condition/action parity is verified. Phase9 adds
receipt-proven rule-ID ordering, atomic routed creation and inbound reopen, and
restartable initial/off-hours local-message workflows. An application coordinator
owns cross-module driving; unrelated manual mutations still fail owner CAS.
Versioned private AUT snapshots preserve checkpoints and causal links; offline
AUT/CON restore, receipt replay and PostgreSQL sequence rollback are tested.
Old incomplete AUT snapshot codecs are rejected, and coordinated live cross-owner
backup consistency is not claimed. Phase10 adds AND/OR conditions, assignment, labels, mute/snooze, queued public
messages and saved private notes. Evaluations and action envelopes are frozen,
owner receipts and deletion fences prevent duplicate or orphan effects. Declared
trigger/cascade coverage is extended in phase11. Phase12 defers later sibling
condition decisions until receipt-proven predecessor completion, preserving the
legacy shared-conversation versus storage-label distinction. Private snapshot v2
persists that decision; v1 snapshots keep their already-frozen decisions.
Default cutover and operational acceptance remain pending.
Read [the bounded monolith bridge contract](architecture/automation-monolith-bridge.md)
before enabling it. Read [condition parity](architecture/automation-conditions-parity.md)
and [local action parity and limits](architecture/automation-actions-parity.md).
[Trigger semantics and causal message cascades](architecture/automation-trigger-parity.md)
describe the phase11 extension; this is not full legacy compatibility or rollout approval.
[History/API compatibility](architecture/automation-history-compatibility.md) preserves existing raw wire types and adds explicit `view=compat_v1` plus cursor paging; clients must opt in before relying on logical action results.
[Valid no-op activity](architecture/automation-activity-parity.md) restores activity/sorting for supported new local admissions using frozen policy and receipt-proven +1 effects, without activity-only descendants. Old pending policies remain unchanged; activity introduced snapshot v4, and explicitly adopted numeric profiles now use v5 while preserving older semantics.
[Snooze expiry](architecture/automation-snooze-expiry.md) now captures guarded transitions and starts only with an explicit cancellable server lifecycle. [Explicit local retry](architecture/explicit-message-retry.md) keeps the existing message and records queued/saved acceptance with owner receipts, not transport delivery. Historical imports remain silent.
Frontend acceptance and provider delivery remain open. Local queued messages do
not claim delivery; action-produced MSG events re-enter AUT only through the proved, bounded causal-family intake. See
[the local runner contract](Chatwoot完整文档/验收证据/E-ARCH-08-aut-local-runner.md).
ARCH-04/ARCH-08 are not declared complete by these checks.

### Verified inbound channels (phase7 bounded subset)

WhatsApp Cloud API and Twilio SMS setup/ingress now fail closed unless configured.
This replaces the earlier placeholder authorization and unsigned inbox-1 writes.
The setup endpoints require `inbox_manage` in the router. Provider setup only
exchanges a supplied Meta signup code and reads phone ownership; it does not
register a number, subscribe an app, send messages or change provider resources.
A setup response means an encrypted local mapping was saved, not that provider
webhook subscriptions or outbound delivery are ready.

Operator configuration (see `.env.example`):

- `CHANNEL_PUBLIC_BASE_URL`: exact externally visible HTTPS origin, without a path
  or query. Callback paths are `/api/v1/accounts/{id}/whatsapp/callback` and
  `/api/v1/accounts/{id}/twilio/callback`. Do not append query parameters. Forwarded
  headers and request Host are not used to construct Twilio's signature URL
- `CHANNEL_CREDENTIAL_KEY`: Base64 of 32 random bytes from a secure generator.
  Keep it in the deployment secret manager and independently backed up. AES-256-GCM
  uses randomized nonces and authenticates tenant/provider/recipient context.
  Losing/changing the key makes persisted tokens unusable. Key rotation is not
  implemented in this bounded release
- For WhatsApp also set `WHATSAPP_APP_ID`, `WHATSAPP_APP_SECRET`, an unpredictable
  `WHATSAPP_VERIFY_TOKEN` (at least 16 characters), and an explicitly chosen
  `WHATSAPP_API_VERSION`. Set `WHATSAPP_REDIRECT_URI` only if the initiating OAuth
  flow uses it, exactly matching that flow. No automatic latest-version default
- WhatsApp setup body: `code`, `waba_id`, `phone_number_id`, optional `inbox_name`.
  It exchanges the code at Meta and verifies phone membership in the WABA. This
  currently supports the first 100 returned phone records only; a missing claimed
  phone fails closed. Provider-supplied pagination URLs are never followed
- Twilio setup body: `inbox_name`, `account_sid`, `auth_token`, `phone_number`,
  optional `medium` (only `sms`). Only an exact SMS-capable owned IncomingPhoneNumber
  is accepted. Twilio WhatsApp, alphanumeric senders, short codes, Messaging Service
  pool routing, and secondary tokens before promotion are outside this subset

Ingress security and delivery semantics:

- WhatsApp authenticates the exact bounded raw body with App Secret HMAC-SHA256
  before parsing. GET verification compares the configured separate verification
  token and requires an active, decryptable mapping for that tenant
- Twilio form callbacks use the official `twilio-go/client` v1.31.2 validator and
  the configured canonical URL. Every single-valued POST field participates;
  duplicate form keys, URL query parameters and other content types are rejected
- A verified provider account plus recipient must resolve to exactly one configured
  same-tenant inbox. Caller-provided tenant IDs alone never authorize an inbound write
- Only inbound text is supported. Unsupported media/status/error-only or mixed
  WhatsApp envelopes return 422 before any text writes; keep unsupported events in
  an upstream retry/dead-letter workflow until the corresponding feature exists
- Payloads are bounded at 256 KiB, 100 WhatsApp messages and 16 KiB per text. Unknown
  fields can remain signed. Invalid signatures/recipient mismatches write nothing
- CUS owns durable per-inbox sender identity and ContactInbox mapping; generic
  visitor-supplied Contact.identifier values cannot claim an inbound sender
- CON reuses open/pending/snoozed sessions, creates a new session after resolution,
  and serializes inbound display-ID allocation across senders within each account.
  The older non-channel conversation creation allocator is unchanged
- MSG commits the provider-message receipt, message and local journal atomically.
  Stable receipts reject changed-content replay and survive restarts and multiple
  instances. MSG→CON activity follows the existing durable journal reconciliation
  path. Failure before the message/receipt commit gets a non-2xx retry response;
  stages already committed by a different owner are idempotently reused. After a
  durable MSG commit, a temporarily unavailable CON activity update may be
  acknowledged while its pending journal is retried by the server reconciler.
  There is no global cross-owner transaction
- Deleted message receipts remain as tombstones to prevent resurrection. Inbox
  deletion/account revocation clear ciphertext and raw provider-account/recipient
  fields, retain a SHA-256 recipient reservation plus historical numeric IDs, and
  permanently block create-only rebinding. This prevents captured app-wide Meta
  signatures from moving old events into a different tenant. Recipient transfer,
  reauthorization and identity merges require a future explicit lifecycle. Merging
  a contact with a verified channel identity currently returns 409 before owner
  reassignments; normal non-channel merges are unchanged
- Private owner snapshots preserve ciphertext, identity/session keys and receipts;
  they still require the independently retained encryption key. Public JSON never
  returns encrypted/plaintext credentials. Request/query logs redact channel
  verification tokens; provider errors/bodies and setup bodies are not logged or
  returned. Configure any reverse-proxy or outbound HTTP tracing to redact tokens,
  OAuth codes and Meta's documented secret-bearing exchange query
- Local channel health reports `configured_not_probed`; it does not invent provider
  quality ratings, messaging limits, current token validity or outbound delivery

Facebook/Instagram GET/POST callbacks (both public and account aliases) now return
503 and perform no writes. Their previous unsigned tenant/inbox fallback was unsafe.
They remain unavailable until verified recipient bindings and signatures are added.
Unrelated call signaling/CSAT and legacy independent-runtime provider-config features
are not provider implementations and were not broadened by this repair.

Protocol references verified 2026-10-06:
[Twilio webhook security](https://www.twilio.com/docs/usage/webhooks/webhooks-security),
[official Go validator v1.31.2](https://github.com/twilio/twilio-go/blob/v1.31.2/client/request_validator.go),
[Twilio IncomingPhoneNumber](https://www.twilio.com/docs/phone-numbers/api/incomingphonenumber-resource),
[Meta sample code exchange](https://github.com/fbsamples/business-messaging-sample-tech-provider-app/blob/main/app/api/beUtils.ts),
[Meta phone-number endpoint](https://www.postman.com/meta/whatsapp-business-platform/request/o84xigu/phone-numbers),
[official WhatsApp webhook SDK](https://github.com/WhatsApp/WhatsApp-Nodejs-SDK/blob/main/src/api/webhooks.ts).

Validation uses local HTTP mocks and disposable SQLite/PostgreSQL owner stores,
never production accounts, real provider credentials, sends, subscriptions or deployments.
`CHANNEL_TEST_POSTGRES_DSN` explicitly enables the disposable concurrent-ingress test;
never point this variable at a production database.

### Explicit numeric custom-condition profile (phase17)

[Numeric adoption and limits](architecture/automation-numeric-conditions.md) documents
`numeric_custom.v1`, exact bounded number comparisons, CUS catalog ownership,
legacy inactive-only preparation, frozen snapshot compatibility, and remaining gates.
Legacy remains default; saved rules require explicit adoption.

### Outbound provider text transport (phase19)

[Outbound text activation, status meanings and recovery limits](architecture/outbound-text-transport.md)
documents opt-in WhatsApp Cloud API/Twilio SMS transport, durable attempts, signed
callbacks and explicit reconciliation. The default is disabled; enabling it never
adopts historical queued rows. Local webchat behavior remains unchanged.

### SYS public-branding configuration snapshot (phase26)

[Public configuration query and bounds](architecture/sys-public-configuration.md)
documents the authenticated `sys.configuration.get.v1` runtime query. It returns
only the ten documented persisted public-branding string settings, with a fixed
request shape, bounded values/data, and no writes or privileged variant. Existing
SuperAdmin HTTP behavior and full private owner backups remain unchanged. This
is a partial SYS query slice; private REST redaction, privileged SYS authority,
and complete independent SYS runtime acceptance remain open.

### Private saved-filter REST compatibility (phase25)

[REST privacy and migration boundaries](architecture/rest-private-saved-filters.md)
limits all saved-filter CRUD to the authenticated current user, with no admin
cross-user bypass. Existing owned version-0 filters remain usable. Unowned rows
are preserved but hidden. REST retains its query-string DTO and rejects mutations
of runtime-versioned rows with 409, preserving SRH CAS/event/receipt guarantees.
Full REST-to-independent-SRH adoption remains a separate migration stage.

### Private SRH saved-filter runtime (phase24)

[Saved-filter contracts and REST writer boundary](architecture/srh-saved-filters.md)
documents private current-user CRUD, TEN membership checks before cached replay,
exact JSON preservation, versioned writes, atomic change/outbox capture, and owner
snapshot recovery. This is a partial independent SRH surface; search/index work
remains open. Full REST adoption is separate; phase25 closes REST privacy and
blocks legacy writes to versioned runtime rows. Version-0 rows are not adopted.

### Stored RPT reporting-event query (phase31)

[Stored event query, pagination and recovery limits](architecture/rpt-stored-reporting-events.md)
documents authenticated owner-local reads with exact name/conversation filters,
a fixed eleven-field DTO and bounded stateless cursor pages. Sparse pages can
continue with no results. Opaque metadata, zero values and zero timestamps survive
owner snapshot restore. This is a partial RPT read slice; generation, metrics,
exports and full independent runtime acceptance remain pending.

Call metadata GETs now enforce current account roles and documented conversation
visibility: ordinary members see their own handled calls only while current
Inbox/Team scope and custom-role constraints permit access. Administrators and
report-authorized members retain account-wide metadata.
[Call GET authorization and historical-reference limits](architecture/call-metadata-read-authorization.md)
describes the exact routes, unchanged response fields and separate mutation,
recording-byte and signed-capability disclosure boundaries.

### Shared OPS canned-response read (phase33)

`ops.canned_response.get.v1` reads persisted account-shared canned responses
through explicit detail, list/search and match shapes. Every read requires a
canonical module User actor and current TEN membership. The six-field projection,
keyset pagination and SQL/encoded-byte bounds preserve existing shortcode/content
matching, including legacy duplicates and wildcard behavior. See the
[bounded read contract](architecture/ops-canned-response-read.md).
The existing contact-merge consumer remains available. Full OPS management,
rendering/sending and runtime adoption by monolith AIC/ORC callers remain pending.
The narrow REST repairs reject a shortcode emptied by the existing trim and make
an update of a concurrently deleted row return 404 without recreating it.

### CSAT local configuration and provider boundary

[CSAT configuration behavior and limits](architecture/csat-local-configuration.md)
preserves non-WhatsApp local save/read forms and presentation defaults, while
reporting invalid input, tenant lookup and storage failures truthfully. WhatsApp
CSAT template provisioning/status remains explicitly unavailable with no provider
effects; local configuration never establishes Meta approval or survey delivery.

### Durable ordinary SMTP mail

New explicit transcript/notification `msg.email.send.v1` commands can use the
MSG-owned ordinary queue with `ORDINARY_EMAIL_WORKER_ENABLED=true` and an exact
configured global SMTP sender. Default-off/invalid configuration rejects admission.
Both server binaries run the same fenced worker; old logs and sensitive credential
mail are not adopted. Account transcript REST has a separate default-off
`ACCOUNT_TRANSCRIPT_QUEUE_ENABLED=true` producer for the actual explicit global
environment SMTP sender. It requires a scoped `Idempotency-Key`, freezes rendering
at durable admission, and returns HTTP 200 with `Transcript queued`, `email_log_id`
and a body-free `/email_logs/:id` status URL. Repeating that key returns its original
admission even after source/configuration changes. Custom/mock/fallback senders
retain the legacy path. Widget has a
separate default-off `WIDGET_TRANSCRIPT_QUEUE_ENABLED=true` producer: current
signed visitor sessions, live inbox/source/contact mapping and exact conversation
ownership are required on admission, replay and status. It freezes only the first
100 public messages. Its body-free `/widget/conversations/:id/transcript/:email_log_id`
status route also requires the original `Idempotency-Key`; refreshed sessions for
the same mapping retain access. Notification producers remain outside this REST adoption. SMTP acceptance is `sent`, not
`delivered`; uncertain submission becomes `unknown` and never automatically
resends. See [the queue, status and recovery contract](architecture/ordinary-email-queue.md)
for enablement, retries, private snapshots and account anonymization limits.

[Inbox transcript routing](architecture/inbox-transcript-queue.md) adds a separate
default-off `INBOX_TRANSCRIPT_QUEUE_ENABLED=true` admission flag. It requires
the matching Account/Widget flag plus the existing worker/global SMTP configuration.
The combined server injects a CHN-owned local adapter that reloads fresh credentials
and preserves the selected Inbox route across safe retries; MSG stores no credentials.
Route changes fail before DATA without global fallback. Standalone module-server
leaves Inbox rows untouched. Disabling a producer leaves accepted work on its
frozen route; disabling the worker pauses all mail. Historical global intents
remain global, and historical unmanaged EmailLogs retain their unresolved retry
gap. The older Inbox-only direct behavior and global-prerequisite discrepancy
remain outside this opt-in. No production activation, mixed-binary rollout or
coherent cross-owner restore is claimed.

### Customer initialization and hosted billing management

No-plan `POST /subscription` and `POST /checkout` support bounded customer
initialization and safe hosted billing management. Explicit BIL currency
selection is required and locked before provider work, including concurrent
first selection. These operations do not charge, activate paid subscriptions,
change balances or grant entitlements. Historical currency fields are preserved;
new billing responses expose selected currency and required/options fields.
All provider settings default unavailable. See the
[API, operator and recovery contract](architecture/billing-customer-portal.md).


### Fixed-offset report count calendars (phase53)

The existing V1/V2 account report roots now accept exact numeric-hour
`timezone_offset` values for conversation/incoming/outgoing record counts in
hour/day/month/year buckets. +5.5, +5.75 and -3.5 change calendar boundaries and
UTC Unix labels while preserving the same absolute half-open interval and count
totals. Nonintegral-second or unsafe arithmetic inputs fail explicitly; fixed
offsets have no DST or geographic range inference. Owner SQL still receives UTC
bounds, with the same 400-bucket limit and SQLite/PostgreSQL representation rules.
See [count calendar contract](architecture/rpt-daily-count-series.md). Weekly,
IANA, additional dimensions/metrics, summary and traffic semantics are unchanged.

### Campaign prerequisite-read preflight (phase59)

Existing campaign dispatch now plans its complete required audience/dedup reads
before side effects. Read failure returns a recognizable error and generic trigger
HTTP 500; optional campaigns do not overturn committed conversation creation.
Selector, ordering and downstream write/status behavior remain unchanged. See
[contract and limits](architecture/campaign-prerequisite-preflight.md). This is
INCOMPLETE / UNVERIFIED until final-source core/native gates and review complete;
it does not establish full OPS or ARCH-08 acceptance.

### Notification mutation reliability (phase60)

Existing notification PATCH and read/unread/snooze/unsnooze mutations use one
NTF account/user/ID-scoped update with transactional response readback. Storage
failures return generic HTTP 500, true missing/out-of-scope records remain 404,
and malformed nonempty snooze/batch JSON cannot trigger default mutations.
Existing valid/empty-body semantics, permissions and successful envelopes remain.
See [the local mutation contract](architecture/ntf-notification-mutations.md).
Phase60 remains INCOMPLETE / UNVERIFIED until its own complete gates and review;
its source inventory is not an executed result or full NTF acceptance.

### Phase64: shared canned-response maintenance

OPS now registers `ops.canned_response.manage.v1` with current TEN membership before receipt replay, version1 creation, CAS update/delete, normalized shortcode conflict checks, and atomic owner/receipt/changed-outbox writes. Legacy version0 rows remain unadopted; old REST cannot change versioned rows. Existing six-field reads remain; explicit `detail_versioned` returns version in one bounded SQL read. See [the contract](architecture/ops-canned-response-management.md). This remains partial OPS, without rendering/sending, AIC/ORC extensions, production cutover or external provider calls.

## Phase 65: read-only canned-response variable preview

OPS adds `ops.canned_response.preview.v1`: explicit string variables, strict nonrecursive `{{name}}` rendering, fresh TEN membership, one-SQL source/version reads, bounded text-only output, and visible rendering errors. See [the preview contract](architecture/ops-canned-response-preview.md). This point-in-time preview grants no sending authority; existing six/seven-field reads and management remain compatible. OPS and OPS-06 remain partial, with UI integration and separately authorized MSG sending outside this stage.

## Phase 66: fresh User membership for local MSG creation

`msg.message.create.v1` checks current User actor membership before cached receipt output and at direct handler entry. Existing independent sender checks, service actor behavior, historical receipts and local queued/saved semantics remain. See [the admission boundary](architecture/msg-message-user-admission.md). This is defense in depth for trusted module actor context, with no provider dispatch, send UI or completed OPS sending workflow.

## Phase 67: confirmed preview text to local MSG queue

`msg.message.create_from_canned_preview.v1` accepts explicit confirmed text, fixes the sender to the current User actor, and atomically writes one local queued message, actor-bound receipt and created event. Source ID/version are unverified hints participating in intent binding; this entry does not call OPS or verify a current template. Fresh TEN admission precedes replay, and exact decimal IDs remain precise. See [the bounded contract](architecture/msg-confirmed-canned-preview.md). Validation is targeted to new and affected paths, not a full core/native pass. OPS-06 remains partial; no UI or provider sending is added.

## Phase 68: explicit OPS prepared-intent sending

OPS now exposes `ops.canned_response.prepare_send.v1` and `ops.canned_response.send.v1`: prepare commits a verified preview/version snapshot, then send requires its exact confirmed text and calls phase 67 with a fixed intent-derived MSG key. Lost responses or OPS rollback can retry the same stored intent without re-rendering or creating another message. See [the contract and recovery limits](architecture/ops-canned-response-send.md). This remains local queued orchestration only, with no UI, provider dispatch or full OPS acceptance. Validation is focused on new and affected paths.
