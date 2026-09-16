package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

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

func listAgents(t *testing.T, ts *httptest.Server) []server.AgentInfo {
	t.Helper()
	resp, err := http.Get(ts.URL + "/agents")
	if err != nil {
		t.Fatalf("list agents: %v", err)
	}
	defer resp.Body.Close()

	var agents []server.AgentInfo
	if err := json.NewDecoder(resp.Body).Decode(&agents); err != nil {
		t.Fatalf("decode agents: %v", err)
	}
	return agents
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

	agents := listAgents(t, ts)
	if len(agents) != 0 {
		t.Errorf("expected 0 agents, got %d", len(agents))
	}
}

func TestSendCommandAndReceiveViaCheckIn(t *testing.T) {
	_, ts := newTestServer()
	defer ts.Close()

	checkIn(t, ts, protocol.CheckIn{ID: "agent1", Hostname: "box", OS: "linux", Arch: "amd64"})

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

	r := checkIn(t, ts, protocol.CheckIn{ID: "agent1", Hostname: "box", OS: "linux", Arch: "amd64"})
	if r.Command != "echo hello" {
		t.Errorf("expected command 'echo hello', got %q", r.Command)
	}
	if r.CommandID != "cmd1" {
		t.Errorf("expected command_id 'cmd1', got %q", r.CommandID)
	}

	r2 := checkIn(t, ts, protocol.CheckIn{ID: "agent1", Hostname: "box", OS: "linux", Arch: "amd64"})
	if r2.Command != "" {
		t.Errorf("command should be cleared after dispatch, got %q", r2.Command)
	}
}

func TestResultEndpoint(t *testing.T) {
	_, ts := newTestServer()
	defer ts.Close()

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

func TestConcurrentCheckInAndListAgentsSnapshot(t *testing.T) {
	_, ts := newTestServer()
	defer ts.Close()

	checkIn(t, ts, protocol.CheckIn{ID: "agent1", Hostname: "box", OS: "linux", Arch: "amd64"})
	req, _ := http.NewRequest(http.MethodPost,
		ts.URL+"/command?agent_id=agent1&command_id=cmd1&command=echo+secret", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("send command: %v", err)
	}
	resp.Body.Close()

	const iterations = 200
	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			checkIn(t, ts, protocol.CheckIn{
				ID:       "agent1",
				Hostname: fmt.Sprintf("box-%d", i),
				OS:       "linux",
				Arch:     "amd64",
			})
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			agents := listAgents(t, ts)
			if len(agents) == 0 {
				errCh <- fmt.Errorf("expected at least one agent")
				return
			}
			if agents[0].ID == "" {
				errCh <- fmt.Errorf("expected agent id in DTO")
				return
			}
		}
	}()

	wg.Wait()
	close(errCh)
	for runErr := range errCh {
		if runErr != nil {
			t.Fatal(runErr)
		}
	}

	agents := listAgents(t, ts)
	if len(agents) != 1 {
		t.Fatalf("expected exactly one agent, got %d", len(agents))
	}
	if agents[0].HasPending {
		t.Fatalf("expected no pending command after check-ins")
	}

	agentsBodyResp, err := http.Get(ts.URL + "/agents")
	if err != nil {
		t.Fatalf("get agents body: %v", err)
	}
	defer agentsBodyResp.Body.Close()
	var raw []map[string]any
	if err := json.NewDecoder(agentsBodyResp.Body).Decode(&raw); err != nil {
		t.Fatalf("decode raw agents: %v", err)
	}
	if _, hasCommand := raw[0]["command"]; hasCommand {
		t.Fatal("agents DTO must not expose pending command text")
	}
}

func TestListenAndServeGracefulShutdown(t *testing.T) {
	s := server.New()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	l.Close()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.ListenAndServe(ctx, addr)
	}()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		resp, reqErr := http.Get("http://" + addr + "/agents")
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
			t.Fatalf("expected graceful shutdown, got error: %v", runErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not shut down after context cancel")
	}
}
