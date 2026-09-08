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

- Go 1.22+

### Running the Server

```bash
go run ./cmd/server
```

### Running Tests

```bash
go test -v ./...
```
