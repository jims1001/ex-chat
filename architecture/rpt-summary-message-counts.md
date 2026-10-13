# Summary message record counts (phase45)

## Defect and bounded correction

The default Account Summary chose message counts from the conversations created in the requested interval if at least one such conversation existed, and from all account messages otherwise. Two in-window messages on an older conversation consequently became zero after adding an unrelated conversation. V1/V2 `reports/summary` and the JSON/CSV `reports/conversations_summary` service consumers now use account-scoped MSG timestamps for the three message counts when business hours are omitted or false.

`total_messages` counts every persisted message row, including activity/template and unknown types. Incoming and outgoing require exact `message_type` matches. Private, bot, queued and failed outgoing records retain their previous counting semantics. No count proves an attempted or successful delivery.

## Ownership and compatibility

`ReportMessageSummaryPort.CountReportSummary(accountID, since, until)` carries only an account ID, optional time pointers and an integer-only `domain.ReportMessageSummary`. MSG executes one parameterized SELECT with three COUNT expressions. No bodies, conversation IDs, cross-owner joins or writes are involved in this capability. The separate inventory in dependency_sources.json and collaboration_contracts.json and its AST guard describe this local capability without inventing a remote contract.

The default shared-store constructor installs the MSG repository; router composition and separate-store callers explicitly install their MSG owner with SetSummaryMessageOwner. Missing owners and database failures fail the summary rather than returning a misleading zero or partial count. Raw aggregate driver/SQL errors are replaced with a stable private error; the legacy HTTP 500 envelope remains unchanged.

Existing conversation/status/contact calculations, response-time samples, FCR and bot formulas retain their original cohort and body loading. This change does not make the whole Summary body-free, bounded in memory, or a transactionally consistent snapshot across owners. Only the three message counts share one database statement snapshot.

The legacy optional bounds, malformed-bound fallback, aliases, exact half-open predicate, PostgreSQL SQL-parameter ceiling to the stored microsecond grid, and SQLite stored-text comparison are retained. SQLite mixed-offset limitations remain. The phase44 root endpoint's required range, 400-day budget, storage guard, request deadline and error contract are not imported. No indexes, migrations, permissions, authentication, providers or deployment change.

## Preserved business-hours conflict

Document20 section5 says counts should not change with business_hours. The existing true path and its regression tests instead filter conversations by creation time and Inbox opening hours, then count their in-window messages. It neither filters each message by its own opening-hour membership nor falls back to all account messages for an empty selected cohort. This patch deliberately preserves that established path and bypasses the new aggregate for true. Resolving the conflict, and any corresponding duration/FCR/bot policy, remains separate product work.

## Verification and rollback

Regression tests cover zero/one/multiple newly created conversations, old-conversation messages, exact types and tenant scope, record rather than delivery semantics, one owner query, optional/empty/reversed/nanosecond intervals, legacy SQLite offsets, separate stores and private failures. Real V1/V2 router tests preserve authorization, aliases, JSON shape and existing CSV output, and compare default incoming/outgoing totals with summed phase44 series. Existing nonzero duration/FCR metrics and business-hours behavior are preserved. PostgreSQL tests use disposable loopback schemas with identical scenario helpers.

The external phase45 evidence directory records an actual failing-before assertion against phase44 plus the regression test, final immutable manifests/modes, ordinary/race/native logs and their precise selections. Focused success does not establish full CI, deployment or resolution of unrelated pending architecture/product policies.

Rollback the phase45 patch as one unit, including composition, local capability registrations, tests, docs and CI inventory. Keep phase44 and phase43 boundaries intact. No stored data needs rollback; reverting restores the known conditional-cohort counting defect.
