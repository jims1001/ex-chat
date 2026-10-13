# Backend CI evidence contract

Use `make ci` and `make ci-postgres`, which call `scripts/ci.sh`. The Python runner
is an internal component, not a replacement for the shell gate's environment,
service, module, vet and build guards. Both workflow jobs must pass on the same
source. A local core result alone does not establish native coverage, hosted CI,
provider acceptance, a release or full module-isolation acceptance.

## Complete selection

- Discovery uses `go list -buildvcs=false` and `go test -json -list .` over only
  `./cmd/... ./internal/... ./pkg/... ./test/...`. Race discovery uses `-race` for
  both commands; ordinary and race build-tag inventories are kept separately.
- Test, runnable Example and Fuzz names are included. Default fuzz seed execution
  is covered; this is not fuzzing (`-fuzz`) or benchmark (`-bench`) coverage.
- Every discovered package gets a batch, including packages with zero tests.
  Discovery and execution require a successful terminal for every package.
- Core ordinary/race batches preserve registration order, cover each discovered
  root once, and contain at most 12 roots. Manifest `standalone` entries isolate
  two known slow roots. This changes test process boundaries: it does not prove
  one-process cross-test ordering behavior. No tests use a new `-short` flag.
- The existing Docker PostgreSQL smoke test is explicitly excluded only from
  core, because its no-DSN fallback probes a local database. Its exact native
  case remains mandatory. No other discovered core case can be excluded.
- Exact native/isolation manifest paths are the only permitted core skip events.
  Unknown skips fail. `coverage.json` records every skipped path and each native
  obligation, including the core exclusion. Isolation executes again with its
  opt-in before core completion; native obligations require the separate native
  job on the same source. Mapping is not evidence of execution.

## Native manifest maintenance

`coverage-manifest.json` is a reviewed assertion, not an automatically learned
baseline. Its `native` map lists every required emitted case, including children.
It currently contains 2954 cases under 181 roots. A passing root cannot substitute
for a missing child. Native execution rejects skipped cases and any unreviewed
extra case. The named-root convention is `Postgres|PostgreSQL|NativePG`; discovered
roots matching it must agree with the manifest. Nonstandard native roots require
an explicit nested selector or a documented convention change.

The conventional group runs once with `-race`; two supplemental selectors use
Go's slash-separated anchored patterns:

- `^TestAUTSnoozeExpiryAliasesRecoverOnceThroughOrdinaryRules$/^Postgres$`
- `^TestModuleMigrationsAreIsolatedAndRepeatable$/^postgres$`

These select the required native branches without selecting their SQLite
siblings or accidentally filtering the conventional roots' children. Migration
coverage explicitly lists all 26 module leaves. A slash inside a `t.Run` name
need not produce a separate intermediate parent event; record actual emitted
case paths, not invented ancestors.

When adding/renaming/removing a native test or changing a dynamic case table:

1. Review its intended cases and edit the manifest, including required child paths
2. Keep every native root present in fresh discovery; do not lower coverage to
   accommodate a skip, failed fixture or incomplete run
3. Update the exact-count regression assertion if the reviewed inventory changes
4. Run Python guard tests and the full disposable native gate; review exact-case
   evidence and zero skips

`CI_POSTGRES_EXTRA_PATTERN` retains the old additive top-level Go regex option.
It cannot remove manifest tests and cannot express arbitrary nested-case coverage;
use the reviewed manifest for that. Additional selected roots and their started
children must pass, but extras do not relax native exact-case expectations.

## Evidence and local resumption

Every invocation starts a new `tmp/ci-<mode>.*` directory unless the user explicitly
passes `./scripts/ci.sh core --resume tmp/ci-core.<suffix>`. Resumption is rejected
when `CI` or `GITHUB_ACTIONS` is set, and for the native mode. Do not import or
share checkpoint directories as proof. They are unsigned, owner-trusted local
state, not an authenticity boundary or hosted CI cache.

The runner writes each stdout line immediately to an attempt JSONL file, keeps
stderr separate, syncs logs before writing the result, and writes JSON metadata
atomically. Completion requires all of:

- Process exit 0 and unchanged source before/after the batch
- Well-formed newline-terminated JSON, package start/terminal pairs, all expected
  root terminals, and terminals for all started children
- No failing event; no unmapped core skip or selected native/isolation skip
- Matching exact native inventory where applicable

