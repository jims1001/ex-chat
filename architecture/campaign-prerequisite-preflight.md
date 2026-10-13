# Campaign prerequisite-read preflight (phase59)

This bounded correction applies to the existing local OPS campaign dispatch
service and explicit trigger route. It is not a new runtime contract, deployment,
provider integration, consent policy, atomic delivery claim or retry protocol.

## Before dispatch

`TriggerCampaign` finishes its existing account contact pagination, audience
selection and, for ongoing campaigns only, all delivery-deduplication reads
before the first conversation, message or delivery effect. Any failed page,
count, preload or audience/dedup read aborts the invocation with zero sent and a
recognizable `ErrCampaignPrerequisiteRead`. Rows returned with an error are not
accepted as a successful audience. Campaign storage failures are also classified;
a genuine missing campaign retains its existing business rejection.

`TriggerOngoingCampaignForContact` completes its existing active ongoing campaign
list, contact lookup and all required per-campaign dedup/audience reads before
sending any campaign. A later campaign's prerequisite failure cannot send an
earlier eligible campaign. Read errors return zero plus the recognizable error;
an empty campaign list or genuinely absent contact still returns zero and nil.
Already-delivered campaigns still skip their unnecessary audience query.

Existing tenant/inbox predicates, contact pagination/order and campaign iteration
order remain. Bulk and per-contact audience formats deliberately remain different:
for example, the bulk typed `contact_ids` form is not newly added to the
per-contact selector. No additional one-off deduplication is introduced. Zero
inbox/contact inputs remain per-contact no-ops. Empty bulk account audiences keep
the existing error; filtered-empty audiences keep zero/nil without changing
status. Healthy all-delivered bulk ongoing campaigns retain the existing final
status write. No new account/contact/consent eligibility policy is inferred.

## HTTP and optional triggers

The explicit V1 trigger returns generic HTTP 500 for prerequisite unavailability,
without raw database details. Existing business rejections retain HTTP 400.
A missing campaign service returns HTTP 500 rather than false completion.
Existing authentication, automation permission and tenant boundaries remain.
The healthy response's legacy `status: completed` field is deliberately unchanged:
it is not authoritative evidence of the stored status or final provider delivery.

Agent, widget and public conversation creation invoke optional ongoing campaigns
after primary creation has committed. Campaign preflight failure must not turn
that primary success into an error, rollback or retry. Widget primary incoming
messages remain committed. No campaign message/delivery is produced on such a
preflight failure. Existing post-commit optional-call behavior is preserved.

## Limits and verification

Zero effects is a preflight-failure guarantee only. After dispatch starts,
existing downstream partial-side-effect behavior remains. In particular, this
change does not mechanically propagate ignored delivery/status saves after a
message has already been sent: doing so would expose an unsafe scheduler resend
path. Final status truthfulness, atomic claims, concurrent duplicate prevention,
restart/retry/cancellation, provider delivery, opt-in/opt-out and full OPS
acceptance require separate contracts and remain open.

Contact paging and cross-owner reads are not one transaction or frozen snapshot;
concurrent changes can still alter a healthy invocation's observations.
Separate OPS/CUS/CON/MSG fixtures, more-than-100-contact pagination, injected
first/late reads and rows-plus-error results, selector parity, tenant scope,
primary creation preservation and real-router errors are registered for SQLite,
race and native PostgreSQL verification. Coverage inventory is an obligation,
not a passing result. Complete core/native gates and independent review remain
required on the final source.
