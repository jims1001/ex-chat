# Phase17 explicitly adopted numeric custom conditions

Legacy remains the default execution mode. No existing rule is adopted, enabled,
migrated or replayed by this change. Providers, date/time comparison, days_before,
attribute_changed, builtin-ID ordering, currency, percent, and general typed
conditions remain outside this stage. This is a local durable capability, not
full migration acceptance or deployment authorization.

## Explicit adoption and mode boundary

Saved rules and immutable rule versions carry `condition_semantics`. Empty means
exact legacy condition behavior, including greater/less false leaves and the
existing left-to-right AND/OR fold. The only new accepted value is
`numeric_custom.v1`. Adoption is an explicit rule create/update. An ordinary
update omitting the field preserves it; clone retains it; enable does not adopt.
Unknown/nonstring selectors reject. Explicit empty string selects legacy again
for a new rule version; it does not reinterpret existing executions.

In legacy execution mode an adopted definition may be prepared only while
inactive. A create/update/clone that leaves it active rejects, including enable.
An imported active adopted rule can be deactivated. The legacy loop defensively
skips any nonempty selector, with a safe fixed warning containing rule/account
IDs and an allowlisted profile enum. It cannot evaluate an adopted OR branch
using the old false-leaf behavior. No global mode switch is made here.

In durable mode a profile-only change creates a new immutable version. Adopted
conditions retain their exact raw JSON, custom owner and definition metadata;
legacy freeze normalization is unchanged. Ordinary HTTP arrays retain JSON
numbers via request-local exact decoding. Generic module command callers must
supply a string/raw JSON definition or an exact-number tree: floating-point
condition values in an adopted definition reject, because already-lost precision
cannot be reconstructed. This is deliberately stricter than the old generic
module envelope for newly adopted rules.

## Supported leaf contract

An adopted greater/less leaf requires all of:

- `attribute_key`: actual catalog key, nonempty and at most 100 UTF-8 bytes
- `custom_attribute_type`: exactly `conversation_attribute` or `contact_attribute`
- `custom_attribute_definition_id`: positive plain integral JSON ID, at most the
  smaller of platform uint and signed 64-bit storage range; strings/exponents are
  not definition IDs
- `filter_operator`: exactly `is_greater_than` or `is_less_than`
- `values`: exactly one JSON number; the existing scalar `value` alias is accepted
  only when `values` is empty
- The account-scoped CUS definition must match that frozen ID/key/model and have
  exact `attribute_display_type=number`

Canonical key/operator and wrapper aliases retain existing precedence. Numeric
leaves explicitly address the chosen custom owner, even if the key matches a
builtin name. Nonnumeric leaves retain their existing parser, lookup and scalar
stringification behavior. Their custom metadata is still ignored. The profile
is not a general typed-condition implementation.

Strict comparison means equal values match neither operator. The comparison is
exact signed decimal/scientific JSON-number comparison, with no float conversion,
string/boolean/array/object coercion, epoch inference, locale parsing, or currency
conversion. Negative values and zero are valid numbers; negative zero equals zero.
The bound is 1,024 bytes per literal and absolute explicit exponent <=1,024.
Comparison is linear in literal length and never expands powers of ten. Each
adopted definition is at most 1 MiB and 100 condition records. A custom value
object over 1 MiB is invalid for numeric evaluation. These are intentional finite
compatibility limits, including mathematically valid but excessively padded
encodings; values are never rounded or clipped to fit.

Missing key, null value, absent contact, or an empty custom-attribute payload is
no-match. A malformed nonempty object or a present nonnumber/out-of-bounds number
is `invalid_frozen_definition`. Invalid cardinality/metadata/threshold or catalog
mismatch is likewise an explicit invalid-definition decision. Such failures do
not turn into a false leaf hidden by OR. Ordinary false leaves still fold by the
previous condition's existing OR/AND semantics.

## Catalog and target ownership

