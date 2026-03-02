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
