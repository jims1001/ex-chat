# Bound assistant summary read (phase37)

This slice implements the already-declared `aic.assistant.get.v1` lookup for an
account and inbox. `CaptainAssistant` and `CaptainInbox` are AIC-owned and are
populated by existing assistant management and inbox-binding paths. The result
is the stored binding's assistant `id` and `name`, or an explicit null when no
binding exists in the requested account. It makes no active-status, availability,
entitlement, provider-readiness or generation-completion assertion.

## Consumer adoption and authority

Both existing gateway consumers (`ConversationHandler` and `InboxHandler`) and
the module query adapter call `CaptainRepository.FindAssistantByInbox` with the
request context. This is shared owner-primitive adoption, not a gateway cutover
to a separately deployed AIC service. Independent deployment and full AIC
acceptance remain unfinished. The two existing collaboration registry edges
already map that method to `aic.assistant.get.v1`; ownership and dependency edges
are unchanged.

Gateway authentication and current account membership middleware remain in place.
The conversation handler resolves the current conversation by account and now
requires its already-hydrated CHN inbox to exist and match both the account and
current inbox ID before consulting AIC. A deleted or foreign current inbox is
404 even if a same-account AIC binding survives. The inbox handler still resolves
the inbox by account. This uses the existing owner hydration and introduces no
new role or permission; it is not a distributed lock or a cross-owner linearizable
ownership check. The AIC primitive performs no CHN inbox
hydration, tenant-existence check or end-user reauthentication.

The internal transport authenticates its configured bearer and allowlisted module
caller. That trusted caller asserts the account scope and must authenticate the
current end user's account/resource access at the gateway. Actor fields are
context, not end-user credentials or global authority; a claimed SuperAdmin
still receives only the envelope account's result. An absent own-account binding
is null even if another account has a binding with the same inbox ID. This query
does not establish that the inbox exists or is accessible to the claimed actor.

## Fixed internal request

POST `/contracts/queries/aic.assistant.get.v1` with the existing bearer, caller,
accepted contract version, timeout and body-size rules:

```json
{
  "query_type": "aic.assistant.get.v1",
  "account_id": 91,
  "actor": {"type": "User", "id": "7", "auth_source": "module"},
  "filter": {"inbox_id": 23},
  "sort": ["inbox_id"],
  "page": "1",
  "fields": ["id", "name"],
  "consistency": "strong",
  "requested_at": "2026-10-07T00:00:00Z"
}
```

`inbox_id` is a canonical positive JSON integer, at most 2^63-1. Integer decoding
preserves values beyond float64 precision. Strings, fractions, exponents, zero,
negative numbers and overflow are rejected. Account IDs must be positive and
within the same storage range. The filter has exactly this key; field order,
sort, page and consistency are fixed, and cursor must be empty. Configuration,
status, generation, pagination and alternate assistant-ID lookup shapes are
unsupported. Invalid shape is 400 with a static error. The runtime success
envelope's `data` is `{"assistant":{"id":17,"name":"Support"}}` or
`{"assistant":null}`.

## Gateway responses and compatibility

- Canonical `GET /api/v1/accounts/{account_id}/conversations/{id}/inbox_assistant`
  returns exactly `{"assistant":{"id":17,"name":"Support"}}`, or
  `{"assistant":null}` with HTTP 200 when unbound, as documented in API09
- Existing `/conversations/{id}/assistant` and `/inboxes/{id}/assistant` aliases
  keep the success envelope `{"success":true,"data":{"id":17,"name":"Support"}}`
  and their unbound HTTP 404 behavior
- Alias success payloads previously serialized the full assistant, including
  configuration, prompts and hydrated inboxes. They now contain only `id` and
  `name`. Clients relying on any removed field must change; this is intentional
  response narrowing, not backward compatibility for the full model
- Invalid stored relationships or unavailable storage produce a static gateway
  HTTP 500 or internal `dependency_unavailable` HTTP 503, with no partial DTO or
  database details. Missing/foreign gateway resources return 404, including a stale conversation
  reference to a deleted or foreign inbox;
  authentication and account membership failures keep their existing denials

## Bounded storage read

One SQL statement joins the matching account/inbox binding to its assistant by
both assistant ID and account. It projects only binding `id`, `account_id`,
`inbox_id`, `captain_assistant_id` and assistant `id`, `account_id`, `name`.
`LIMIT 2` permits one result and one duplicate sentinel. The join observes the
relationship in one database statement, avoiding a binding/assistant gap between
separate reads. It does not promise a snapshot across successive requests.

Duplicate bindings, duplicate joined assistant rows, dangling or cross-account
references, invalid projected identity values, and invalid names fail closed.
Projected numeric values must be positive stored integers. Names must be nonempty
valid UTF-8, at most 255 characters and 1020 bytes, following the model's 255
character limit; text is otherwise preserved. SQL guards bound transferred name
and numeric values before scanning, including corrupt oversized storage. This
is validation of rows selected in the requested scope, not a global store audit.

No configuration, system prompt, response guidelines, guardrails, model/status,
history, credentials, quota, entitlement or foreign-owner table is read. There
are no writes, provisioning, provider calls, network dependencies, events,
receipts, journals or outbox effects. Context cancellation reaches the SQL query.

## Recovery and verification limits

Existing AIC owner snapshots include assistants and bindings. Focused SQLite and
native PostgreSQL tests verify that a quiesced owner snapshot restores the same
bound summaries and account separation, and that ordinary reads detect a broken
relationship before restore. The generic assistant snapshot format/validation
is unchanged; this slice does not introduce a new recovery acceptance policy or
claim a coherent cross-owner snapshot. Pending SAML, date, frontend, audio and
provider-template decisions remain separate.

Coverage includes real gateway routing/authentication, exact field omission,
unbound and corrupt relationships, native and SQLite bounds, owner-only minimal
projections, read-only transactions, cancelled/concurrent reads and recovery.
These local fixtures do not establish a real provider integration or full AIC
acceptance.
