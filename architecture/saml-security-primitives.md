# Independent SAML security primitives

## Scope and activation boundary

The SAML verifier and IAM request/replay repository are isolated building blocks.
They do not activate the existing SAML login endpoints, which remain unavailable
(HTTP 503). There is no new HTTP/module command, query, event, handler injection,
user binding, provisioning, token issuance, setting activation or configuration
lifecycle implementation. Existing frozen collaboration and module-contract
registries are unchanged. `SAMLRequestRepository` is registered only as an IAM
storage owner, and its models are included in both combined and IAM-only migrations.

A future login integration must obtain trusted current configuration, fence its
lifecycle changes, issue a fresh AuthnRequest, securely bind the browser secret,
verify cryptographically, and atomically consume correlation/replay state before
any identity binding or authentication result. These steps are not implied by
registering persistence models or restoring a backup.

## IAM storage boundary

`SAMLAuthnRequest` retains the exact request ID, tenant and trusted configuration
version/digest, separate relay and browser-secret SHA-256 digests, creation and
expiry times, and a nullable terminal consumption timestamp. Pending lifetime is
at most five minutes. Neither bearer secret, assertion XML nor asserted subject
is persisted. Lookup requires both independently generated 256-bit secrets;
relay state alone does not establish browser correlation.

`SAMLReplay` retains independent unique assertion and response digests. Each
identity is separated by its kind and bound to the exact trusted issuer. Replay
protection is issuer-wide across tenant and configuration changes. `RetainUntil`
is a minimum retention boundary, and this primitive does not automatically purge
or recycle these records. Request consumption and both replay constraints are
committed in one IAM database transaction; database errors cannot trigger an
in-memory fallback. The repository has no cross-owner storage dependency.

All model fields use public JSON redaction. Private browser/state/configuration
hashes and replay identities are excluded from public model JSON, API payloads,
and new event payloads (none are introduced).

## Owner snapshot and recovery contract

Private mode-0600 owner snapshots use explicit, versioned codecs:

- `iam.saml_request.v1`: every request field, including hidden digests and terminal state
- `iam.saml_replay.v1`: every replay field, including both independent security identities

Restoration requires the exact IAM table set. Missing, invalidly null, unknown, differently
cased or duplicate fields, unsupported codecs, public-only JSON, duplicate keys or
identities, invalid digests, invalid request lifetimes and invalid retention
boundaries fail closed. All affected table changes and the recovery marker share
the existing restore transaction; a corrupt checkpoint or failed rebuild does
not partially change the target.

Recovery requires offline/quiesced owner writes. Restored pending requests are
always invalidated by setting `ConsumedAt` to their preserved `ExpiresAt`. This is
a deterministic terminal tombstone, not a claim that authentication completed at
that instant. Existing consumption timestamps are preserved. A fresh AuthnRequest
and new browser/relay secrets are required after recovery, even if the snapshot
is recent. An older checkpoint cannot revive a browser login consumed afterward.

Target replay rows are never deleted by IAM restore. Restored replay pairs are
merged with retained facts: an identical pair keeps the earliest creation and
latest retention boundary; a new pair is assigned a fresh local surrogate ID.
The numeric row ID is not a security identity. PostgreSQL sequences are advanced
transactionally afterward. If an assertion or response partially collides with
a different retained pair, restore fails atomically rather than dropping either
replay guard. There is no general cross-owner merge or online-recovery behavior.

This preserves known target and checkpoint replay facts; a backup cannot recover
facts absent from both. Invalidating every pending request prevents resuming a
stale snapshot login, but deployment recovery must still preserve the authoritative
replay history. No recovery step opts a SAML setting into production use.

## Verification

`internal/modruntime/saml_snapshot_test.go` checks private codec field coverage,
public JSON redaction, empty snapshots, strict/ambiguous/corrupt shapes, terminal
request preservation, restored-browser invalidation, replay retention union,
independent assertion/response uniqueness, owner isolation and atomic rollback.
Equivalent PostgreSQL restore/rollback tests use the existing isolated-schema
helper when `DB_DSN` is supplied. Database ownership/migration and runtime
inventory checks keep the IAM registrations in sync.
