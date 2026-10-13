# Bounded billing customer and hosted management

## Routes and compatibility

Both existing aliases retain tenant membership and `billing.manage` checks:
`/api/v1/accounts/:account_id` and `/enterprise/api/v1/accounts/:account_id`.

- `POST /select_billing_currency` accepts exactly one explicit currency from
  operator-owned `BILLING_ALLOWED_CURRENCIES`. Existing ASCII case/space
  normalization is preserved (` EUR ` → `eur`); blank, non-string, non-ASCII and
  unsupported values are rejected. No currency or locale mapping is inferred.
- `POST /subscription` with no body or `{}` creates/confirms only the current
  account's provider customer. The existing success/data envelope reports
  `billing_customer_status: ready`, selected currency and
  `currency_selection_required: false`. This does not activate a subscription.
- Exactly one explicit `plan_id` or `plan_slug` retains the prior free-plan
  branch. Paid requests remain 503 with no provider or state changes; malformed,
  ambiguous and client provider/customer/amount/URL overrides are rejected.
- `POST /checkout` with no body or `{}` creates a `billing_portal.session`, never
  a purchase `checkout.session`. Compatible top-level/data fields contain the
  official short-lived HTTPS `redirect_url` and selected `currency`.
  `Cache-Control: no-store` also applies to errors. No session URL is persisted.
- `GET /subscription` preserves existing serialized subscription fields and adds
  `selected_billing_currency`, `billing_customer_status`,
  `currency_selection_required` and operator-owned `currency_options`. Typed
  embedding preserves integer precision. No `suggested_currency` is invented.

Missing explicit selection is a 422 validation state with required/options
fields. Missing customer is 409. Unproven/paid/unknown bindings require
reconciliation (409). Missing provider/configuration, upstream failures and
invalid provider responses are 503 without a redirect. Duplicate JSON keys,
non-object/oversized/trailing JSON, query overrides and unknown client fields
are rejected locally without changing Gin's global decoder behavior.

## Currency authority and existing monetary records

Only BIL's new `BillingCustomer` record is authoritative for these operations.
Historical TEN `Account.CustomAttributes.billing_currency` and BIL
`AccountSubscription.BillingCurrency` are not imported or rewritten; balances
are not converted or relabeled. Generic account/auth/admin serializers remain
unchanged. Clients relying on the legacy TEN attribute do not gain an implied
frontend compatibility guarantee; the additive billing fields identify the new
selection explicitly.

No subscription is compatible unless its referenced plan exists, has slug
`free`, price zero, active status, no cancellation marker and no legacy Stripe
customer ID. Missing subscriptions are allowed; paid, unknown, missing-plan and
unproven legacy rows are refused. The explicit free branch cannot bypass a
reconciliation conflict. Customer/portal operations never modify subscription
plans/status/periods, AccountBilling, balances, ENT rights or quotas. The existing
USD-only top-up and webhook paths remain separate and unchanged.

## Atomic selection, durable intent and recovery

`selected` is editable; `creating` and `ready` lock currency. SelectCurrency and
Prepare acquire the same account row transactionally. Prepare first acquires
SQLite's writer lock, then explicitly SELECTs FOR UPDATE. This second lock is
also required in PostgreSQL when the initial UPDATE saw no row but a concurrent
selector inserted one before the SELECT. Current allowed currencies are checked
under that lock before the transition. A rejected selection stays editable;
changing options never resets an existing intent or unlocks a created customer.

The allowed list is copied at service construction from process/operator
configuration. This is not a distributed SYS/BIL configuration transaction.

BIL commits an opaque immutable intent UUID, selected currency, provider scope
and first-attempt timestamp before provider work. Scope binds installation,
explicit test/live mode and a credential fingerprint, with no raw key stored.
Key rotation/account switches require reconciliation; no legacy customer is
silently adopted. Account and customer identifiers have durable unique indexes;
customer uniqueness remains global across scopes.

Concurrent/repeated creates reuse one idempotency key and identical metadata.
Unknown results or provider-success/database-failure retry that same intent
within 23 hours of its original timestamp. Expiry or clock rollback requires
reconciliation, never a replacement customer. Confirmed binding and its safe
BIL journal commit atomically. Snapshots preserve intent/provenance and the
original horizon. Ready customers are verified again before confirmation and
before portal creation.

## Provider configuration and credential boundaries

Customer operations require `BILLING_STRIPE_SECRET_KEY`, explicit
`BILLING_STRIPE_MODE` (`test` or `live`) and `BILLING_INSTALLATION_ID`.
`BILLING_ALLOWED_CURRENCIES` is an explicit comma-separated lowercase list with
no built-in entries. These are independent of top-up and webhook settings.

Portal entry additionally requires `BILLING_PORTAL_CONFIGURATION_ID`,
`BILLING_PORTAL_RETURN_URL` and an exact HTTPS match in
`BILLING_ALLOWED_RETURN_URLS`. The exact active configuration is retrieved and
its mode and explicitly disabled subscription-update/cancel features are
verified before session creation. There is no default configuration fallback,
configuration write, subscription/charge/refund/price/grant endpoint or direct
subscription-change flow.

Customer creation sends only opaque installation/binding references and the
selected currency metadata label. No email, name, address or payment data is
needed. Provider currency may be absent/null on a new customer; a non-null value
must match. Provider objects, IDs, mode, customer, configuration, return URL and
absence of an alternate flow/account are checked. Portal links must use exact
HTTPS `billing.stripe.com`; the Stripe API origin is fixed.

Requests have bounded body/depth/token/time limits, deny redirects and return
static errors. Provider bodies/headers/credentials/portal links are not logged.
Rejected query values are suppressed on both billing aliases, including
trailing-slash requests. Every later explicit portal request gets a fresh
operation key; no URL is stored, journaled, cached or automatically replayed.

## Test evidence boundary

Tests use injected synthetic transports, isolated SQLite stores and disposable
PostgreSQL owner schemas. The initial missing-row regression controls insertion
and query barriers and observes the actual PostgreSQL blocker PID. It checks a
racing selector cannot change the locked currency, and a disallowed first
selection remains editable without provider or intent writes. Additional cases
cover concurrency, ambiguity/expiry, journal rollback recovery, global customer
uniqueness, snapshots, authorization/tenant isolation, legacy casing and
unconfigured/paid/free branches. Local tests establish no live Stripe readiness,
production migration, real payment behavior or deployment.

Primary references:
- https://docs.stripe.com/api/customers/create
- https://docs.stripe.com/api/customers/retrieve
- https://docs.stripe.com/api/customer_portal/configurations/object
- https://docs.stripe.com/api/customer_portal/sessions/create
- https://docs.stripe.com/api/idempotent_requests
