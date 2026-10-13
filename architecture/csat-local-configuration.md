# CSAT local configuration and unavailable WhatsApp provisioning

`GET/POST /api/v1/accounts/:account_id/inboxes/:id/csat_template` preserves the
existing non-WhatsApp local configuration behavior. POST requires the established
`inbox_manage` permission; GET retains its existing tenant-member authority.
Account administrators and qualifying custom roles may write. Ordinary agents
cannot write unless their current custom role grants that permission.

Local POST accepts one complete JSON object. `template` and `csat_template`, when
present, must be objects; the existing precedence is a nonempty `template`, then
a nonempty `csat_template`, then the flat `message`, `button_text`, `language`,
`display_type` fields. The existing flat fallback and response fields are retained,
including the local `active` presentation status and `template`, `csat_template`,
and `data` aliases. Nested object contents, including `question`, `ratings`, custom
fields and client status values, remain local data. They are not provider claims.

An existing inbox must belong to the current account before any save. Invalid IDs
or malformed input return 400; missing/foreign inboxes return 404; lookup,
serialization or persistence failures return a static 500. A deletion between
lookup and save returns 404 and never recreates the row. Failed requests neither
overwrite configuration nor return synthetic success.

GET preserves the local presentation default for unset, JSON `null`, or empty
object storage. Historical null remains an unset value because the existing
repository could persist a nil map as JSON null. Malformed JSON or stored
array/scalar values now return a static 500 instead of concealing the invalid data
behind defaults. Reading never rewrites the stored value. This repair does not
delete, migrate or normalize old configuration.

For the existing `Channel::Whatsapp` classification, canonical GET and POST return
503 after the scoped inbox lookup. The account-level POST alias
`/api/v1/accounts/:account_id/whatsapp/csat_template` also returns 503 and now has
the same `inbox_manage` write permission. Its undocumented target selector remains
unresolved; no inbox ID is guessed and no new body/query mode is introduced.
The error says that WhatsApp CSAT template provisioning and provider status are
currently unavailable. Adding credentials alone cannot enable an implementation
that does not yet exist. These endpoints perform no provider calls or writes.

Local configuration and generic stored message templates do not prove that a
WhatsApp template exists, was submitted for review, or was approved. This work
does not implement Meta template identity/naming, category, language defaults,
survey-button URL, uncertain-submission reconciliation, provider review status,
template synchronization, or survey dispatch. Existing OAuth, ingress, outbound
text, public CSAT surveys and call-recording behavior are outside this repair.

Regression coverage retains the existing WebWidget `question`/`ratings` round
trip, all accepted local forms and response aliases, tenant and role checks,
invalid input, storage failures, deletion races and legacy stored values.
Disposable SQLite and native PostgreSQL run the same configuration regressions;
provider requests are trapped locally and expected to remain zero.
