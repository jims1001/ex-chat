# Local message-template import contract

The existing `POST /api/v1/accounts/:account_id/inboxes/:id/sync_templates`
route imports `Inbox.ProviderConfig.templates` into the CHN-owned local table.
It does not contact a provider, submit a template for approval, verify an
approval, reconcile remote deletion, or dispatch a message. CSAT delivery is
independent of this path.

## Client-visible correction

- New templates without a supplied status have `status: ""` (unknown). They
  are not automatically `APPROVED` or `PENDING`. Model and SQL defaults for
  `status` and `sync_status` are both empty strings.
- GET is storage-only, including an empty, unfiltered list. It never imports
  configuration or refreshes row timestamps. `meta.last_sync_attempt_at` is
  `null`: there is no durable inbox-level attempt record from which to answer.
- An explicit successful local import returns `status: "imported"` and local
  configuration wording. Its `data`, `templates`, and `payload` list aliases and
  local numeric template IDs remain intact. The existing POST `meta.synced_at`
  key is retained with `null`; this path has no provider-sync timestamp.
- Invalid configuration, an explicit configuration `error` / `sync_error`,
  invalid template rows, and database failures return a non-2xx response. An
  import either commits every row and its import metadata or commits nothing.
  Failed attempts are surfaced in the response, not stored as fabricated
  provider rejection facts.
- Empty configuration or no template definitions is a successful no-op. It
  leaves all existing content, statuses, IDs, and row metadata unchanged.

Successful imported rows use the existing `sync_status` column value
`imported`. Their legacy-named `last_sync_at` records this local import's time;
it is not evidence of provider synchronization. A later GET cannot reconstruct
an inbox-wide attempt from row timestamps, particularly for empty or failed
imports.

## Compatibility and boundaries

No provenance columns, snapshot codec, identity key, or provider adapter is
introduced. Existing explicit status values, including `APPROVED`, `REJECTED`,
and historical `synced` metadata, remain stored local data. Migration changes
future SQL defaults without rewriting historical rows or assigning them new
IDs. Existing snapshots keep their explicit values; omitted or empty status
fields restore as unknown instead of gaining an approval default.

For a configuration import that matches an existing row by the current
account/inbox/name key, an omitted `status` field preserves that row's stored
status. An explicitly empty, whitespace-only, or null status clears it to
unknown. A supplied nonempty status updates it as local data. For a new row,
an omitted status is unknown. Direct local CRUD and `chn.template.manage.v1`
retain their existing replacement/clear behavior; the import-only omission
rule does not apply to those commands.

Local create, delete, and import validate the current account/inbox pair inside
their write transaction. PostgreSQL locks that owner row for the transaction;
CHN does not read another module's account store. Public writes continue to
require `inbox_manage`; existing member read permission is unchanged. Nested
repository work remains inside the CHN command transaction, so receipt/event
failure can roll back local template writes.

The existing name-key upsert, nonempty provider-reference replacement behavior,
local delete, category/language/component defaults, and public template fields
remain unchanged. Absent configuration entries do not delete stored rows.
This patch does not resolve language-key collisions, provider-ID adoption,
remote deletion policy, approval provenance, or provider protocol/version
selection.

## Regression evidence

`TestMessageTemplateImportCorrections` and its PostgreSQL race counterpart cover
unknown defaults, status-field presence, repeat imports and stable IDs,
storage-only reads, target ownership, malformed/config-declared errors,
mid-batch create/update/lookup/final-read rollback, enclosing transaction
rollback, empty no-op imports, and repeat migration from the legacy defaults.
The PostgreSQL root has 34 required emitted cases in the reviewed CI manifest.

`TestMessageTemplateSnapshotCompatibility` covers old explicit values, omitted
fields, and explicit unknown fields through owner restore and a new snapshot
round-trip. Real-router role tests retain admin/custom inbox manager write
access, deny ordinary agent/unrelated-role writes, and prove GET cannot import
configuration. Existing CHN command/replay/event and PostgreSQL concurrency
regressions continue to run. Focused results are not full CI or provider
acceptance evidence.
