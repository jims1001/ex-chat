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