Create/adopt validates syntax only. It does not claim the supplied definition
exists or has the right type. At admission the new read-only CUS contract
`cus.automation.number_definition.get.v1` reads that exact ID under the account,
returning only identity, key, model, and display type. Duplicate definitions with
the same account/model/key are rejected as ambiguous. There is no invented
active/disabled flag: the catalog does not have one. Description/default/display
name changes do not change this structural type binding.

AUT never queries CUS/CON tables. CUS owns the catalog and contact custom values;
CON owns conversation custom values. Requests retain the supplied actor; catalog
and custom-value projections require a System/module actor and never silently
elevate a User. All owner reads precede the AUT admission transaction, including
single-connection monolith operation. Wrong response account/ID is a dependency
failure. Missing/deleted/renamed/remodeled/retyped/ambiguous definitions produce
an invalid definition for new admissions. Transport/store/permission failures do
not become no-match; intake remains retryable with no committed execution.

Definition identity, expected number type, key/model, and profile are frozen in
the rule version. Actual catalog and owner-value observations are taken at initial
admission; this is not a cross-owner atomic historical snapshot. Deferred siblings
freeze those observations, preserve shared fields, and use the existing confirmed
predecessor/version fence. Later rule/catalog/custom-value edits do not reinterpret
that admitted batch. New admissions validate the current catalog again. CON CAS
still rejects unrelated target interleaving before later actions. A deleted and
recreated same-key definition with a different ID is not silently adopted.

## Checkpoints and compatibility

Each new execution stores its private condition selector. Numeric pending
siblings use `aut.sibling_conditions.v2`, freezing the selector and catalog/value
observation. Markerless siblings stay v1. Matching, no-match and invalid decisions
remain frozen across cancellation, retries, restart, replay, and snapshots.

New exports use `aut.execution.v5`. Complete v1/v2/v3/v4 codecs retain their old
behavior; they reject nonempty condition selectors, new numeric sibling codecs,
or numeric catalog observations. Restore validates execution/version/profile
agreement, catalog tenant/identity and immutability, condition decisions and
existing causal proofs atomically. No old committed decision is reevaluated and
no mixed-old/new-binary rollout claim is made.

## Precision at ordinary ingress

Conversation/contact HTTP and existing public/widget custom-attribute writes use
request-local UseNumber decoding without changing Gin's global behavior. Owner
merge/delete paths retain stored number tokens when unrelated keys change.
Strings remain strings. Legacy condition comparison still uses its old decoder.
Numbers already rounded before this change or by a caller's binary float cannot
be recovered. External/custom writer correctness and preexisting stored precision
remain operator data-quality concerns; no catalog-wide rewrite is performed.

## Numeric action-ID cost bound

The shared durable/legacy numeric action-ID parser now rejects nonintegral/out-of-range
values by bounded lexical/range checks before conversion, without big.Rat power
allocation. Numeric ID tokens share the 1,024-byte and +/-1,024 explicit exponent
bounds. Exact usual integer, zero-tail decimal and scientific encodings remain
supported, including IDs above 2^53. Zero/negative/fractional/overflow inputs reject;
platform uint range and existing parameter/fallback precedence remain unchanged.
Conservative independent inventory analysis limits remain disclosed as incomplete
analysis, not a new action-runtime failure.

## Read-only inventory and remaining gates

The inventory reports each source's allowlisted selector and distinguishes a
syntactically valid adopted numeric leaf from an unmarked inert leaf. It never
queries the catalog or values; actual CUS identity/type/value/permissions remain
unassessed. Exact adopted thresholds do not trigger legacy float-rounding warnings.
Legacy and nonnumeric warnings, secret redaction, response limits, pure projection,
and no-write guarantees remain in force.

Actual saved-rule review/adoption, offline execution-mode transition, frontend
profile controls/client history consumption, providers, production rollout,
load/soak, broad owner-trigger coverage, and ARCH-04/ARCH-08 final acceptance remain
separate gates. This stage adds one explicit CUS-owned read-only contract; strict
registry fingerprint and source/dependency guards are retained.