A partial attempt has no successful checkpoint and is rerun in full; its old log
is preserved. A failed completed attempt is never reused or silently retried.
A corrupt log, missing terminal, duplicate terminal, changed command or changed
expected inventory is rejected even when a result file claims success. No test
pass is recovered out of an unfinished package log. `coverage.json` is marked
incomplete when an invocation begins and becomes complete only at the end.

Reused batch logs are revalidated against the current contract. The contract
binds every repository file's bytes and executable bit (excluding `.git`,
`__pycache__`, `tmp/`, and runtime uploads under `test/uploads/`), both discovered
inventories, test flags, runner/manifest source, Go version/binary/compiler tool
hashes, selected Go build settings, OS identity and Python version. The entire
inherited environment and effective Go build settings are bound through one
locally salted HMAC; variable values
and individual secret hashes are not persisted. Make/shell launcher controls (`MAKEFLAGS`, `MFLAGS`, `MAKELEVEL`, `SHLVL`,
`OLDPWD`) are removed before both test execution and fingerprinting, and `PWD`
is set to the repository root. This permits fresh `make ci` followed by direct
script resumption. Other environment changes can conservatively invalidate reuse. The salt file is private (mode 0600); omit it from shared evidence/artifacts.
The salt is not a credential and
does not make an attacker-controlled checkpoint trustworthy.

`GOWORK=off` prevents inherited workspace replacements. Local module replacements
are rejected rather than pretending external files are included in the source
fingerprint. `go mod verify`, vet and all three builds run again before the
runner. Checkpoints cannot prove external service state, C compiler/system-library
contents, wall-clock conditions or arbitrary out-of-tree fixtures are unchanged.
Resume only your own interrupted run with those inputs unchanged; use a fresh
run after relevant environment/tool changes. Native and isolation always execute
fresh because service/opt-in state needs current evidence.

A runner lock prevents concurrent use of an evidence directory. A fully written
successful checkpoint is the unit of recovery. If a killed process was not
cleaned up by the executor, stop that old process before resuming; the runner
handles ordinary Python interruption by terminating its child process group.

## Validation and reporting

Run `python3 -m unittest discover -s scripts/ci -p 'test_*.py'`. Tests exercise
missing/failed/duplicate/truncated terminals, skipped or absent nested cases,
zero-test packages, examples/fuzz roots, selector regressions, interrupted
attempts and stale/corrupt checkpoints. The CI shell retains exact Go/CGO guards,
module verification, vet, builds, normal/race modes and opt-in isolation checks.

`coverage.json` reports complete component coverage and reused-batch counts.
A successful resumed shell command is not evidence that all tests ran in one
uninterrupted invocation. Never report a never-run `make ci` as exit 0 merely
because equivalent component evidence exists. Report fresh, reused, interrupted,
failed and unrun stages precisely.

## Ordinary SMTP queue cases

`TestPostgresOrdinaryEmailWorkersAndManualRetry` adds fifteen exact native results:
the root, nine direct children, two crash-recovery children, and three
conversation-cleanup children. It exercises
real PostgreSQL workers/manual retry, unknown exclusion, crash recovery, atomic
outcome/event failure, anonymization/concurrent admission, and snapshot rollback.
All SMTP is synthetic loopback. The ordinary/race suite also checks disabled
admission, replay conflicts, sender identity, TLS, deadlines and lost acknowledgements.
These cases do not cover adopting legacy direct mail or the full email/push scope.

## Account transcript producer cases

`TestPostgresAccountTranscriptBridgeLifecycle` adds twelve exact results, covering
concurrent request identities, lost admission acknowledgement, anonymized-key
tombstones, conversation deletion/capture races, bounded deadlines, worker restart
after unknown submission, and versioned private snapshot recovery.
`TestAccountTranscriptQueueNativePostgresREST` adds seven exact results for the
actual Account REST route, loopback SMTP worker, status, concurrent same-key
replay, changed sources/configuration and current membership/permission checks.
Their nineteen exact results raise the native inventory from 773/113 to 792/115
cases/roots. The separate SQLite ordinary/race cases include ineligible senders,
size/input rejection, private SQL failures, branding and the 200-message cap.
These are reviewed obligations; only emitted passing evidence establishes a run.

## Widget transcript producer cases

