# Agent invitations and owner-safe activation

## Rollout boundary

This is a backend API change. This repository does not contain the frontend
application. Invitations are disabled by default even when SMTP and FRONTEND_URL
already exist. Operators must first implement the client contract below, then set
`AGENT_INVITATIONS_ENABLED=true`, a secure trusted `FRONTEND_URL`, and a configured
mail sender. Missing/invalid configuration returns HTTP 503 before creating an
invitation. There is no temporary-password fallback. All development verification
uses explicit local mocks; no provider delivery, hosted UI or rollout is implied.

The existing self-registration/confirmation flow is not used for activation. In
particular, activation creates no personal workspace and does not depend on the
legacy confirmation token or its email audit log. SAML remains unchanged and its
unavailable endpoints are not activated by this feature.

## API and compatibility changes

- `POST /api/v1/accounts/:account_id/agents` now requests an invitation and returns
  HTTP 202 with invitation metadata, not HTTP 201 or an AccountUser/User object.
  Required: `email`. `role` defaults to `agent`; only `agent`/`administrator` are
  accepted. A same-tenant `custom_role_id` may accompany `agent` only. `name` is
  accepted for old clients but ignored; the recipient chooses their own name.
  Any supplied `password`, including null or an empty string, is rejected.
- No identity or membership exists merely because an invitation was requested.
  The bearer token and its digest are never included in administrator responses,
  invitation lists, logs, EmailLog, or the ordinary retry queue.
- `GET /api/v1/accounts/:account_id/agent_invitations` lists the newest 100 records.
  An AgentManage custom-role member can see only their own records; administrators
  can see all. `DELETE /agent_invitations/:id` revokes a link within that same scope.
- `POST /auth/invitations/preview` accepts JSON `token` and returns only the
  server-bound tenant ID/name, role/custom-role ID, expiry and accepted status,
  with Cache-Control: no-store. It grants no access and accepts no query token.
- `POST /auth/invitations/activate` accepts JSON `token`, `name`, `password` and
  `password_confirmation`. Matching passwords must be 8–72 bytes, consistent with
  password-reset limits. A successful response says `activated: true` and directs
  the recipient to ordinary sign-in; it never issues a JWT. The token supplies
  mailbox proof for creation of a new IAM identity with role `agent` and confirmed
  email, independently of the intended tenant membership role.
- If the email already has a user, activation returns HTTP 409 `sign_in_required`.
  It never sets that user's password, name, confirmation flag or global role.
- An existing user signs in normally, then posts JSON `token` and, when MFA is
  enabled, a fresh `mfa_code` to `POST /api/v1/agent_invitations/accept`. The usual
  access-token middleware is mandatory. The current IAM email must match the
  normalized intended mailbox; a pre-existing identity is also pinned by user ID.
  The mailed proof remains required even when User.ConfirmedAt is already set.
- Only tenant administrators may invite administrators/custom-role members or
  change membership roles/custom-role IDs. A custom role with AgentManage may
  invite ordinary agents and update membership availability, but cannot grant
  administrator or custom-role privileges. This intentionally closes the old
  arbitrary role-grant escalation. Only same-tenant custom roles are accepted.
- `PUT /agents/:id` rejects any supplied global `name`, `email`, or `password`
  before changing anything, including when supplied as null. Availability is
  limited to online/offline/busy. Recipients use their own profile/auth flows.
  Deleting agents modifies only TEN membership and invalidates their relevant
  invite proofs; it does not delete or change their global IAM identity.

## Client contract

The mail link is the configured FRONTEND_URL prefix followed by
`/app/auth/invitation#account_id=…&token=…`. The application must implement this
page before enabling invitations. Both token and tenant ID are in the fragment,
not the request query or Referer. The client must read and remove the fragment
with replaceState, keep proof only in transient memory, avoid analytics or
third-party resources on this flow, never put it in logs/local storage, and submit
it only in JSON to the same trusted backend over HTTPS. Do not auto-accept on GET.
Fetch preview using the JSON token and show its server-bound tenant and role
(never trust the fragment account_id for consent). Require an explicit recipient choice to activate or
sign in/accept. Preserve the proof safely in memory across an in-page sign-in/MFA
flow; if it is lost, the recipient can reopen their email. Do not ask a tenant
administrator to copy or relay the proof. A client must distinguish invitation
records from account membership, handle 202, prompt for sign-in/MFA when needed,
and display incomplete activation rather than claiming tenant access.

## Delivery and reissue

Invites expire after 24 hours. Only SHA-256 digests of random 256-bit tokens are
stored. One current row per tenant/mailbox binds token, exact recipient, optional
pre-existing user ID, role, custom-role fingerprint, issuer and issuer membership.
Every explicit repeated invite request creates a new proof and invalidates the
previous proof. Requests are synchronous with the configured sender:

