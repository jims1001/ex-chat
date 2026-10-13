# Macro and Public automation owner composition

Phase46 repairs two application-composition gaps in the opt-in durable monolith.
The router now passes its existing CON, MSG, label, account and team repositories
to `NewMacroRepositoryWithOwnerPorts`, and its existing inbox, contact, CON and MSG
repositories to `NewPublicRepositoryWithOwnerPorts`. Only the router's shared CON
and MSG owners receive durable capture activation. Detached default constructors
therefore previously committed these mutations without capturing their AUT facts.
The default constructors and `AUTOMATION_EXECUTION_MODE=legacy` remain unchanged.

## Captured paths and existing authority

The real Account macro execute route now uses the shared CON owner for its final
macro mutation and labels, and the shared MSG owner for local macro messages.
The Public status route and Public conversation custom-attribute merge use that
same CON owner. Existing capture, source admission, immutable rule snapshots,
owner-command receipts and asynchronous AUT action execution are reused; no new
route, permission, capability or execution-history field is introduced.

Current Account membership, macro visibility and tenant checks still run in the
existing handlers/repositories. Public routes still require the signed visitor
session and matching live contact/inbox mapping and conversation ownership.
Injection does not assert broader action-level Macro permission coverage.

The router applies no additional transport or activity configuration to the
shared repositories. MSG uses the same CON activity-capable repository type and
IAM/CUS reference adapters; labels use the same CON owner adapter. Account/team
repositories share the same user repository type. In legacy/default mode, local
macro messages keep their current outgoing/sent state and actual user sender,
private notes stay private, label and status effects remain, and these macro and
Public mutations still have no immediate automation callback to duplicate.
No provider delivery is added or implied by a local `sent` message state.

## Transaction and causal limits

Capture failure rolls back the corresponding owner transaction. A status-only
macro or Public status update commits the mutation and exactly one CON fact
atomically. AUT can then admit a frozen rule and apply a real priority effect;
redelivery/restart does not duplicate that execution or effect. Repeating a
resolved write retains a generic updated fact and version increment, but does not
invent a new resolved transition alias.

A complete multi-action macro is not one transaction. Label changes and messages
commit through their owners before the final `ApplyMacroMutation` call, which
increments the CON version even when the macro had only message/label actions.
A later owner failure does not undo earlier successful message or label commits.
An earlier captured label/message fact may already target an older CON version
by the time AUT runs. The existing ordinary-root version fence deliberately
returns `target_version_stale`; this repair does not rebase it onto newer state.
A successful final fact can still execute its matching rule. Capture coverage is
not a promise that every multi-action event executes successfully.

These ordinary facts retain the existing System/CON or System/monolith actor,
correlation and causation contract. They do not acquire invented Macro origin
execution IDs, depth or causal-family ancestry. The API caller's message sender
remains separate from the owner fact's actor. Broader Macro actor propagation,
whole-macro transactions, ancestry and durable Macro execution history are outside
this change.

## Verification

`TestAutomationOwnerWiring` and `TestPostgresAutomationOwnerWiring` exercise the
same twelve scenarios through `router.SetupRouter` without a background worker.
They cover both status routes, frozen rules and one priority effect, replay,
repeated-status behavior, four capture-failure rollbacks, earlier multi-action
facts rejected as stale, partial owner commits, Public custom attributes, tenant
and current authority rejection, and legacy/default-off effects.

The two status cases fail against the previous router because both committed
updates lack CON facts. The reviewed native selection requires all thirteen
emitted results (root plus twelve leaves), with no skipped or missing child.
Normal/race/focused-native evidence and the full CI gates are distinct obligations.
No database activation, provider call, external deployment, frontend acceptance,
load test, new permission design or blanket document completion is claimed.