`TestPostgresWidgetTranscriptBridgeLifecycle` adds twelve exact results for
scoped concurrent admission, lost acknowledgement, anonymization/deletion fences,
deadlines, unknown restart and v3 snapshots. `TestWidgetTranscriptQueueNativePostgresREST`
adds eleven results for actual Widget HTTP admission/worker/status, concurrent
keys, token-refresh/frozen replay and current source/conversation authority.
The reviewed native inventory is now 853 exact cases across 121 roots.
SQLite ordinary/race cases also verify source/account/inbox/contact and Account-log
isolation, revoked secondary credentials, first100 public filtering, original sender
provenance, body-free status and no direct fallback after failed admission.
Only captured passing evidence establishes execution; no external provider is used.

Phase41 adds 27 exact native cases under three roots for Inbox transcript
REST routing, competing workers and crash/scrub/delete fences, and strict private
route snapshot restore/scrub. Legacy global native obligations remain unchanged.

Phase44 adds 64 reviewed exact native cases under three roots: owner count
aggregation, in-flight owner read deadlines, and complete V1/V2 count-series HTTP.
They cover 400-day grouped-query bounds, owner/type/tenant isolation, fractional
and pre-epoch PostgreSQL boundaries, structured errors and inherited authority,
revocation/demotion, canceled/blocked reads, and original-route compatibility.
The inventory is now 976 cases / 126 roots. SQLite canonical-storage rejection
is tested independently; the PostgreSQL branch does not apply its text guard.

Phase45 adds 35 reviewed exact native cases under three roots: MSG summary
aggregation, separate-owner summary semantics, and V1/V2 summary HTTP. The
inventory is now 1011 cases / 129 roots. These preserve optional bounds, exact
types, total-all-types counting, PostgreSQL fractional boundaries, default-count
insertion invariance, legacy true-business-hours cohorts and error privacy.
The existing independent-store RPT acceptance fixture composes the new MSG
capability explicitly. Focused native passes do not replace the full gate.

Phase46 adds 13 exact native cases under `TestPostgresAutomationOwnerWiring`: the
root and twelve shared SQLite/PostgreSQL real-router scenarios. They cover macro
and Public status capture, frozen rule execution and replay, same-status writes,
owner-local capture rollback, message/label capture, ordinary-root stale-version
fences, earlier commits surviving final macro failure, Public custom attributes,
current authority and default-off behavior. The inventory is 1024 cases / 130
roots. This composition change does not make the whole macro atomic or introduce
causal ancestry for manually executed macros. Focused native evidence does not
replace the full disposable native gate.

Phase48 adds ten exact native results under `TestPostgresChannelTextOwnerIsolation`: the root and nine separate-owner cases shared with SQLite. These cover credential exclusion from MSG/OPS stores, opaque Send-only preparation, MSG dispatch fencing, pre/post-preparation rotation, retryable decryption failure, owner-verified Twilio lookup/signatures, whole-envelope WhatsApp binding preflight and its existing no-decryption callback behavior. This is a local logical ownership boundary, not physical process isolation or a remote transport contract. The inventory is now 1306 cases / 133 roots; focused passes do not replace full core/native gates.


Phase50 added thirteen exact native contact-search cases under one root (1319
cases / 134 roots). Phase51 adds 23 exact native results under
`TestPostgresNativeFCMOwnerStores`: the root and 22 separate SYS/NTF owner-store
cases shared with SQLite. These cover direct selection, no auth/config fallback,
gateway compatibility, accepted/rejected/unknown outcomes, exact-token cleanup,
replacement/refresh/recreated-identity races, account/user isolation, audit failure
stopping later sends, no cleanup on failed persistence, preferences/quiet hours,
URL-token treatment and non-FCM compatibility. The reviewed inventory is 1342
cases / 135 roots. Synthetic JWT, OAuth cache/expiry/concurrency, HTTP bounds and
secret-safe errors additionally run in ordinary/race unit tests; no real Firebase
credentials or external token/send requests are used. Focused evidence never
replaces the full disposable native gate.

The rebuilt bounded billing-customer/portal slice adds 32 exact native results
under two roots: TestBillingCustomerRepositoryPostgres (12 direct children plus
16 nested compatibility/corrupt-state cases) and
TestBillingCustomerInitiallyAbsentPostgres (two deterministic insertion-gap
cases). Reviewed inventory is 1374 cases / 137 roots. The initial absent-row
case observes PostgreSQL's actual blocker PID before releasing initialization;
it does not infer locking from a sleep. All provider calls remain synthetic.
Only freshly emitted passing evidence validates this reconstructed source.


