# Call metadata GET authorization

The existing account REST Call reads now enforce the permission rule documented
in product/API document 15, section 9 (ordinary members can read only calls they
handled whose conversation remains accessible), using document 21, section 6
for current conversation visibility. This is a metadata GET boundary, not a new
RTM runtime capability or an authorization policy for recording bytes.

The covered routes are:

- `GET /api/v1/accounts/:account_id/calls`
- `GET /api/v1/accounts/:account_id/calls/:id`
- `GET /api/v1/accounts/:account_id/calls/:id/whatsapp`
- `GET /api/v1/accounts/:account_id/whatsapp_calls/:id`
- `GET /api/v1/accounts/:account_id/calls/:id/candidates`

The WhatsApp detail routes preserve numeric Call ID precedence and provider-ID
fallback only when the numeric record is absent. A failed numeric lookup is a
static server error; an unauthorized numeric record never triggers fallback to
a different provider-ID record. Authorization precedes caller composition and
candidate lookup. Unauthorized details return the same static `404 Call not
found` response as missing records. Lists omit inaccessible rows without counts
or placeholders; a dependency failure returns a static error with no partial
list. No new paging, filtering or response envelope is introduced. An ordinary
member cannot change the authenticated actor through `agent_id` or other query
parameters.

## Current identity and roles

Authentication still validates the token against the current IAM user. Call
reads require that user's nonzero ID to agree with the middleware user ID. They
then reload TEN membership for the requested account rather than trusting a
prior middleware role object or global `User.Role`.

An account `administrator` retains account-wide metadata access. A resolved
custom role with `PermissionReportView` also retains account-wide access. The
permission decision uses the same `CheckPermissionInJSON` evaluator and target
as `AccountRepository.HasPermission`, from one fresh TEN-owned snapshot:
`report_view`, `report_manage`, all existing Chinese report aliases and the
existing `administrator` permission token retain their meaning.

An explicit custom-role ID must resolve in the current account. A missing,
zero or foreign ID cannot fall back to its role name or to plain Agent access.
Without an explicit ID, the existing account-local role-name convention is
supported only when one role resolves. Missing/ambiguous names and malformed
permission JSON do not grant access. These strict reads are specific to Call
GETs; they do not change generic membership endpoints or role mutations.

## Ordinary-member rule

All of the following must hold:

1. `Call.AgentID` is the authenticated IAM `User.ID`, not `AccountUser.ID`.
2. A nonzero stored `ConversationID` resolves unambiguously under the rule below.
3. The Call, Conversation, Inbox and Contact agree on account and references;
   the Inbox and Contact still exist within that account.
4. The user is currently an Inbox member OR a member of the Conversation's
   current account-owned Team. Assignment or participation alone cannot satisfy
   this base scope.
5. A plain `agent` is allowed within that scope. A custom role also needs one of
   the following existing permission sets:

| Custom permission | Additional constraint within base scope |
| --- | --- |
| `conversation_manage` | All conversations in base scope |
| `conversation_unassigned_manage` | Unassigned or assigned to this user |
| `conversation_participating_manage` | Assigned to this user or this user is a current participant |
| Both latter permissions | Union of their allowed conversations |
| None | No conversation access |

The existing permission evaluator's conversation aliases retain their meaning.
Owner facts are read for each request; later requests observe removed account,
Inbox, Team or participant memberships and changed roles/assignments. The owner
queries are separate reads, not one cross-store transaction or a promise of
linearizable revocation during a request. Each owner accesses only its own
storage; no cross-owner SQL join or hydration of IAM users is required. Narrow
collaboration bindings use existing TEN/CHN/CUS/CON queries and events; no module
capability, dependency graph or runtime declaration is added.

## Historical conversation references

Outbound documentation describes `conversation_id` as a display ID, while the
existing writer stores that value directly and recording ownership checks use
it as an internal `Conversation.ID`. This repair does not reinterpret or migrate
historical values.

Ordinary reads require a relationship-consistent internal-ID match. There is
no display-ID fallback. If a different Conversation in the same account with
the same Inbox and Contact has that value as its `display_id`, the reference is
ambiguous and ordinary reads fail closed. The same row having equal internal
and display IDs is valid. A display-ID collision with a different account,
Inbox or Contact does not make an otherwise consistent internal match ambiguous.

Unknown/nil/zero historical `AgentID`, absent conversations and unprovable
references remain unknown. Current assignee/participants do not reconstruct a
historical handler. Administrators and report-authorized members retain
account-wide reads of those stored records.

## Preserved output and separate disclosure boundaries

Authorized responses retain their current fields, including SDP offer/answer,
provider IDs, recording URL/SID, duration, caller and ICE candidate data. Signed
or external recording URL query parameters are preserved exactly. No whole
User/Inbox/Conference object, provider configuration or `Conference.Token` is
added. Handler errors are static. Read queries involved in authorization and
Call/candidate lookup suppress database SQL/error traces to avoid exposing
provider identifiers or raw dependency errors in logs.

This change intentionally leaves these separate boundaries in place:

- Call mutations and acceptance-like endpoints still have their existing
  authorization and may return Call metadata. This GET repair alone does not
  establish comprehensive Call confidentiality. Mutation authorization and
  first-accept/ownership transition design require a separate change.
- Ordinary account-authenticated `/uploads/*filepath` still uses its existing
  account/member ownership rules. A member who already knows a recording path
  can retain access under those rules; hiding its Call metadata does not revoke
  that knowledge or tighten playback authorization.
- Exact-key, expiry-bound signed uploads remain bearer capabilities and are not
  invalidated by a later membership change. External recording URLs retain the
  provider's capability and expiry semantics.
- No future `AgentID` assignment, acceptance state, historical ownership,
  recording-file migration, cleanup or audio download rule changes here.
- Existing Conversation endpoints are not repaired by this Call-specific
  visibility check. REST pagination/filter contract work remains separate.

## Validation scope

The focused tests exercise the real router/Auth/Tenant role and scope matrix,
report aliases, fresh revocation, actor spoofing, strict custom roles, tenant and
reference mismatches, historical ambiguity, numeric/provider aliases, list and
candidate nondisclosure, and preserved authorized payloads. Separate handler
tests run the policy with distinct TEN/CHN/CUS/CON/RTM stores (without foreign
owner tables), inject dependency failures, verify static failures/logs and prove
that an unsuccessful later list check emits no earlier authorized rows.

The bounded native PostgreSQL regression repeats current role/membership,
Inbox-or-Team/custom scope, owner/reference integrity, ambiguity and tenant
isolation checks in separate owner schemas. Its root and five children are
registered as six exact native obligations.

Existing voice/Call lifecycle, recording upload/storage, private upload and
exact-key signature regressions remain applicable. Focused test results do not
replace the combined core/native gates against the final integrated tree.