- `pending_delivery`: recorded, never accepted
- `submitted`: real configured sender returned acceptance; not proof of delivery
- `captured_mock`: an explicitly configured local mock captured the message
- `delivery_unknown`: send failed or its outcome could not be established; never
  accepted, even if the provider actually delivered the link

A process/database failure can leave pending_delivery after a provider accepted
mail. This also fails closed. There is no automatic resend of the same secret.
An explicit reissue creates a fresh proof. A race with reissue/revocation can
produce a delivered but unusable older link; responses do not claim non-delivery.
An HTTP 202 with captured_mock must never be presented as real SMTP delivery.

Built-in invitation SMTP uses the shared [bounded credential-mail transport](sensitive-email-transport.md):
30 seconds total (or the caller's earlier deadline), socket cancellation and verified
TLS except for explicitly local loopback servers. DATA acceptance followed by an
unconfirmed QUIT remains `delivery_unknown`. A late nil sender result after the
context ends cannot activate an invitation.

## Atomicity, retries and authority

TEN owns invitations and memberships. Its transaction locks the account before
reading mutable invite state, rechecks active account/current issuer membership
and permissions/current custom-role fingerprint, inserts a membership and consumes
the proof together. Existing memberships are never overwritten. The three normal
repository member-creation paths use the same account serialization and reject
insertion duplicates, including when a trusted module races with acceptance.

IAM owns identities, MFA and the create-once InvitationIdentity operation binding.
New-user activation is a bounded saga: validate TEN proof, atomically create IAM
User plus binding, then revalidate and commit TEN membership. A retry reuses only
the exact operation-bound IAM user; email similarity can never adopt another
user. Competing ordinary registration wins or loses under the normalized email
uniqueness constraint; a winner from another operation requires its own login.
No cross-database transaction is claimed. If TEN fails/expires/is revoked after
IAM commits, the created identity can remain without tenant access. The response
says so. Retrying the same valid proof reconciles the same identity; an expired or
reissued proof requires sign-in and a new invite. No credentials are replaced on
retry. A retry password must match the immutable first-created password before
TEN acceptance; a different password produces sign_in_required rather than a
misleading activation success. A changed credential version or enabled MFA on a partial identity requires
ordinary authenticated acceptance instead of unauthenticated activation.

Existing-user acceptance uses current IAM identity and credential version, plus a
fresh factor whenever MFA is enabled. Generic access tokens alone do not prove
current MFA. IAM persists a five-minute receipt in its existing replay map, keyed
by a nonreversible digest of invite operation, user ID, credential version and MFA
secret. Same-operation retries reuse it without consuming a second backup code;
different invitations cannot reuse the factor. Credential-version/secret changes
invalidate reuse. IAM attestation is a bounded authorization checkpoint, not an
instantaneous distributed lock across IAM and TEN. A subsequent IAM change can
occur after attestation, as with normal authenticated requests; TEN still rechecks
its own authority at commit.

Consumed proofs may only read-reconcile their exact surviving accepted membership
ID. They never recreate a deleted membership or overwrite later role changes.
A removed/recreated issuer membership has a different ID and cannot authorize old
pending invitations. Role changes through this API revoke pending invitations
issued by that member; target custom-role edits invalidate the stored fingerprint.
Revocation and reissue make all prior proof uses invalid. Deleted/inactive tenants
fail closed. Owner recovery invalidates every restored invitation proof, including
consumed-result reconciliation; see agent-invitation-recovery.md.

## Architecture registry status

ARCH-04 remains reopened. AgentInvitationHandler is API-owned orchestration; the
existing approved dependency graph already permits API → IAM/TEN/MSG. AccountHandler
no longer creates or updates IAM identities for tenant administrators. Storage
scanning, source/method inventories, approved registry fingerprints and ownership
checks remain strict.

The registry explicitly names TEN invitation issue, delivery_record, revoke,
accept, get and list facades, plus IAM invitation activate/authorize facades.
Invitation state and authorization-receipt event names describe persisted owner
facts. These are local monolith ports and intended collaboration contracts, not
implemented independent module-server transports, emitted events, or completed
ARCH-04/ARCH-08 acceptance. No new runtime command/query/event registration is
made in internal/modruntime/business.go. MSG keeps its existing email contracts. ARCH-06 also remains reopened: the
renamed invitation issuance/revocation route mappings point to their TEN invitation
contracts, and preview is an explicit read-only POST query carrying proof in JSON.
All other write/action/idempotency classifications remain unchanged.
A future independent deployment must implement and test the owner transports,
attestation, timeouts and lifecycle semantics before exposing those capabilities.

## Verification limits

Unit, HTTP, separate-pool SQLite, real disposable PostgreSQL concurrency and owner
recovery tests cover the backend boundary. The source baseline, exact commands,
results and source hashes accompany the patch. No external mail, provider account,
production database, deployment, or frontend/browser acceptance is part of these
local checks. The explicit enable flag is an operator readiness assertion, not a
runtime proof that a remote client page exists.