Phase54 adds 21 reviewed exact native results under two roots: isolated-owner
human response durations (four paths) and shared V1/V2 real-router HTTP coverage
(seventeen paths). They cover exact bot exclusions, bot-only sample omission,
ordinary/business-time values, existing record/FCR/resolution contracts,
owner/tenant scope, message clipping, summary JSON/CSV, all four dimensions and
three FRT aliases. The current inventory is 1983 cases / 143 roots, including
phase53's fixed-offset obligations. Registration is an obligation, not a claim
that a test or full gate has run. Focused passes never replace both full gates.

Phase55 adds 87 reviewed native results under two roots: isolated-owner CHN
failure/recovery and healthy empty/missing-inbox compatibility (21 paths), plus
V1/V2 HTTP failure/recovery, aliases, JSON/CSV, authority and empty-account checks
(66 paths). Ordinary/race unit cases additionally reject partial rows returned
with an error and verify first-call failure stops before other owner reads.
The reviewed inventory is 2070 cases / 145 roots. This changes no business-hours
policy or HTTP error vocabulary; both full gates still require fresh passing
evidence. Manifest registration alone does not establish a run.

Phase56 adds 46 reviewed exact native results under two roots: isolated-owner
agent/annual dependency failures and recovery (14 paths), and V1/V2 real-router
coverage (32 paths). CON read failures at ordinals 1–4 must discard all agent
rows; RPT annual Row/SQL errors must not become zero averages. Recovery preserves
member pagination, live counts, user/tenant scope, UTC year boundaries, integer
conversion and healthy empty values. HTTP includes all three registered annual
paths, authority-first behavior and existing generic errors. The reviewed
inventory is 2116 cases / 147 roots. These are proposed execution obligations,
not passing evidence; both full gates must run on the final immutable source.

Phase57 adds 54 proposed exact native results under two roots: QLT/CON/IAM
owner-store and query failure/recovery contracts (21 paths), plus all three
registered CSAT HTTP routes (33 paths). Every legacy/advanced required SQL read
is fail-closed; AVG NULL remains healthy zero, and successful shapes, rounding,
rating eligibility, inclusive endpoints, filters and existing permissions remain
unchanged. Ordinary/race fault-driver cases additionally cover Row/Scan failures
and partial rows followed by an error. Phase57’s reviewed inventory was 2170
cases / 149 roots; the current total, including later batches, is stated above. These source registrations are obligations, not emitted test passes.
Both complete gates still require evidence from the same final source.

## Data Import durability cases

`TestPostgresDataImportDurabilityOwners` and
`TestPostgresDataImportDurabilityHTTP` add 60 exact native obligations (46 and 14).
They cover separate MIG/CUS stores, task admission, processing and final saves,
empty-input persistence, mutation failures, actual missing-table errors,
count-only truthfulness, preserved partial results, permissions and real V1
routes. V2 has no registered Data Import route. Synthetic failure/recovery tests
do not establish atomic cross-owner rollback, automatic retry, provider import
or concurrent start/cancel safety. Listing these paths is not a test pass.

## Campaign prerequisite-read cases (phase59)

`TestPostgresCampaignPrerequisiteOwners` and
`TestPostgresCampaignPrerequisiteHTTP` add 78 proposed exact native obligations
(60 owner paths and 18 HTTP paths), bringing that phase’s source inventory to 2308
cases / 153 roots. They cover complete pagination, first/late failure and
rows-plus-error rejection, preflight dedup/audience planning, separate owner
stores, unchanged healthy selector/order/status contracts, real-router generic
errors and primary creation preservation. Ordinary/race also checks unavailable
service handling. These are not test passes or full OPS acceptance. No Go,
build or service execution is authorized until the phase58 lane is released.

## Notification mutation cases (phase60)

The proposed `TestPostgresNotificationMutationOwners` and
`TestPostgresNotificationMutationHTTP` roots add 205 exact root/subtest paths (current cumulative inventory: 2513
cases / 155 roots).
Owner cases have only the NTF notifications table; HTTP cases use the real
router and authenticated users. Executed-SQL callbacks test write/readback
rollback and generic errors, including a post-update readback that reports
record-not-found; unavailable-table cases exercise real SQL errors.
Bodies/defaults, PATCH precedence, both existing batch aliases, scope and
recovery are explicit obligations. This inventory was prepared while runtime
execution was blocked. It is NOT RUN evidence; full native/core gates and
independent review on the final source remain required.


