# MSG message-create User admission (phase66)

`msg.message.create.v1` requires a fresh TEN account-membership decision for exact
`User` actors before any successful receipt replay, and again at direct handler
entry before conversation lookup or echo replay. This is defense in depth within
an authenticated module caller's actor context. It does not independently
authenticate actor claims or prevent a trusted module from claiming `System`.

A User actor ID must be positive and representable as Go `uint`; existing decimal
formatting and `auth_source` compatibility remain. The TEN query uses a decimal
string `user_id` so large IDs are not rounded through JSON float64. Missing,
revoked or account/user-mismatched membership fails closed. Invalid IDs return
`validation_failed`; unavailable, malformed JSON/type or canceled dependency reads
return `dependency_unavailable`; absent/null or unsuccessful membership returns
`resource_not_found`.

The existing authorizer hook runs before the local receipt transaction. A fresh
command invokes the same check at handler entry inside that transaction, following
the existing email dual-check pattern. These two fresh reads are intentional;
there is no cached authorization or new cross-module transaction protocol.
Membership can change after any observed check. Bare receipt-store calls remain
internal storage operations and are not an authorization boundary.

Existing independent outgoing User sender membership remains required. The actor
and sender may be distinct members. Exact non-User actor behavior is unchanged:
no new numeric-ID rule or TEN dependency for System/Contact/service actors, while
System commands with a User sender still retain their existing sender check.

Legacy command digest, receipt key scope and decoding are unchanged. An authorized
different actor may consume the same historical receipt; a nonmember User cannot.
Authorized receipt replay needs TEN but does not need CON or recheck the original
sender. Revocation blocks both cached receipt output and new-key echo replay
without creating messages, receipts or events; restored membership permits replay.

Message trim/100,000-byte content bounds, targets/accounts, private incoming
restriction, defaults, echo semantics and local `queued`/`saved` statuses remain.
There is no provider dispatch, new send UI, OPS send wrapper, or implicit conversion
of a canned preview into a send permit. OPS-06 and wider system delivery remain
partial. Stage acceptance requires focused ordinary/race/native runs, additive
native inventory review, frozen independent review and fresh complete gates.
