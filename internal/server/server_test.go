package server_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lsanarchist/c2/internal/server"
	"github.com/lsanarchist/c2/pkg/protocol"
)

const (
	testOperatorToken = "operator-secret"
	testAgentAToken   = "agent-a-secret"
	testAgentBToken   = "agent-b-secret"
)

func sha256Hex(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func newTestServer(t *testing.T) (*server.Server, *httptest.Server) {
	t.Helper()
	s, err := server.New(server.Config{
		OperatorTokenHashes: []string{sha256Hex(testOperatorToken)},
		AgentTokenHashes: map[string]string{
			"agent-a": sha256Hex(testAgentAToken),
			"agent-b": sha256Hex(testAgentBToken),
		},
		RateLimitPerWindow: 1000,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(s.Handler())
	return s, ts
}

func newAuthedJSONRequest(t *testing.T, method, rawURL, token string, payload any) *http.Request {
	t.Helper()
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
	}
	req, err := http.NewRequest(method, rawURL, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

func doRequest(t *testing.T, req *http.Request) *http.Response {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

func checkIn(t *testing.T, ts *httptest.Server, token, id string) protocol.CheckInResponse {
	t.Helper()
	req := newAuthedJSONRequest(t, http.MethodPost, ts.URL+"/checkin", token, protocol.CheckIn{
		ID:       id,
		Hostname: "box1",
		OS:       "linux",
		Arch:     "amd64",
	})
	resp := doRequest(t, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("checkin status: %d", resp.StatusCode)
	}
	var out protocol.CheckInResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode checkin response: %v", err)
	}
	return out
}

func TestOperatorQueuesCommandAsJSONBody(t *testing.T) {
	_, ts := newTestServer(t)
	defer ts.Close()

	checkIn(t, ts, testAgentAToken, "agent-a")

	req := newAuthedJSONRequest(t, http.MethodPost, ts.URL+"/command", testOperatorToken, protocol.CommandRequest{
		AgentID:   "agent-a",
		CommandID: "cmd1",
		Command:   "echo hello",
	})
	resp := doRequest(t, req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	ciResp := checkIn(t, ts, testAgentAToken, "agent-a")
	if ciResp.CommandID != "cmd1" || ciResp.Command != "echo hello" {
		t.Fatalf("unexpected command response: %+v", ciResp)
	}
}

func TestMissingOrWrongCredentialRejected(t *testing.T) {
	_, ts := newTestServer(t)
	defer ts.Close()

	noAuthReq := newAuthedJSONRequest(t, http.MethodGet, ts.URL+"/agents", "", nil)
	resp := doRequest(t, noAuthReq)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for missing auth, got %d", resp.StatusCode)
	}

	wrongAuthReq := newAuthedJSONRequest(t, http.MethodGet, ts.URL+"/agents", "wrong", nil)
	resp = doRequest(t, wrongAuthReq)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong auth, got %d", resp.StatusCode)
	}
}

func TestAgentCannotCallOperatorEndpoint(t *testing.T) {
	_, ts := newTestServer(t)
	defer ts.Close()

	req := newAuthedJSONRequest(t, http.MethodGet, ts.URL+"/results", testAgentAToken, nil)
	resp := doRequest(t, req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestAgentCannotImpersonateAnotherAgent(t *testing.T) {
	_, ts := newTestServer(t)
	defer ts.Close()

	req := newAuthedJSONRequest(t, http.MethodPost, ts.URL+"/checkin", testAgentAToken, protocol.CheckIn{
		ID:       "agent-b",
		Hostname: "box1",
		OS:       "linux",
		Arch:     "amd64",
	})
	resp := doRequest(t, req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}

	req = newAuthedJSONRequest(t, http.MethodPost, ts.URL+"/result", testAgentAToken, protocol.Result{
		AgentID:   "agent-b",
		CommandID: "cmd-x",
		Output:    "x",
	})
	resp = doRequest(t, req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

func TestRevokedAgentCredentialDenied(t *testing.T) {
	s, ts := newTestServer(t)
	defer ts.Close()

	checkIn(t, ts, testAgentAToken, "agent-a")
	s.RevokeAgentCredential("agent-a")

	req := newAuthedJSONRequest(t, http.MethodPost, ts.URL+"/checkin", testAgentAToken, protocol.CheckIn{
		ID:       "agent-a",
		Hostname: "box1",
		OS:       "linux",
		Arch:     "amd64",
	})
	resp := doRequest(t, req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 after revocation, got %d", resp.StatusCode)
	}
}

func TestValidationRejectsInvalidJSONEmptyIDAndOversize(t *testing.T) {
	_, ts := newTestServer(t)
	defer ts.Close()

	checkIn(t, ts, testAgentAToken, "agent-a")

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/command", strings.NewReader(`{"agent_id":"agent-a"}{}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testOperatorToken)
	req.Header.Set("Content-Type", "application/json")
	resp := doRequest(t, req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for extra JSON object, got %d", resp.StatusCode)
	}

	req = newAuthedJSONRequest(t, http.MethodPost, ts.URL+"/checkin", testAgentAToken, protocol.CheckIn{
		ID:       "",
		Hostname: "box1",
		OS:       "linux",
		Arch:     "amd64",
	})
	resp = doRequest(t, req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty id, got %d", resp.StatusCode)
	}

	oversizedCommand := strings.Repeat("x", 3000)
	req = newAuthedJSONRequest(t, http.MethodPost, ts.URL+"/command", testOperatorToken, protocol.CommandRequest{
		AgentID:   "agent-a",
		CommandID: "cmd-oversized",
		Command:   oversizedCommand,
	})
	resp = doRequest(t, req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for oversized command field, got %d", resp.StatusCode)
	}
}

func TestContentTypeRequired(t *testing.T) {
	_, ts := newTestServer(t)
	defer ts.Close()

	payload, _ := json.Marshal(protocol.CheckIn{ID: "agent-a", Hostname: "box1", OS: "linux", Arch: "amd64"})
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/checkin", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testAgentAToken)
	resp := doRequest(t, req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415, got %d", resp.StatusCode)
	}
}

func TestConcurrentCheckInAndListAgentsSnapshot(t *testing.T) {
	_, ts := newTestServer(t)
	defer ts.Close()

	checkIn(t, ts, testAgentAToken, "agent-a")
	req := newAuthedJSONRequest(t, http.MethodPost, ts.URL+"/command", testOperatorToken, protocol.CommandRequest{
		AgentID:   "agent-a",
		CommandID: "cmd1",
		Command:   "echo secret",
	})
	resp := doRequest(t, req)
	resp.Body.Close()

	const iterations = 100
	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			checkIn(t, ts, testAgentAToken, "agent-a")
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			req := newAuthedJSONRequest(t, http.MethodGet, ts.URL+"/agents", testOperatorToken, nil)
			resp := doRequest(t, req)
			if resp.StatusCode != http.StatusOK {
				errCh <- fmt.Errorf("expected 200, got %d", resp.StatusCode)
				resp.Body.Close()
				return
			}
			var raw []map[string]any
			if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
				errCh <- err
				resp.Body.Close()
				return
			}
			resp.Body.Close()
			if len(raw) == 0 || raw[0]["id"] == "" {
				errCh <- fmt.Errorf("missing agent id")
				return
			}
			if _, hasCommand := raw[0]["command"]; hasCommand {
				errCh <- fmt.Errorf("agents DTO must not expose command text")
				return
			}
		}
	}()

	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestListenAndServeGracefulShutdown(t *testing.T) {
	s, err := server.New(server.Config{
		OperatorTokenHashes: []string{sha256Hex(testOperatorToken)},
		AgentTokenHashes:    map[string]string{"agent-a": sha256Hex(testAgentAToken)},
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	l.Close()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.ListenAndServe(ctx, server.ListenConfig{Addr: addr, DevHTTP: true})
	}()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		req := newAuthedJSONRequest(t, http.MethodGet, "http://"+addr+"/agents", testOperatorToken, nil)
		resp, reqErr := http.DefaultClient.Do(req)
		if reqErr == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case runErr := <-errCh:
		if runErr != nil {
			t.Fatalf("expected graceful shutdown, got: %v", runErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not shut down")
	}
}

func TestDevHTTPRequiresLoopback(t *testing.T) {
	s, err := server.New(server.Config{
		OperatorTokenHashes: []string{sha256Hex(testOperatorToken)},
		AgentTokenHashes:    map[string]string{"agent-a": sha256Hex(testAgentAToken)},
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	err = s.ListenAndServe(context.Background(), server.ListenConfig{Addr: "0.0.0.0:8080", DevHTTP: true})
	if err == nil {
		t.Fatal("expected error for non-loopback dev http")
	}
}