### Phase61 SLA source-read obligations (prepared, not run)

`TestPostgresSLASourceReadOwnerStores` and `TestPostgresSLASourceReadHTTP`
add 135 proposed exact root/subtest results. The current cumulative inventory
is 2648 cases / 157 roots; prior phase counts above remain historical. Required
sources must fail before SLA writes, lists must discard earlier successful items
when later reads fail, and independent account successes do not erase failures.
See `architecture/sla-required-read-preflight.md`. Inventory is not execution
proof; both full gates and focused fault/recovery tests must pass on the final
source after phase60 acceptance and explicit runtime release.

## Reconstructed phase62 explicit retry target

`TestPostgresWebhookExplicitRetryOwner` and `TestPostgresWebhookExplicitRetryHTTP`
add 29 exact obligations (11 owner, 18 HTTP), for 2677 cases / 159 roots.
They bind an explicit positive delivery ID to the account without fallback,
exercise empty/populated target-read failures and actual unavailable tables,
verify zero mutation/HTTP before selection, and retain no-ID compatibility.
HTTP validates IDs, authority ordering and the existing successful envelope.
The absent-service regression runs in ordinary/race handler tests. All outgoing
HTTP uses synthetic transports. These are new reconstruction obligations, not
recovered former phase62 evidence; full fresh core/native verification is required.

### AIC usage session-timezone boundaries

Three additional exact `TestPostgresAICUsage/session_timezone` obligations cover
UTC, Asia/Yerevan and Asia/Shanghai, bringing the inventory to 2680 cases under
the same 159 roots. They preserve the original UTC year bounds, exercise the
historical Shanghai second-resolution offset, and reject genuinely out-of-range
UTC instants and infinities. PostgreSQL timestamp projection normalizes instants
to UTC before the existing strict text validation; no OS/session timezone change
or wider accepted timestamp grammar is required.

## Phase63 macro plan preflight

`TestPostgresMacroPlanPreflightOwners` and `TestPostgresMacroPlanPreflightHTTP`
add 80 reviewed exact obligations (64 owner paths and 16 HTTP paths), bringing
the inventory to 2760 cases / 161 roots and retaining phase62's three AIC
session-timezone cases. Every parsed action name is checked before target reads
or owner effects. Unsupported external, unknown, empty and whitespace names
reject the entire plan, including when there are no targets. Visibility and
parsing/name precedence remain unchanged, as do all 25 implemented names,
existing target behavior and HTTP error/success envelopes. The HTTP fixture owns
and joins its router workers before database cleanup using phase62's lifecycle.
This is supported-name preflight, not atomic cross-owner execution or new
provider, idempotency, version or cancellation/compensation behavior. Source
registration is not execution evidence; focused and full core/native gates must
pass on the same final source.

Phase64 adds82 explicit PostgreSQL cases across9 roots for OPS canned-response maintenance, validation, rollback, independent-connection concurrency, snapshot/high-water recovery, old-schema migration and legacy REST guards. Existing2760 native cases remain obligations. The full inventory is2842 cases /170 roots; full acceptance still requires fresh core and PostgreSQL gates on the same frozen source. No timeout, fault assertion or skip rule is relaxed.

## Canned-response variable preview (phase65)

Phase65 adds 70 reviewed PostgreSQL paths across 7 roots for strict read-only variable preview: semantics, validation, actual wire decoding, bounds, concurrent coherent source/version reads, cancellation, and a PostgreSQL read-only transaction. All 2842 phase64 paths remain obligations. The inventory is 2912 paths / 177 roots. Renderer-only ordinary tests additionally cover grammar and deterministic cancellation. Fresh core and native gates on one frozen source are required; test timeouts, skip rules and fault assertions are unchanged.

### Phase66 MSG User-actor admission inventory

Phase66 adds 42 reviewed PostgreSQL paths across four roots: actor admission/revocation (6), fail-closed errors (19), service and message compatibility (16), and historical receipt recovery (1). All 2912 phase65 paths remain obligations; the inventory is 2954 paths / 181 roots. Exact totals and per-root obligations are pinned by the Python guards. No contract registration surface changes, old obligation removals, timeout relaxation or skip changes are introduced.
