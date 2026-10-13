# Ordinary email confirmation: bounded proof and recovery

## Existing business policy stays in place

Ordinary signup still creates the administrator user, initial workspace and administrator membership, and returns its registration access token. Sign-in and that registration token are not newly gated on confirmed email. No confirmation-required configuration exists in this backend. This change does not add one or silently auto-confirm users when SMTP is absent. Invitation activation is an independent flow and remains unchanged.

Signup now reports `confirmation_status` independently of account creation: `submitted` means the configured sender accepted submission, `captured_mock` identifies an explicit local test/development mock, `unavailable` means no usable send was initiated, `delivery_unknown` means the send outcome is uncertain, and `inactive` means a successful send could not activate its matching current proof. None means mailbox delivery. The built-in confirmation SMTP request is bounded by a 30-second context, socket deadlines and cancellation; no detached send goroutine is started. Explicitly injected custom EmailSender implementations must honor the context contract.

Anonymous resend responses say only `Confirmation request processed`, whether the account is absent, already confirmed, a send succeeds or a send outcome is unknown. Deployment configuration failure returns 503 before looking up a mailbox. Authenticated callers also see their own `confirmation_status`. Clients must stop interpreting HTTP 200 as proof of delivered email.

## IAM proof lifecycle

- New proofs use 32 random bytes encoded as 64 lowercase hexadecimal characters; the User row stores only a domain-separated, prefixed SHA-256 digest
- The proof binds the exact existing user ID, current email and authentication version, preparation timestamp, 24-hour immutable deadline and successful-submission activation timestamp
- Preparation replaces the previous digest under the IAM user lock. Resend therefore immediately invalidates the previous link, even if the replacement later fails. No old link is restored
- A proof is pending until the exact generation receives a trustworthy configured-sender success. Unknown outcomes, canceled requests and storage errors leave it unusable
- A later resend, confirmation, email change, credential reset, expiry or removed identity prevents stale send completion from activating proof
- Activation and consumption serialize on the same user row in SQLite and PostgreSQL. Consumption rechecks state and expiry under the lock, sets `confirmed_at` and clears all proof fields in one transaction. Failures roll back the entire transaction
- The returned identity is the snapshot verified under that lock. It is never reloaded after commit to issue a session using a newer credential version
- Ordinary UserRepository profile saves omit confirmation lifecycle fields, preventing a stale profile from restoring a consumed digest or clearing confirmation
- A current enabled MFA profile yields the existing short-lived MFA challenge (206), never an access token; no-MFA users retain confirmation auto-login. MFA lookup/signing failure asks the user to sign in and never issues access

No raw proof is returned by signup or resend. Existing access tokens and required MFA challenges remain part of the established authentication response contract.

## Mail and logging

Confirmation uses the existing configured EmailSender abstraction, strict sender/address checks, and sensitive-message error redaction. Built-in confirmation SMTP requires verified TLS for non-loopback servers, including deployments without SMTP authentication; a missing STARTTLS capability fails before MAIL/RCPT/DATA. Plaintext is permitted only for an explicitly configured loopback IP or exact localhost development server. Arbitrary DNS names resolving to loopback do not qualify. Password reset and agent invitation now share this [bounded credential-mail transport](sensitive-email-transport.md); ordinary SMTP retains its existing path. Built-in SMTP provider errors cannot be copied into application logs. The mail body is passed directly to the sender; no new confirmation EmailLog or retry item is created. Exact and batch ordinary email retries reject legacy confirmation rows. The isolated `msg.email.send.v1` generic queue also rejects `email_type=confirmation` (case/whitespace normalized) before receipt replay or persistence, and repeats the guard in its business handler. Dedicated direct confirmation submission is the only supported confirmation-mail path; transcript, reset_password and notification queue behavior is unchanged. The restriction is recorded in module_runtime.json and tested against actual routing/handler behavior.

The existing configured URL precedence remains FRONTEND_URL, then APP_URL, then http://localhost:3000. An explicit invalid URL fails closed rather than falling back. HTTPS is required except loopback development. The existing `/auth/confirmation?confirmation_token=...` route/query remains compatible with deployed clients. HTTP application logging removes all query input on confirmation paths and redacts confirmation_token elsewhere, including malformed query input. Confirmation responses set `Cache-Control: no-store` and `Referrer-Policy: no-referrer`. Deployment operators must also redact these paths at reverse proxies/APM; the repository cannot erase external access logs, browser history or old delivered email. This repair does not ship or claim frontend acceptance.

## Upgrade, legacy invalidation and recovery

Both combined AutoMigrate and owner-specific migration paths run security cleanup after adding columns. IAM invalidates old raw/unbound/indefinite User proof values rather than translating them into active proof. MSG scrubs legacy confirmation EmailLog HTML, text, provider errors, bounce reason and subject, disables retries and clears retry scheduling. IAM never touches MSG storage in isolated migration, and MSG never creates IAM tables. Failure aborts migration/startup; cleanup is transactional and repeatable. It leaves valid new pending/active proofs and unrelated email logs intact.

Owner recovery also fails closed: IAM User snapshots omit confirmation proof and binding fields, so imported rows cannot reactivate an old link. MSG snapshots scrub confirmation bodies/errors on both export and import, including legacy snapshot files; the same restore transaction explicitly disables retries despite database defaults. Neither operation changes invitation/SAML checkpoint handling or ordinary email content.

Every pre-upgrade link becomes invalid. Users request a fresh resend; no account, workspace, membership, existing registration token or password is deleted or reset by migration. An unknown/inactive post-upgrade attempt likewise requires explicit resend; the system never automatically replays a secret-bearing mail. A successfully consumed link remains single-use even if its HTTP response is lost: use ordinary sign-in afterward.

Do not run mixed old/new application binaries against the same store. Before upgrade, stop old workers and requests, back up under existing access controls, migrate IAM/MSG, then start only the patched binaries. Rolling back to the vulnerable binary or restoring raw-token backups reintroduces the old acceptance rule and is unsupported. Recover through the patched binary and idempotent cleanup, followed by explicit resend. Scrubbing live rows does not erase old backups or external provider/proxy records; apply the operator's existing retention controls to them.

## Boundaries and verification

This phase touches IAM User proof lifecycle, API orchestration and MSG confirmation mail only. It adds no domain model, no owner or dependency edge, and no external route. The exact new IAM activation and MSG preflight facade methods are declared in collaboration_contracts.json, with existing strict ownership, dependency, contract and route tests retained. These facade mappings do not establish independent module-server confirmation commands or emitted events; ARCH-04/ARCH-08 acceptance remains staged. No OAuth/SAML redesign, global confirmation/login policy, invitation changes, live mail/provider calls, deployments or frontend acceptance is implied.
