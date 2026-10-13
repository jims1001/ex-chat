# Agent invitation ownership and recovery

Agent invitations are TEN-owned. `AgentInvitation` stores only a lowercase SHA-256 digest of the emailed bearer proof, a normalized mailbox, its fixed 24-hour expiry, the inviter's membership, role binding, and any consumption/revocation facts. There is no invitation password, session, or workspace creation in this model. Invitation operations stay on the existing `AccountRepository` (TEN).

New-account activation checkpoints are IAM-owned. `InvitationIdentity` binds a domain-separated operation digest to the exact identity created by that operation. It is a create-once fact, not login authority or a tenant permission grant. Activation operations stay on the existing `UserRepository` (IAM). Neither owner's migration introduces the other owner's tables.

## Private snapshots

The versioned `ten.agent_invitation.v1` and `iam.invitation_identity.v1` codecs include every persisted field explicitly. They are used only by private mode-0600 owner snapshots. Public model redaction remains unchanged, including all hidden role/identity bindings and digests. Codec coverage tests fail if a persisted field is added without explicit handling.

Both codecs reject unknown versions or fields, missing fields (including nullable fields), null required values, duplicate/case-aliased JSON keys, trailing JSON, invalid digests/mailboxes/timestamps, invalid state/role bindings, and duplicate unique keys. A failed decode does not modify its target. Empty snapshots use explicit empty row arrays.

TEN restore terminates every restored invitation proof, regardless of delivery state, consumption state, or remaining time. It sets a deterministic revoked timestamp to that invitation's expiry whenever one is absent; existing consumed and revoked facts remain unchanged. Even read-only acceptance reconciliation must not resume from a restored proof. A non-null revocation is terminal even when its timestamp is in the future. Restoring a backup cannot re-enable any previously emailed proof. Restoring consumption facts does not recreate a membership: acceptance reconciliation must still find the exact accepted membership ID.

IAM restore preserves activation checkpoints and verifies that each user ID and mailbox match an identity in the same restored IAM database. It never creates a user, workspace, session, or membership from a checkpoint. Independent owner snapshots do not provide a coherent cross-owner checkpoint; TEN's invalidation policy is deliberate and requires a newly issued invitation after recovery.

Owner restore is offline/quiesced and atomic, including checkpoint insertion, IAM identity consistency checks, PostgreSQL sequence repair, projection rebuilding, and the completion marker. Invalid snapshots or failed rebuilds roll back all changes. Existing SAML security codecs and replay-merge policy are unchanged.

## Verification

- `TestInvitationMigrationOwnerIsolation` and its PostgreSQL equivalent verify owner-only schema and repeatable migration
- Invitation private-codec tests cover all fields, public redaction, exact JSON shape, empty snapshots, and failure without partial decode
- TEN owner tests cover every delivery state, pending invalidation, retained consumed/revoked facts, repeat restore, no fabricated memberships, and next-ID allocation
- IAM owner tests verify checkpoint preservation, no authority creation, identity consistency, and next-ID allocation
- Corruption and failed-rebuild tests verify transactional rollback, retained target rows/journal, and unchanged ID sequence

PostgreSQL equivalents are named `TestPostgresInvitation...` and use the existing explicit `DB_DSN` opt-in with isolated temporary schemas. SQLite coverage is separate from PostgreSQL execution evidence.
