# Independent SAML verification and request/replay primitives

## Status and security boundary

This is **not enabled SSO**. Both `POST /api/v1/auth/saml_login` and
`POST /omniauth/saml/callback` remain HTTP 503. Settings CRUD cannot activate this
implementation. No identity links, email matching, JIT users, memberships, role
mapping, MFA bypass, access tokens, or production IdP configuration are added.

`auth.SAMLServiceProvider` validates a deliberately narrow assertion profile.
`repository.SAMLRequestRepository` stores browser-bound request/replay facts in
IAM. They are currently independent primitives, exercised only by local tests.
A successful verifier call alone is **not a login**. Production integration must
resolve identity/session scope first and must add configuration lifecycle
fencing, browser cookie handling, admission rate/concurrency/storage limits,
local MFA, revocation, and session issuance before changing either HTTP guard.

The application's existing JWT authenticates a global user, not a tenant. A
member-only or email-matching SAML callback would therefore let one tenant IdP
impersonate a global/multi-tenant user. No identity/delegation policy is chosen by
this implementation. A tenant-scoped session design and its global-endpoint,
MFA, file, and WebSocket restrictions remain separate work.

## Trusted configuration and supported wire profile

Construct `SAMLSPConfig` only from explicit, independently trusted configuration:
nonzero account/version, exact IdP and SP entity IDs, HTTPS SSO/ACS URLs without
credentials/query/fragment, and one currently valid pinned RSA signing
certificate (2048–8192 bits). The constructor builds metadata locally; it does
not download metadata, follow URLs, inspect host/proxy headers, or trust callback
certificates. The SHA-256 configuration digest covers the trusted endpoint/entity
and certificate values plus profile identity. The caller supplies the version;
this code does not implement a durable configuration epoch or its lifecycle.

The request helper creates a fresh AuthnRequest with a persistent NameID policy,
AllowCreate=false, and ForceAuthn=true. The initial subset uses unsigned
HTTP-Redirect requests and HTTP-POST responses. IdPs requiring signed requests
are unsupported until separately configured signing-key support exists. The
helper never redirects an actual browser. Persist its request ID before using
the returned redirect URL. RelayState is exactly 32 random bytes encoded as
43-character unpadded base64url; it is an opaque correlation token, not a URL.

Accepted responses have exactly one directly signed plaintext Assertion inside
an **unsigned** Response envelope. Response-signed and dual-signed messages are
rejected because crewjam skips assertion-signature verification when the
Response is signed. This avoids confusing signature presence with verification.
The maintained library performs all canonicalization and signature checks.

The restricted profile requires:

- Exact protocol namespaces, version 2.0, nonempty unique Response/Assertion IDs
- Exact configured issuer, destination, audience, recipient, and pending request
- Exactly one persistent NameID, bearer confirmation, Conditions, single-audience
  restriction, and authentication statement
- RSA-SHA256/384/512 signatures and SHA256/384/512 digests; one local reference to
  the Assertion ID and enveloped-signature then exclusive-C14N transforms
- Required issue/expiry bounds, at most five-minute Conditions lifetime, fresh
  authentication, no expiration grace, and only a small future/not-before skew
- No unsupported assertions, nested identities, XML comments/directives,
  arbitrary XML processing instructions, duplicate singleton fields, duplicate
  attributes, or unrecognized signature reference/transform shapes

One scalar AttributeStatement may be present, but no attributes are returned or
used to map identity or authority. NameID qualifiers are preserved and validated.
Optional session expiration is honored. Nothing in AuthnContext satisfies the
application's local MFA requirement.

Hard limits precede expensive parsing: 1 MiB form body, 256 KiB decoded XML,
10,000 XML tokens, depth 32, 32 attributes per element, and 10,000 total
attributes. POST parsing rejects duplicate/unknown form fields, query input,
artifact binding, wrong content types, and unsupported encodings. ParsePOST
returns RelayState as **untrusted data**; a future handler must verify the
browser secret through durable lookup, never accept RelayState alone.

All verification failures expose a static error. Raw XML, subject data, cookies,
and parser diagnostics are not logged by these primitives. The result exposes
only immutable getters. Its zero value is invalid. ResponseID comes from the
unsigned but structurally checked envelope; assertion identity and correlation
come from the cryptographically verified assertion. Assertion-ID replay checks
are mandatory even when a ResponseID changes.

## Durable correlation and replay

`Prepare` persists a generated request ID, trusted account/configuration binding,
five-minute-or-shorter deadline, and independent SHA-256 digests of random relay
and browser secrets. Raw secrets are returned once and never stored. A future
HTTPS browser adapter must place the browser secret in a host-only Secure,
HttpOnly, SameSite=None cookie for the cross-site POST flow. A `__Host-` cookie
requires Path=/ and no Domain. Cookie/storage/rate controls are not active yet.

`Lookup` requires both secrets and only returns live, unconsumed correlation
state. `Consume` accepts the immutable verified assertion plus an explicit
current trusted configuration binding. It atomically claims the exact pending
request and inserts independent unique issuer/assertion and issuer/response
replay digests. Both configuration values and expiry are rechecked. Database
failure or a concurrent loser fails closed; there is no process-local fallback.
The issuer-wide replay namespace intentionally survives account/configuration
changes. No automatic security-state deletion or historical adoption occurs.

This primitive does **not** read enabled settings or serialize against settings
changes. The future configuration owner must lifecycle-fence creation and
consumption. Do not describe supplied-version equality as a current-settings
check. A crash after successful consumption requires a fresh login request.

IAM owns both new tables. Private owner snapshots preserve all digest/checkpoint
fields rather than serializing their empty public JSON. Restore invalidates
pending requests and preserves known replay facts, so users must initiate fresh
requests after recovery. An old backup is not evidence that an assertion was
never consumed on a lost database.

## Dependency pins and local evidence

Pinned: crewjam/saml v0.5.1, goxmldsig v1.6.1, etree v1.7.1. The explicit transitive
pins cover the 2026 signature-reference, SignedInfo memory, and etree text CPU
fixes. A bounded XML-token pass mitigates the shared round-trip validator's
allocation amplification. Do not downgrade those transitive pins when updating
crewjam.

Official references:
- https://github.com/crewjam/saml/blob/v0.5.1/service_provider.go
- https://github.com/russellhaering/goxmldsig/security/advisories/GHSA-479m-364c-43vc
- https://github.com/russellhaering/goxmldsig/security/advisories/GHSA-qhrp-hfff-vphr
- https://github.com/beevik/etree/security/advisories/GHSA-x27v-cqwr-28jx
- https://github.com/russellhaering/gosaml2/security/advisories/GHSA-66r8-42f7-4q74

Synthetic local fixtures use generated RSA certificates and the maintained
XML-DSig signer. No external identity provider is contacted. Focused and race
suites cover cryptographic/semantic negatives and durable replay. Optional
`TestPostgresSAML*` cases use `SAML_TEST_POSTGRES_DSN` for a disposable PostgreSQL
database. Passing these checks is evidence for these primitives only, not for
real-provider interoperability, browser cookie behavior, active SSO, or a
chosen identity/session-scope policy.
