# Snooze expiry capture and worker lifecycle

This bounded repair connects the existing CON expiry command to ordinary durable
automation. The automation execution-mode default remains `legacy`; activation
claims and rollout gates are unchanged. No provider, webhook or push is added.

## Owner transaction

Each pass uses one cutoff and scans overdue non-null snooze deadlines in ID order.
CON locks each candidate, then rechecks account, ID, scanned object version, status
and deadline. A missing/deleting/open/resolved/pending row, a newer version, a null
deadline or an extended deadline cannot be overwritten by that scan. A winner
becomes open, clears its deadline, records the pass activity time and advances
exactly one owner version. Its immutable snoozed/open markers and changed fields
belong to one `con.conversation.changed.v1` fact in the same CON transaction.
The opened alias is derived by existing AUT ingestion, never a second owner fact.
Capture/activation journal failure rolls back the row and fact together.

Transactions are per row. If a later row fails, the command returns earlier
committed winners **and** the error. Routing broadcasts those committed winners
before returning the error. Later passes retry still-overdue rows; repeated or
concurrent workers cannot advance or broadcast an already-won version again.
PostgreSQL row locks coordinate independent repositories. SQLite lock contention
can return an error, leaving the failed row eligible for a later pass.

## Composition and lifecycle

Router composition starts expiry only after all owner ports and handlers are
configured, and only with a supplied `Options.BackgroundContext`. Main already
supplies and cancels this lifecycle. Nil-context embedded callers remain manually
driven. An already-cancelled context starts no worker. A live worker scans once
immediately, then waits one minute after each completed pass; invalid non-positive
explicit intervals use the same default. The completion-based delay prevents a
slow pass from building a backlog. Context reaches owner scans and transactions;
cancellation prevents subsequent passes. Generic warning logs make failed passes
observable without exposing database details.

Startup runs in both automation modes. This deliberately repairs the previously
unconnected product scheduler in legacy mode too. Legacy expiry still has no AUT
callback, and capture-disabled expiry needs no journal table. Durable mode uses
the same capture-enabled CON instance and existing bridge, so pending facts can
recover across a process restart. This does not promise durable WebSocket replay
or that cancellation joins unrelated preexisting application workers.

## Limits

Historical imports remain silent. The importer does not assign live snooze
deadlines, so imported snoozed rows alone are not expiry candidates. No backfill,
new status semantics, provider operation, public contract or snapshot codec is
introduced. The existing combined updated/opened rule batch, rule-ID order,
deferred sibling conditions, action CAS and replay receipts remain authoritative.
This repair does not imply blanket trigger parity or production activation.
