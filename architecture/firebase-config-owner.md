# Private Firebase configuration ownership

Native Firebase push is composed only by the combined router. SYS owns the
persisted `FIREBASE_PROJECT_ID` and `FIREBASE_CREDENTIALS` values; NTF owns the
native transport, OAuth token cache, provider submission and delivery outcomes.

`PushService` accepts the local `FirebasePushConfigPort`, whose sole operation
accepts a context and returns the persisted project/credential pair and an error.
The router explicitly injects `NewSystemConfigRepository(db)`. The push service
constructor does not infer a SYS owner from its NTF database. The standalone
module server does not compose this capability.

The SYS method selects only the two fixed configuration keys with the caller's
context. It bounds each value to 32 KiB in the database projection and reads at
most three rows so malformed duplicate pairs can be rejected. It rejects invalid
types, duplicate keys, oversized values and invalid UTF-8, and returns a static
error. Existing SYS SQL logging suppression covers this read. It does not offer
an arbitrary-key reader, environment-default fallback, public configuration
payload or HTTP credential endpoint.

The source and collaboration registries record this as a separate local private
NTF -> SYS synchronous port. The runtime dependency inventory mirrors that edge;
NTF startup dependencies remain empty. No remote contract is added or aliased,
and `sys.configuration.get.v1` and `ntf.delivery.dispatch.v1` are unchanged.

The architecture gate checks the exact one-method signature, private consumer
field, owner repository, bounded query arguments, constructor, sole production
configuration-read call, sole combined-router composition and registered split
receiver files. It rejects method-value extraction, direct SYS configuration
model/repository/table use in NTF source scopes, hidden constructor composition
and standalone composition. The collaboration gate requires identical local
port inventories and rejects mapping the credential read onto a remote contract.

This is an in-process ownership boundary, not a process-security sandbox. The
transport necessarily receives the private values and keeps private key and
token material in memory; owner and transport objects must not be logged or
serialized. Separate SYS and NTF store tests establish the supported storage
boundary. Protocol and end-to-end tests establish transport behavior; static
architecture checks alone do not establish provider or device delivery.
