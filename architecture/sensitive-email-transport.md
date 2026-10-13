# Bounded credential-mail submission

Email confirmation, password reset and agent invitation all use the same built-in
SMTP transport when the service applies its private sensitive-mail context marker.
The transport lives in `internal/service/smtp_confirmation_transport.go`; its shared
entry points are `sendSensitiveSMTP` and `submitSensitiveSMTP`. Ordinary mail keeps
its previous SMTP path, context behavior, retry eligibility and diagnostics.

## Transport boundary

- The service gives each synchronous send a total 30-second deadline; an earlier
  caller deadline wins. The built-in sender also bounds a direct marked call
- Dialing uses `net.Dialer.DialContext`. A single absolute socket deadline covers
  the greeting, EHLO, authentication, MAIL, RCPT, DATA body and acknowledgement,
  TLS negotiation and QUIT. Cancellation closes the owned socket
- Port 465 selects implicit TLS; other ports negotiate STARTTLS. Standard
  `crypto/tls` and `net/smtp` perform TLS, hostname verification and authentication
- TLS is mandatory for every configured host except exact case-insensitive
  `localhost` or a parsed loopback IP without a zone. DNS aliases, suffixes,
  wildcard addresses and private-network addresses receive no plaintext exception
- Without STARTTLS, the remote-host path stops before AUTH, MAIL, RCPT or DATA,
  even when SMTP authentication is disabled. Failed TLS is never downgraded
- Transport errors exposed to callers are static or context errors. Provider
  responses can contain credentials and are never wrapped, persisted or logged

Custom `EmailSender` implementations must honor the provided context. A sender
that ignores cancellation can still block its synchronous caller and is unsupported
for credential mail. No background send goroutine is used to pretend such a sender
has stopped. A nil result returned after cancellation or deadline expiry is rejected
by the service, so that late result cannot authorize a proof.

## Submission and proof lifecycle

The existing conservative success contract is retained: the final DATA reply and
QUIT must both succeed before the context ends. DATA acceptance alone means that
the provider may already have queued a message. A later lost QUIT acknowledgement
still yields an unknown application outcome, not a claim that no email was sent.
`submitted` is an SMTP submission acknowledgement, never mailbox delivery.

Reset retains its error-only service API and generic HTTP response. Its existing
`password reset email delivery failed` error is intentionally redacted and must
not be interpreted as proof that the provider did not accept the message. An error
or late nil return leaves the stored reset digest unissued and unusable. Invitation
keeps its `submitted`, explicit `captured_mock`, and `delivery_unknown` states;
unknown outcomes leave the invitation unusable. Callers explicitly request a fresh
reset or reissue an invitation rather than replaying the uncertain credential.

Sensitive services send directly and create no `EmailLog` or ordinary retry item.
This change adds no persistent queue, delivery worker, retry, endpoint, owner edge,
database migration or proof-recovery policy. Existing IAM/TEN proof guards and
confirmation legacy cleanup continue to apply.

## Local verification and deployment scope

Protocol fixtures bind only ephemeral loopback ports and exercise cancellation
and deadlines across the greeting, TLS, AUTH, MAIL, RCPT, DATA and QUIT. Verified
TLS fixtures use a generated temporary CA in an isolated test process; production
trust settings are unchanged. Rejected roots and mismatched names prevent secret
transmission. Actual HTTP-handler tests exercise successful and uncertain SMTP
outcomes, late custom senders, proof usability, redaction and absence of retries.

No live provider or mailbox delivery is claimed. Deployment operators must supply
SMTP certificates trusted by the normal system store. Remote plaintext relays
that formerly received reset or invitation credentials now fail closed and need
working verified TLS before those requests can complete. Frontend routes, URL
policy, invitation enablement, SAML, authentication policy and IAM recovery remain
outside this change.
