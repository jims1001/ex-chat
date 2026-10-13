# OPS shared canned-response maintenance (phase64)

This is partial OPS acceptance, alongside the existing canned-response reads and
contact-merge consumer. It does not add rendering, preview/sending, AIC/ORC
runtime wiring, independent Macro/Campaign management, or production cutover.

## Authority and command

POST `/contracts/commands/ops.canned_response.manage.v1` uses the existing module
contract envelope and authenticated allowlisted caller. Actor must be a canonical
positive decimal User with `auth_source: module`. Every call, including a receipt
replay, checks current account membership via TEN's existing permissions query.
Agents and administrators share account maintenance authority. Missing/revoked or
mismatched membership denies access; TEN failure fails closed. No foreign owner
tables are read. This is admission-time membership, not a distributed transaction
with TEN: a revocation after that check does not roll back an admitted commit.

`input.operation` is exactly `create`, `update`, or `delete`.

- Create: target type `account`, target ID exactly the envelope account's decimal
  string, no expected version (zero). Input is exactly operation, short_code,
  content. A new row starts at object_version 1.
- Update: target type `canned_response`, canonical positive decimal row ID, and
  positive integer `target.expected_version` below 2^63-1. Input is exactly
  operation, short_code, content. Both fields replace their stored values.
- Delete: same target requirements; input contains only operation. Physical
  deletion uses compare-and-swap. Its result has id, account_id, object_version
  (previous version +1), and deleted:true.

IDs fit positive signed 64-bit values. Unknown envelope/input fields, invalid
number representations and invalid target/actor shapes are rejected. A stable,
nonblank, unpadded idempotency key is required per intended operation. Retries
reuse that key; a new operation uses a new key. Receipt identity/digest includes
account, command type, target, exact input and actor. A key reused with changed
input or another actor returns409; an authorized different member can use a new
key to update the shared row. Successful replay returns the original result even
after a later update/delete, after checking current membership. IDs and versions
retain exact integer precision in first responses and receipts.

## Identity, versions and legacy compatibility

Runtime shortcode identity is Go `strings.ToLower(strings.TrimSpace(value))`,
valid UTF-8, nonempty, at most100 Unicode characters. This is lowercase mapping,
not Unicode NFC normalization or full case folding. Runtime stores that canonical
form. Validation applies to decoded JSON strings: like existing Go JSON transport,
invalid UTF-8 bytes or isolated surrogate escapes are replaced with U+FFFD before
validation. Content is nonempty decoded UTF-8 and otherwise stored exactly; no new
business-specific length limit is introduced. Existing configured transport and
bounded-read engineering limits continue to apply.

A runtime write conflicts with any same-account row whose normalized shortcode
matches, including an existing unversioned legacy row. It may retain its own
shortcode on update. Distinct accounts have separate identity scopes. Existing
legacy duplicates, whitespace, case, content and timestamps are not rewritten or
deduplicated. This does not claim global uniqueness of all historical rows.

Migration adds hidden `object_version`, NOT NULL DEFAULT0. Old rows stay0 and are
not automatically adopted: runtime update/delete of them returns409. Legacy REST
POST remains version0 with its existing input semantics and allows legacy-to-
legacy duplicates, but cannot collide with a versioned shortcode. Legacy PUT and
DELETE reject a current versioned row with409 inside the owner lock and also
predicate mutations on version0. They cannot bypass CAS or erase new events.
Existing six-field REST JSON remains unchanged because the model hides version.

Every supported writer shares an account-scoped PostgreSQL transaction advisory
lock; SQLite obtains its writer lock before any receipt/owner read. Runtime owner
mutation, command receipt and `ops.canned_response.changed.v1` outbox append commit
or roll back together. Duplicate/stale version conflicts return409, missing or
foreign row404, invalid shape400, invalid actor403, and unavailable dependencies503.
Database integrity or availability failures never become successful responses.
No memory-only lock, request serializing test pool, or cross-owner transaction is
used. Direct ad-hoc SQL is outside the supported writer contract.

Changed events use CannedResponse aggregate ID and exact resulting version. Their
payload is only id, operation and object_version, with no shortcode/content.
Single-target and fanout delivery preserve exact integer payloads for this event,
including odd values above2^53. Other event codecs retain their prior behavior.
The receipt contains the authorized response needed for replay. Events are
persisted for configured consumers; this stage does not add a renderer or sender.

## Reads and recovery

Existing get detail/match/list modes retain exactly six fields and all existing
predicates, ordering, bounds, membership checks and legacy duplicate behavior.
Opt-in get `filter.mode: detail_versioned` accepts only mode and integer id, sort
`["id"]`, page `"1"`, no cursor, and exact fields
`["id","account_id","short_code","content","created_at","updated_at","object_version"]`.
It reads text and version in one SQL projection with the existing transfer and
encoded-response limits. It reports version0 for legacy rows without granting
mutation authority. Other modes cannot request the seventh field.

New OPS canned-response snapshots use an explicit codec and required version;
missing/null/invalid versions fail restore rather than downgrading records.
Historical exact six-field arrays have an explicit legacy-only compatibility
branch. Restore preserves receipt replay and the highest deleted versioned row
ID from changed outbox history, including when no live rows remain. Sequence
adjustment is transactional and rolls back on restore failure. Restore remains
offline/quiesced, as required by the existing snapshot facility.

## Acceptance

Ordinary and native counterparts cover shared CRUD, six/seven-field read
compatibility, exact replay and actor binding, revocation, fail-closed TEN,
legacy collisions and409 protection, precise large IDs/versions, invalid shapes,
rollback at receipt/owner/outbox/finalize stages, independent-connection races,
legacy/runtime overlapping writes, one-SQL read consistency, old-schema repeated
migration and version/high-water/snapshot rollback. Every added native path is
listed in the strict native manifest; core skips remain mapped native obligations.
Completion requires frozen-source ordinary/race/native focused checks,
independent review, and full fresh core/native gates. Test timeouts, fault
assertions and skip rules are unchanged. No hosted CI/provider/deployment claim.
