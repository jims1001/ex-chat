# ex-chat

Customer conversation and live chat support platform backend in Go.

## Overview

`ex-chat` provides a multi-tenant, real-time customer messaging and support system modeled after modern support platform standards (Chatwoot compatible). It includes support for website widgets, customer relationship management, inbox routing, agent auto-assignment, real-time WebSocket communications, canned responses, and conversation analytics.

## Features

- **Multi-Tenant Accounts**: Complete data isolation across accounts, workspaces, and teams.
- **IAM & Permissions**: Role-based access control (Administrator, Agent) and secure JWT authentication.
- **Channels & Inboxes**: Support for website live chat widget, email, and API channels.
- **Customer CRM**: Contact profiles, channel identity mapping, and custom attributes.
- **Conversations & Messaging**: Rich conversation state machine (Open, Snoozed, Resolved, Pending), incoming/outgoing messages, and private internal agent notes.
- **Real-Time Communication**: WebSocket hub providing live event streaming for agents and visitors.
- **Auto-Assignment & Routing**: Automatic round-robin conversation routing based on agent availability.
- **Productivity & Reporting**: Canned responses, labeling system, and performance reports.

## Getting Started

### Prerequisites

- Go 1.25+
- Docker Desktop or Docker Engine with Compose (for PostgreSQL integration tests)

### Running the Server

```bash
go run ./cmd/server
```

### Running Tests

```bash
make test
```

`go test ./...` intentionally does not discover every integration test in this
workspace. Use the Make target above or the isolated script:

```bash
./scripts/test.sh
```

Run the same suite against local PostgreSQL 16 while also verifying that the
local Redis container starts and passes its authenticated health check:

```bash
make test-docker
```

The Docker test target uses its own Compose project, ports, and disposable data
volumes. It cleans them up when the test finishes and does not touch development
environment data.

Additional concurrency and stability checks:

```bash
make test-race
make test-stress STRESS_COUNT=10
make test-soak SOAK_DURATION=10m
```

The Docker services bind only to localhost. For manual startup, copy
`.env.example` to `.env`, replace every placeholder secret, then run:

```bash
docker compose up -d --wait
```

### Frontend prototype checks

```bash
cd Chatwoot客服系统界面原型
npm test
```

### Module schema preparation (ARCH-08 work in progress)

The runtime inventory is in `architecture/module_runtime.json`. It covers all
26 logical modules and records their existing storage models, declared
dependencies, default U1–U5 units, and stop/recovery policies. It is a draft;
the current server still starts all modules in one process with one database.

To prepare a **separate** module database, explicitly select its target:

```bash
go run ./cmd/module-migrate -module MSG -sqlite-path /path/to/isolated-message.db
# For PostgreSQL, supply MODULE_DB_DSN securely in the environment:
go run ./cmd/module-migrate -module MSG -postgres
```

This creates only that module's registered tables and local journal. Association
migration is disabled so GORM cannot implicitly create other owners' tables.
It does not migrate existing business records, implement public module calls,
or start an independently working business service. The combined server's
migration behavior is unchanged. API, MED and ORC currently have no registered
persistent domain model in this migration list; their local journal alone does
not establish runtime readiness.

Run the independent-store acceptance gate separately:

```bash
make test-module-isolation
```

The staged gate now passes for message creation with separate CON/MSG databases:
MSG records an atomic local change, invokes a CON-owned activity command, and
replays a pending command after a simulated CON outage. It also checks CON
mark-unread against a separate MSG store and MSG sender enrichment against
separate IAM/CUS stores. These are in-process owner ports, not deployed public
module APIs. A source inventory remains in
`scripts/audit-module-storage.py`; other repository operations still access
foreign-owned tables. Conversation deletion now uses a durable CON deletion
marker and retryable commands against each owner store; those commands remain
in-process adapters. Conversation detail reads now hydrate owner data through
CHN/CUS/IAM/TEN ports; conversation lists and label filters use the same owner
boundary. The staged gate also covers label relation validation through CUS
and attachment listing through MSG. BIL order updates validate conversation
references through CON. Ordinary `make test`, `make test-docker`,
and this staged gate do not establish ARCH-08 completion. Independent module startup, all
business paths, failure handling, scaling, upgrade, backup, rollback, and
topology acceptance still need implementation and verification.

Current evidence and the next repair order are recorded in
[ARCH-08 current evidence](Chatwoot完整文档/验收证据/E-ARCH-08-v14.md).
