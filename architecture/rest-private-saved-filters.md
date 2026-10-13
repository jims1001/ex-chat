# Private saved-filter REST compatibility (phase25)

The existing `/api/v1/accounts/{account_id}/custom_filters` routes now implement
current-user private filters, including for administrators. This closes the REST
privacy defect independently of full REST-to-independent-SRH adoption. It does
not claim completion of that deployment or of SRH search/indexing.

## Authentication and scope

The routes still require the existing JWT authentication and current TEN account
membership middleware. The handler also requires a positive authenticated user
and matching user, account and membership context, and a canonical positive
account/target ID. Headers, query parameters and body ownership/version/ID fields
cannot override these identities. No administrator cross-user exception exists.

Every repository lookup or mutation includes account ID and user ID; detail and
writes also include the filter ID. Other owners' and other accounts' rows, and
rows with user_id=0, are excluded from private lists and details. Detail and
update return the same 404 as an absent row. Delete retains the existing 200
`{"success":true,"data":{"deleted":true}}` for absent or inaccessible targets,
without touching them or revealing whether they exist. Nonmembers are rejected
by the account middleware before the handler.

## Existing client contract

POST still returns 201, GET/PUT/PATCH return 200, and the existing success/data
wrapper and CustomFilter fields are retained. `query` in the REST response is a
string, including when the request supplies an object. The independent runtime
continues to return query as an object under its separate contract.

POST accepts a nonblank name (at most 255 Unicode characters), a filter_type of
conversation/contact/report, and a nonblank string or JSON-object query. String
criteria remain opaque and are trimmed as before; they need not contain JSON.
Object criteria are compacted without converting numbers through floating point.
Nested arrays, null values, unknown criteria and exact large numbers are retained.
Top-level null/arrays/scalars and multiple concatenated JSON values are rejected
with a generic 400. These checks enforce the documented request forms; clients
that relied on undocumented scalar criteria must send a string or object.

PUT and PATCH remain partial updates. Omitted fields are preserved; blank name
and filter_type remain unchanged as before. An explicitly supplied query must
have one of the accepted forms. Empty updates succeed without inventing a data
change. Lists preserve descending ID order, optional exact filter_type matching,
and an empty array for no matching owned filters. Saved criteria are not executed
or validated against a search evaluator by these endpoints.

## Explicit writer separation

Owned pre-existing version-0 rows remain visible and fully usable through REST.
New REST rows continue to start at version 0; they are not silently adopted into
the runtime. The SRH runtime still creates version-1 rows and excludes version 0.
REST may list/read the current owner's versioned rows, but PUT/PATCH/DELETE on
such rows return 409 with `Custom filter requires the versioned SRH command`.
There is no fallback legacy write. That check occurs after owner-scoped lookup,
so it cannot reveal another owner's runtime row.

Legacy updates and deletes lock the scoped row in a local transaction and guard
the SQL mutation with object_version=0. Updates write only explicitly supplied
fields, preventing stale copies from overwriting another partial update. The
response reload and mutation commit together; failed local writes/readback roll
back. PostgreSQL row locking serializes these operations. Same-field REST writes
retain last-writer-wins semantics; REST has no client CAS or idempotency receipts.
SQLite lock contention may fail a request rather than permit a partial write.

The independent runtime's CAS, journal, outbox and receipts remain its exclusive
versioned writer path. No monolith facade or REST call is described as a complete
independent-owner integration. A later adoption must explicitly decide how to
represent legacy opaque string criteria, enroll owned version-0 rows, expose
versions/idempotency to clients, and compose authenticated TEN authorization with
SRH commands. It must not simply remove the version guard.

## Existing unowned rows and operations

Rows with user_id=0 are preserved byte-for-byte by this repair and cannot be read,
updated, deleted, adopted or assigned through these private routes. They cease to
appear in REST lists, including administrator lists. Deployments that previously
used them as shared filters must plan an explicit ownership/product migration;
no shared-owner interpretation or mass version reassignment is introduced.

Before rollout, an already-authorized database operator can inventory unowned and
version-0 row counts in a protected maintenance environment. Keep a backup and
record ownership evidence separately. This stage provides no new administrator
API, cross-user disclosure, migration, deletion or reassignment tool. Any later
data migration requires its own approved ownership policy and quiesced writer
plan. Ordinary owned filters need no migration to continue using REST.

## Existing collaboration bindings

The existing ORC → SRH repository facade retains the same owner, read query,
manage command and changed-event declarations. Its three REST writer method keys
are renamed in `collaboration_contracts.json`: Create → CreateLegacy binds the
current actor and version 0; Save → UpdateLegacy applies scoped partial fields
inside a transaction; Delete → DeleteLegacy guards the scoped row and version.
The exact collaboration fingerprint is updated, with strict source-key equality
and owner/capability/event validation retained. The source inventory and public
module contracts are unchanged. These facade declarations do not mean REST
emits runtime events or invokes an independent SRH command. ARCH-04/ARCH-08 remain
open.

## Privacy and verification limits

REST response errors never echo request parsing/provider errors or saved names
and criteria. Saved-filter SQL tracing is suppressed for this repository, because
both rendered SQL and provider errors can contain private values. Storage errors
log only operation, numeric account/user/filter IDs. HTTP access logs omit all
saved-filter query parameters, including invalid/unknown input.

Actual router tests exercise signed users, two same-tenant members, an
administrator, a cross-tenant member, missing/forged tokens, revoked membership,
all CRUD paths, preserved unowned rows, versioned writer separation, exact JSON
numbers, legacy response shape, rollback and error/log redaction. Handler tests
also prove contradictory context fails before repository access. Repository tests
cover owner-store boundaries, transactional failure and native PostgreSQL lock
interleavings. Existing runtime, architecture and module-isolation guards remain
required. See packaged execution evidence for aggregate results; this document
alone does not establish a test pass or deployment.
