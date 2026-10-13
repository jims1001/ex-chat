# Local CHN text transport ownership

This bounded extraction preserves the existing outbound text protocol described
in `outbound-text-transport.md`. It adds no provider, permission, route, queue,
retry policy, macro adoption, schema, or remote module-server composition.

## Ownership and composition

The combined router constructs `NewChannelTextOwner` from the CHN inbox store,
channel configuration and text transport, then injects its `channel.TextOwner`
interface into the MSG `ChannelOutboundService`. MSG retains message admission,
idempotency, durable attempts, claims, dispatch-start fencing, completion and
status application. CON/CUS still resolve and recheck exact recipient identity.

CHN alone resolves `OutboundConnection`, checks configuration/binding, decrypts
credentials, constructs provider requests, invokes transport, verifies Twilio
signatures and verifies/parses WhatsApp status envelopes. MSG receives bounded
credential-free binding/status values or an opaque `PreparedText` capability.
CHN has no MSG repository and performs no message writes. No owner shares a
transaction or performs provider I/O inside another owner's transaction.

The dependency and collaboration registries record the actual local synchronous
MSG -> CHN port and CHN-local encrypted connection read. They do not relabel it
as an implemented remote `chn.connection.status.v1` API. Source/AST guards check
method signatures, DTO fields, factories, owner reads, call sites, composition,
imports and the preparation/fence/send order.

## Preserved race and failure boundaries

Preparation rechecks routing and frozen connection/configuration, decrypts the
credential and captures the exact existing request without provider I/O. MSG
then durably starts dispatch before invoking `PreparedText.Send`. The private
function capability performs no second owner read or revalidation. A rotation
or route change after preparation cannot recall that captured in-flight request,
exactly as before. A MSG cancellation at the dispatch fence still prevents send.

Missing or changed owner bindings cancel locally. Decryption/configuration
failure leaves a claimed, undispatched attempt recoverable after lease expiry.
No failed/unknown provider result gains automatic retry. Twilio lookup remains
GET-by-known-SID; callbacks retain exact identity/signature checks. WhatsApp
checks all envelope bindings before applying any status and does not introduce
a new credential decryption requirement for status processing.

## Scope of the isolation claim

This is a typed in-process ownership boundary, not a sandbox, remote capability
protocol or process-security boundary. The opaque prepared function cannot be
JSON-serialized or formatted into a credential-bearing request. That guarantee
does not extend to arbitrary formatting/reflection of the owner object itself:
its private implementation retains channel configuration, as required to send.
Do not log owner/configuration objects or provider requests. No logging policy
or general-purpose formatter behavior is changed by this extraction.

Tests use injected local transports and synthetic credentials only. The owner
suite runs the same cases against five separate SQLite stores and five separate
PostgreSQL schemas: both providers, prepared-capability opacity, MSG fence
cancellation, rotation before/after preparation, decryption failure, Twilio
lookup/callback binding and WhatsApp envelope/binding semantics. Existing
transport/service/HTTP/router and architecture regressions remain applicable.
The PostgreSQL native manifest adds its exact ten emitted cases (root plus nine
children); a mapped or skipped test is not evidence that it passed.
