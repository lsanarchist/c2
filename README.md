# c2

A lightweight Command and Control (C2) framework written in Go.

## Architecture

```
┌────────────┐  HTTP check-in / result  ┌───────────────┐
│   Server   │◄─────────────────────────│     Agent     │
│  :8080     │─────────────────────────►│               │
└────────────┘                          └───────────────┘
```

The **server** exposes an HTTP API that agents connect to.  
The **agent** periodically checks in, executes any queued command, and ships the result back.

## Building

```bash
go build -o c2-server ./cmd/server
go build -o c2-agent  ./cmd/agent
```

## Running

### Start the server

```bash
./c2-server -addr :8080
```

### Start an agent

```bash
./c2-agent -server http://<server-ip>:8080 -id my-agent -interval 10s
```

## Server HTTP API

| Method | Path        | Description                                      |
|--------|-------------|--------------------------------------------------|
| GET    | /agents     | List all registered agents                       |
| POST   | /checkin    | Agent check-in (returns pending command if any)  |
| POST   | /result     | Agent posts a command result                     |
| GET    | /results    | List all stored results                          |
| POST   | /command    | Queue a command for an agent (query params below)|

### Queue a command

```
POST /command?agent_id=<id>&command_id=<id>&command=<shell command>
```

Example:

```bash
curl -X POST "http://localhost:8080/command?agent_id=agent-box1&command_id=1&command=whoami"
```

The next time that agent checks in it will receive and execute the command, then POST the result back to `/result`.

## Running tests

```bash
go test ./...
```
