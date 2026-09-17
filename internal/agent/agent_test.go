package agent_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/lsanarchist/c2/internal/agent"
	"github.com/lsanarchist/c2/pkg/protocol"
)

// fakeServer mimics the C2 server for agent tests.
type fakeServer struct {
	mu       sync.Mutex
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
		f.mu.Lock()
		f.checkIns = append(f.checkIns, ci)
		resp := f.response
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	case "/result":
		var res protocol.Result
		if err := json.NewDecoder(r.Body).Decode(&res); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.results = append(f.results, res)
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

func TestAgentCheckIn(t *testing.T) {
	fake := &fakeServer{}
	ts := httptest.NewServer(fake)
	defer ts.Close()

	a, err := agent.New("test-agent", "myhost", ts.URL, time.Hour)
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}

	if err := a.RunOnce(context.Background()); err != nil {
		t.Fatalf("run once: %v", err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
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

	a, err := agent.New("test-agent", "myhost", ts.URL, time.Hour)
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	if err := a.RunOnce(context.Background()); err != nil {
		t.Fatalf("run once: %v", err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
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

func TestNewValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		id        string
		serverURL string
		interval  time.Duration
	}{
		{name: "empty id", id: "", serverURL: "http://127.0.0.1:8080", interval: time.Second},
		{name: "invalid url", id: "agent1", serverURL: "://bad", interval: time.Second},
		{name: "missing host", id: "agent1", serverURL: "http://", interval: time.Second},
		{name: "unsupported scheme", id: "agent1", serverURL: "ftp://127.0.0.1:8080", interval: time.Second},
		{name: "non-positive interval", id: "agent1", serverURL: "http://127.0.0.1:8080", interval: 0},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := agent.New(tc.id, "host", tc.serverURL, tc.interval); err == nil {
				t.Fatalf("expected error for invalid config")
			}
		})
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	fake := &fakeServer{}
	ts := httptest.NewServer(fake)
	defer ts.Close()

	a, err := agent.New("test-agent", "myhost", ts.URL, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- a.Run(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("agent run did not stop after context cancel")
	}
}
