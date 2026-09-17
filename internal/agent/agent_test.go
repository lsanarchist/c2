package agent_test

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/lsanarchist/c2/internal/agent"
	"github.com/lsanarchist/c2/pkg/protocol"
)

const testCredential = "agent-cred"

// fakeServer mimics the C2 server for agent tests.
type fakeServer struct {
	mu            sync.Mutex
	requiredAuth  string
	checkIns      []protocol.CheckIn
	results       []protocol.Result
	response      protocol.CheckInResponse
	checkInStatus int
	resultStatus  int
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+f.requiredAuth {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	switch r.URL.Path {
	case "/checkin":
		if f.checkInStatus != 0 {
			http.Error(w, "forced", f.checkInStatus)
			return
		}
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
		if f.resultStatus != 0 {
			http.Error(w, "forced", f.resultStatus)
			return
		}
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
	fake := &fakeServer{requiredAuth: testCredential}
	ts := httptest.NewServer(fake)
	defer ts.Close()

	a, err := agent.New("test-agent", "myhost", ts.URL, time.Hour, agent.Options{Credential: testCredential, AllowHTTP: true})
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
		requiredAuth: testCredential,
		response: protocol.CheckInResponse{
			CommandID: "cmd1",
			Command:   "echo hello",
		},
	}
	ts := httptest.NewServer(fake)
	defer ts.Close()

	a, err := agent.New("test-agent", "myhost", ts.URL, time.Hour, agent.Options{Credential: testCredential, AllowHTTP: true})
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

func TestAgentUnauthorizedResponseFailsRunOnce(t *testing.T) {
	fake := &fakeServer{requiredAuth: testCredential, checkInStatus: http.StatusUnauthorized}
	ts := httptest.NewServer(fake)
	defer ts.Close()

	a, err := agent.New("test-agent", "myhost", ts.URL, time.Hour, agent.Options{Credential: testCredential, AllowHTTP: true})
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}

	if err := a.RunOnce(context.Background()); err == nil {
		t.Fatal("expected unauthorized error")
	}
}

func TestNewValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		id        string
		serverURL string
		interval  time.Duration
		opts      agent.Options
	}{
		{name: "empty id", id: "", serverURL: "https://127.0.0.1:8443", interval: time.Second, opts: agent.Options{Credential: testCredential}},
		{name: "invalid url", id: "agent1", serverURL: "://bad", interval: time.Second, opts: agent.Options{Credential: testCredential}},
		{name: "missing host", id: "agent1", serverURL: "https://", interval: time.Second, opts: agent.Options{Credential: testCredential}},
		{name: "unsupported scheme", id: "agent1", serverURL: "ftp://127.0.0.1:8080", interval: time.Second, opts: agent.Options{Credential: testCredential}},
		{name: "non-positive interval", id: "agent1", serverURL: "https://127.0.0.1:8443", interval: 0, opts: agent.Options{Credential: testCredential}},
		{name: "empty credential", id: "agent1", serverURL: "https://127.0.0.1:8443", interval: time.Second, opts: agent.Options{}},
		{name: "http disabled", id: "agent1", serverURL: "http://127.0.0.1:8080", interval: time.Second, opts: agent.Options{Credential: testCredential}},
		{name: "http non-loopback", id: "agent1", serverURL: "http://192.168.1.10:8080", interval: time.Second, opts: agent.Options{Credential: testCredential, AllowHTTP: true}},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := agent.New(tc.id, "host", tc.serverURL, tc.interval, tc.opts); err == nil {
				t.Fatalf("expected error for invalid config")
			}
		})
	}
}

func TestTLSWrongCertificateFails(t *testing.T) {
	server := httptest.NewTLSServer(&fakeServer{requiredAuth: testCredential})
	defer server.Close()

	cert := server.Certificate()
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	caFile, err := os.CreateTemp(t.TempDir(), "ca-*.pem")
	if err != nil {
		t.Fatalf("create temp ca file: %v", err)
	}
	if _, err := caFile.Write(certPEM); err != nil {
		t.Fatalf("write ca file: %v", err)
	}
	if err := caFile.Close(); err != nil {
		t.Fatalf("close ca file: %v", err)
	}

	a, err := agent.New("test-agent", "myhost", server.URL, time.Hour, agent.Options{
		Credential:    testCredential,
		CACertFile:    caFile.Name(),
		TLSServerName: "wrong-server-name.local",
	})
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}

	if err := a.RunOnce(context.Background()); err == nil {
		t.Fatal("expected TLS verification error")
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	fake := &fakeServer{requiredAuth: testCredential}
	ts := httptest.NewServer(fake)
	defer ts.Close()

	a, err := agent.New("test-agent", "myhost", ts.URL, 20*time.Millisecond, agent.Options{Credential: testCredential, AllowHTTP: true})
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

func TestCustomCARootAccepted(t *testing.T) {
	fake := &fakeServer{requiredAuth: testCredential}
	ts := httptest.NewTLSServer(fake)
	defer ts.Close()

	cert := ts.Certificate()
	if _, err := x509.ParseCertificate(cert.Raw); err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	caFile, err := os.CreateTemp(t.TempDir(), "ca-*.pem")
	if err != nil {
		t.Fatalf("create temp ca file: %v", err)
	}
	if _, err := caFile.Write(certPEM); err != nil {
		t.Fatalf("write ca file: %v", err)
	}
	if err := caFile.Close(); err != nil {
		t.Fatalf("close ca file: %v", err)
	}

	a, err := agent.New("test-agent", "myhost", ts.URL, time.Hour, agent.Options{
		Credential: testCredential,
		CACertFile: caFile.Name(),
	})
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}

	if err := a.RunOnce(context.Background()); err != nil {
		t.Fatalf("expected successful TLS check-in, got: %v", err)
	}
}
