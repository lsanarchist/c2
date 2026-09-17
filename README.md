# c2

A lightweight Command and Control (C2) framework written in Go.

## Architecture

```
┌────────────┐ HTTPS check-in / result ┌───────────────┐
│   Server   │◄────────────────────────│     Agent     │
│127.0.0.1:8443│───────────────────────►│               │
└────────────┘                         └───────────────┘
```

The **server** exposes an HTTP API that agents connect to.
The **agent** periodically checks in, executes any queued command, and ships the result back.

## Security model

- **Operator credential** can queue commands and read `/agents` + `/results`.
- **Per-agent credential** is pre-issued by operator and bound to a single agent ID.
- Agent credential can only call `/checkin` and `/result`, and payload `id`/`agent_id` must match the bound ID.
- Credentials are passed as `Authorization: ****** and are never sent in URL params.
- Server stores only SHA-256 credential hashes from environment variables.
- Structured audit log records actor, agent ID, command ID, action, status, and timestamp.
- Command text, token values, and result output are not logged by default.

## Credentials and revocation

1. Generate random tokens (operator and one token per agent).
2. Compute SHA-256 hash for each token and configure server env vars:

```bash
export C2_OPERATOR_TOKEN_HASHES="$(printf '%s' "$OPERATOR_TOKEN" | sha256sum | awk '{print $1}')"
export C2_AGENT_TOKEN_HASHES="agent-a:$(printf '%s' "$AGENT_A_TOKEN" | sha256sum | awk '{print $1}'),agent-b:$(printf '%s' "$AGENT_B_TOKEN" | sha256sum | awk '{print $1}')"
```

3. Distribute only plaintext agent token to that specific agent (for example via `C2_AGENT_CREDENTIAL`).
4. Revoke access by removing an agent hash from `C2_AGENT_TOKEN_HASHES` and restarting the server.

## Building

```bash
go build -o c2-server ./cmd/server
go build -o c2-agent  ./cmd/agent
```

## Running

### Start the server (TLS)

```bash
./c2-server -addr 127.0.0.1:8443 -tls-cert /path/server.crt -tls-key /path/server.key
```

### Start an agent (TLS)

```bash
C2_AGENT_CREDENTIAL="$AGENT_A_TOKEN" ./c2-agent -server https://127.0.0.1:8443 -id agent-a -interval 10s -ca-cert /path/ca.pem -tls-server-name c2.local
```

### Optional dev HTTP mode (loopback only)

```bash
./c2-server -dev-http -addr 127.0.0.1:8080
C2_AGENT_CREDENTIAL="$AGENT_A_TOKEN" ./c2-agent -dev-http -server http://127.0.0.1:8080 -id agent-a
```

Authentication and authorization checks are still enforced in dev mode.

## Server HTTP API

| Method | Path      | Role      | Description                                     |
|--------|-----------|-----------|-------------------------------------------------|
| GET    | /agents   | operator  | List all registered agents                      |
| POST   | /checkin  | agent     | Agent check-in (returns pending command if any) |
| POST   | /result   | agent     | Agent posts command result                      |
| GET    | /results  | operator  | List all stored results                         |
| POST   | /command  | operator  | Queue a command for an agent (JSON body)        |

### Agent list response

`GET /agents` returns public agent metadata only:

- `id`, `hostname`, `os`, `arch`, `last_seen`
- `has_pending` (boolean flag showing whether a command is queued)

Pending command text is intentionally not returned by this endpoint.

### Queue a command

```http
POST /command
Content-Type: application/json
Authorization: ******

{"agent_id":"agent-a","command_id":"cmd-1","command":"whoami"}
```

The next time that agent checks in it will receive and execute the command, then POST the result back to `/result`.

## Validation and limits

- JSON-only endpoints enforce `Content-Type: application/json`.
- Empty IDs, malformed IDs, oversized fields, oversized body, and trailing JSON data are rejected.
- Request rate limiting is enabled per authenticated actor.
- HTTP server uses read/write/idle timeouts.

## Running tests

```bash
go test ./...
```
