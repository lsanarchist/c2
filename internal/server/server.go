// Package server implements the C2 HTTP server that manages agents and commands.
package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/lsanarchist/c2/pkg/protocol"
)

const (
	defaultMaxBodyBytes       int64 = 64 * 1024
	defaultReadHeaderTimeout        = 5 * time.Second
	defaultReadTimeout              = 10 * time.Second
	defaultWriteTimeout             = 10 * time.Second
	defaultIdleTimeout              = 60 * time.Second
	defaultRateLimitPerWindow       = 30
	defaultRateLimitWindow          = time.Second

	maxIDLength      = 128
	maxHostname      = 256
	maxOSArchLength  = 64
	maxCommandLength = 2048
	maxOutputLength  = 16 * 1024
	maxErrorLength   = 1024
)

var idPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// Agent holds runtime state for a connected agent.
type Agent struct {
	ID       string
	Hostname string
	OS       string
	Arch     string
	LastSeen time.Time

	commandID string // pending command ID
	command   string // pending command text
}

// AgentInfo is the API DTO exposed from GET /agents.
type AgentInfo struct {
	ID         string    `json:"id"`
	Hostname   string    `json:"hostname"`
	OS         string    `json:"os"`
	Arch       string    `json:"arch"`
	LastSeen   time.Time `json:"last_seen"`
	HasPending bool      `json:"has_pending"`
}

type authRole string

const (
	roleOperator authRole = "operator"
	roleAgent    authRole = "agent"
)

type authContext struct {
	role    authRole
	agentID string
	key     string
}

// Config configures server security and HTTP limits.
type Config struct {
	OperatorTokenHashes []string
	AgentTokenHashes    map[string]string

	MaxBodyBytes        int64
	ReadHeaderTimeout   time.Duration
	ReadTimeout         time.Duration
	WriteTimeout        time.Duration
	IdleTimeout         time.Duration
	RateLimitPerWindow  int
	RateLimitWindowSize time.Duration
}

// ListenConfig configures how the HTTP server listens.
type ListenConfig struct {
	Addr        string
	DevHTTP     bool
	TLSCertFile string
	TLSKeyFile  string
}

// Server is the C2 HTTP server.
type Server struct {
	mu      sync.Mutex
	agents  map[string]*Agent
	results []protocol.Result
	mux     *http.ServeMux

	operatorTokenHashes [][32]byte
	agentTokenByHash    map[[32]byte]string
	agentHashByID       map[string][32]byte

	maxBodyBytes      int64
	limiter           *rateLimiter
	readHeaderTimeout time.Duration
	readTimeout       time.Duration
	writeTimeout      time.Duration
	idleTimeout       time.Duration
}

// DefaultConfig returns a secure baseline configuration.
func DefaultConfig() Config {
	return Config{
		MaxBodyBytes:        defaultMaxBodyBytes,
		ReadHeaderTimeout:   defaultReadHeaderTimeout,
		ReadTimeout:         defaultReadTimeout,
		WriteTimeout:        defaultWriteTimeout,
		IdleTimeout:         defaultIdleTimeout,
		RateLimitPerWindow:  defaultRateLimitPerWindow,
		RateLimitWindowSize: defaultRateLimitWindow,
	}
}

// New creates and initialises a new Server.
func New(cfg Config) (*Server, error) {
	cfg = normalizeConfig(cfg)
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	s := &Server{
		agents:            make(map[string]*Agent),
		mux:               http.NewServeMux(),
		agentTokenByHash:  make(map[[32]byte]string),
		agentHashByID:     make(map[string][32]byte),
		maxBodyBytes:      cfg.MaxBodyBytes,
		limiter:           newRateLimiter(cfg.RateLimitPerWindow, cfg.RateLimitWindowSize),
		readHeaderTimeout: cfg.ReadHeaderTimeout,
		readTimeout:       cfg.ReadTimeout,
		writeTimeout:      cfg.WriteTimeout,
		idleTimeout:       cfg.IdleTimeout,
	}

	for _, tokenHash := range cfg.OperatorTokenHashes {
		parsedHash, err := parseSHA256Hex(tokenHash)
		if err != nil {
			return nil, fmt.Errorf("invalid operator token hash: %w", err)
		}
		s.operatorTokenHashes = append(s.operatorTokenHashes, parsedHash)
	}

	for agentID, tokenHash := range cfg.AgentTokenHashes {
		if err := validateID(agentID); err != nil {
			return nil, fmt.Errorf("invalid agent id in credentials: %w", err)
		}
		parsedHash, err := parseSHA256Hex(tokenHash)
		if err != nil {
			return nil, fmt.Errorf("invalid agent token hash for %q: %w", agentID, err)
		}
		if _, exists := s.agentTokenByHash[parsedHash]; exists {
			return nil, errors.New("duplicate agent credential hash")
		}
		s.agentTokenByHash[parsedHash] = agentID
		s.agentHashByID[agentID] = parsedHash
	}

	s.mux.HandleFunc("/checkin", s.handleCheckIn)
	s.mux.HandleFunc("/result", s.handleResult)
	s.mux.HandleFunc("/agents", s.handleListAgents)
	s.mux.HandleFunc("/command", s.handleSendCommand)
	s.mux.HandleFunc("/results", s.handleListResults)
	return s, nil
}

// RevokeAgentCredential revokes the configured credential for a given agent ID.
func (s *Server) RevokeAgentCredential(agentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hash, ok := s.agentHashByID[agentID]
	if !ok {
		return
	}
	delete(s.agentHashByID, agentID)
	delete(s.agentTokenByHash, hash)
}

func normalizeConfig(cfg Config) Config {
	defaults := DefaultConfig()
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = defaults.MaxBodyBytes
	}
	if cfg.ReadHeaderTimeout <= 0 {
		cfg.ReadHeaderTimeout = defaults.ReadHeaderTimeout
	}
	if cfg.ReadTimeout <= 0 {
		cfg.ReadTimeout = defaults.ReadTimeout
	}
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = defaults.WriteTimeout
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = defaults.IdleTimeout
	}
	if cfg.RateLimitPerWindow <= 0 {
		cfg.RateLimitPerWindow = defaults.RateLimitPerWindow
	}
	if cfg.RateLimitWindowSize <= 0 {
		cfg.RateLimitWindowSize = defaults.RateLimitWindowSize
	}
	if cfg.AgentTokenHashes == nil {
		cfg.AgentTokenHashes = map[string]string{}
	}
	return cfg
}

func validateConfig(cfg Config) error {
	if len(cfg.OperatorTokenHashes) == 0 {
		return errors.New("at least one operator token hash is required")
	}
	if len(cfg.AgentTokenHashes) == 0 {
		return errors.New("at least one agent token hash is required")
	}
	return nil
}

// Handler returns the HTTP handler for the server, useful for testing.
func (s *Server) Handler() http.Handler {
	return s.mux
}

// ListenAndServe starts the HTTP server and shuts down gracefully when ctx is canceled.
func (s *Server) ListenAndServe(ctx context.Context, cfg ListenConfig) error {
	if err := validateListenConfig(cfg); err != nil {
		return err
	}

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           s.mux,
		ReadHeaderTimeout: s.readHeaderTimeout,
		ReadTimeout:       s.readTimeout,
		WriteTimeout:      s.writeTimeout,
		IdleTimeout:       s.idleTimeout,
	}

	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("[server] graceful shutdown error: %v", err)
		}
	}()

	if cfg.DevHTTP {
		log.Printf("[server] listening with dev HTTP on %s", cfg.Addr)
		err := httpServer.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			<-shutdownDone
			return nil
		}
		return err
	}

	log.Printf("[server] listening with TLS on %s", cfg.Addr)
	err := httpServer.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile)
	if errors.Is(err, http.ErrServerClosed) {
		<-shutdownDone
		return nil
	}
	return err
}

func validateListenConfig(cfg ListenConfig) error {
	if strings.TrimSpace(cfg.Addr) == "" {
		return errors.New("listen address is required")
	}
	if cfg.DevHTTP {
		host, err := hostFromAddr(cfg.Addr)
		if err != nil {
			return err
		}
		if !isLoopbackHost(host) {
			return errors.New("dev HTTP is only allowed on loopback addresses")
		}
		return nil
	}

	if strings.TrimSpace(cfg.TLSCertFile) == "" || strings.TrimSpace(cfg.TLSKeyFile) == "" {
		return errors.New("TLS certificate and key are required unless -dev-http is enabled")
	}
	return nil
}

func hostFromAddr(addr string) (string, error) {
	host, _, err := net.SplitHostPort(addr)
	if err == nil {
		return host, nil
	}
	if strings.Contains(err.Error(), "missing port in address") {
		return "", fmt.Errorf("listen address must include port: %w", err)
	}
	return "", err
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

func (s *Server) authenticate(r *http.Request, expectedRole authRole) (authContext, error) {
	token, err := bearerToken(r.Header.Get("Authorization"))
	if err != nil {
		return authContext{}, err
	}

	tokenHash := sha256.Sum256([]byte(token))

	s.mu.Lock()
	defer s.mu.Unlock()

	if expectedRole == roleOperator {
		for _, known := range s.operatorTokenHashes {
			if subtle.ConstantTimeCompare(tokenHash[:], known[:]) == 1 {
				return authContext{role: roleOperator, key: "operator"}, nil
			}
		}
		return authContext{}, errors.New("operator credential is invalid")
	}

	for knownHash, agentID := range s.agentTokenByHash {
		if subtle.ConstantTimeCompare(tokenHash[:], knownHash[:]) == 1 {
			return authContext{role: roleAgent, agentID: agentID, key: "agent:" + agentID}, nil
		}
	}
	return authContext{}, errors.New("agent credential is invalid or revoked")
}

func bearerToken(header string) (string, error) {
	if strings.TrimSpace(header) == "" {
		return "", errors.New("missing authorization header")
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", errors.New("authorization header must be bearer token")
	}
	token := strings.TrimSpace(parts[1])
	if token == "" {
		return "", errors.New("empty bearer token")
	}
	return token, nil
}

func (s *Server) applyRequestPolicy(w http.ResponseWriter, r *http.Request, expectedRole authRole, action string) (authContext, bool) {
	ctx, err := s.authenticate(r, expectedRole)
	if err != nil {
		s.audit("unknown", "", "", action, "unauthorized")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return authContext{}, false
	}
	if !s.limiter.Allow(ctx.key) {
		s.audit(string(ctx.role), ctx.agentID, "", action, "rate_limited")
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return authContext{}, false
	}
	return ctx, true
}

// handleCheckIn registers or updates an agent and returns any pending command.
func (s *Server) handleCheckIn(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	authCtx, ok := s.applyRequestPolicy(w, r, roleAgent, "agent.checkin")
	if !ok {
		return
	}

	var ci protocol.CheckIn
	if err := decodeStrictJSON(w, r, &ci, s.maxBodyBytes); err != nil {
		s.audit("agent", authCtx.agentID, "", "agent.checkin", "bad_request")
		return
	}

	if err := validateID(ci.ID); err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		s.audit("agent", authCtx.agentID, "", "agent.checkin", "invalid_id")
		return
	}
	if ci.ID != authCtx.agentID {
		http.Error(w, "agent id mismatch", http.StatusForbidden)
		s.audit("agent", authCtx.agentID, "", "agent.checkin", "forbidden")
		return
	}
	if len(ci.Hostname) == 0 || len(ci.Hostname) > maxHostname {
		http.Error(w, "invalid hostname", http.StatusBadRequest)
		s.audit("agent", authCtx.agentID, "", "agent.checkin", "invalid_hostname")
		return
	}
	if len(ci.OS) == 0 || len(ci.OS) > maxOSArchLength || len(ci.Arch) == 0 || len(ci.Arch) > maxOSArchLength {
		http.Error(w, "invalid os/arch", http.StatusBadRequest)
		s.audit("agent", authCtx.agentID, "", "agent.checkin", "invalid_platform")
		return
	}

	s.mu.Lock()
	a, exists := s.agents[ci.ID]
	if !exists {
		a = &Agent{ID: ci.ID}
		s.agents[ci.ID] = a
	}
	a.Hostname = ci.Hostname
	a.OS = ci.OS
	a.Arch = ci.Arch
	a.LastSeen = time.Now().UTC()

	resp := protocol.CheckInResponse{
		CommandID: a.commandID,
		Command:   a.command,
	}
	a.commandID = ""
	a.command = ""
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("[server] encode error: %v", err)
	}
	s.audit("agent", authCtx.agentID, resp.CommandID, "agent.checkin", "ok")
}

// handleResult stores the result received from an agent.
func (s *Server) handleResult(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	authCtx, ok := s.applyRequestPolicy(w, r, roleAgent, "agent.result")
	if !ok {
		return
	}

	var res protocol.Result
	if err := decodeStrictJSON(w, r, &res, s.maxBodyBytes); err != nil {
		s.audit("agent", authCtx.agentID, "", "agent.result", "bad_request")
		return
	}

	if err := validateID(res.AgentID); err != nil {
		http.Error(w, "invalid agent_id", http.StatusBadRequest)
		s.audit("agent", authCtx.agentID, "", "agent.result", "invalid_agent_id")
		return
	}
	if err := validateID(res.CommandID); err != nil {
		http.Error(w, "invalid command_id", http.StatusBadRequest)
		s.audit("agent", authCtx.agentID, res.CommandID, "agent.result", "invalid_command_id")
		return
	}
	if res.AgentID != authCtx.agentID {
		http.Error(w, "agent id mismatch", http.StatusForbidden)
		s.audit("agent", authCtx.agentID, res.CommandID, "agent.result", "forbidden")
		return
	}
	if len(res.Output) > maxOutputLength || len(res.Error) > maxErrorLength {
		http.Error(w, "result too large", http.StatusBadRequest)
		s.audit("agent", authCtx.agentID, res.CommandID, "agent.result", "oversized_result")
		return
	}

	s.mu.Lock()
	s.results = append(s.results, res)
	s.mu.Unlock()

	w.WriteHeader(http.StatusNoContent)
	s.audit("agent", authCtx.agentID, res.CommandID, "agent.result", "ok")
}

// handleListAgents returns a JSON list of all known agents.
func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	authCtx, ok := s.applyRequestPolicy(w, r, roleOperator, "operator.list_agents")
	if !ok {
		return
	}

	s.mu.Lock()
	agents := make([]AgentInfo, 0, len(s.agents))
	for _, a := range s.agents {
		agents = append(agents, AgentInfo{
			ID:         a.ID,
			Hostname:   a.Hostname,
			OS:         a.OS,
			Arch:       a.Arch,
			LastSeen:   a.LastSeen,
			HasPending: a.commandID != "" || a.command != "",
		})
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(agents); err != nil {
		log.Printf("[server] encode error: %v", err)
	}
	s.audit(string(authCtx.role), "", "", "operator.list_agents", "ok")
}

// handleSendCommand queues a command for a specific agent.
func (s *Server) handleSendCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	authCtx, ok := s.applyRequestPolicy(w, r, roleOperator, "operator.queue_command")
	if !ok {
		return
	}

	var req protocol.CommandRequest
	if err := decodeStrictJSON(w, r, &req, s.maxBodyBytes); err != nil {
		s.audit(string(authCtx.role), "", "", "operator.queue_command", "bad_request")
		return
	}
	if err := validateID(req.AgentID); err != nil {
		http.Error(w, "invalid agent_id", http.StatusBadRequest)
		s.audit(string(authCtx.role), req.AgentID, req.CommandID, "operator.queue_command", "invalid_agent_id")
		return
	}
	if err := validateID(req.CommandID); err != nil {
		http.Error(w, "invalid command_id", http.StatusBadRequest)
		s.audit(string(authCtx.role), req.AgentID, req.CommandID, "operator.queue_command", "invalid_command_id")
		return
	}
	if strings.TrimSpace(req.Command) == "" || len(req.Command) > maxCommandLength {
		http.Error(w, "invalid command", http.StatusBadRequest)
		s.audit(string(authCtx.role), req.AgentID, req.CommandID, "operator.queue_command", "invalid_command")
		return
	}

	s.mu.Lock()
	a, ok := s.agents[req.AgentID]
	if !ok {
		s.mu.Unlock()
		http.Error(w, fmt.Sprintf("agent %s not found", req.AgentID), http.StatusNotFound)
		s.audit(string(authCtx.role), req.AgentID, req.CommandID, "operator.queue_command", "agent_not_found")
		return
	}
	a.commandID = req.CommandID
	a.command = req.Command
	s.mu.Unlock()

	w.WriteHeader(http.StatusNoContent)
	s.audit(string(authCtx.role), req.AgentID, req.CommandID, "operator.queue_command", "ok")
}

// handleListResults returns all stored results as JSON.
func (s *Server) handleListResults(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	authCtx, ok := s.applyRequestPolicy(w, r, roleOperator, "operator.list_results")
	if !ok {
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
	s.audit(string(authCtx.role), "", "", "operator.list_results", "ok")
}

func decodeStrictJSON(w http.ResponseWriter, r *http.Request, dst any, maxBodyBytes int64) error {
	if err := requireJSONContentType(r); err != nil {
		http.Error(w, err.Error(), http.StatusUnsupportedMediaType)
		return err
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	defer r.Body.Close()

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.As(err, new(*http.MaxBytesError)) || strings.Contains(err.Error(), "request body too large") {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return err
		}
		http.Error(w, "bad request", http.StatusBadRequest)
		return err
	}

	if err := dec.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "bad request", http.StatusBadRequest)
		return errors.New("multiple JSON values")
	}

	return nil
}

func requireJSONContentType(r *http.Request) error {
	contentType := r.Header.Get("Content-Type")
	if strings.TrimSpace(contentType) == "" {
		return errors.New("content-type must be application/json")
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return errors.New("content-type must be application/json")
	}
	if mediaType != "application/json" {
		return errors.New("content-type must be application/json")
	}
	return nil
}

func validateID(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return errors.New("id cannot be empty")
	}
	if len(trimmed) > maxIDLength {
		return errors.New("id too long")
	}
	if !idPattern.MatchString(trimmed) {
		return errors.New("id contains invalid characters")
	}
	return nil
}

func parseSHA256Hex(value string) ([32]byte, error) {
	var zero [32]byte
	decoded, err := hex.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return zero, err
	}
	if len(decoded) != sha256.Size {
		return zero, fmt.Errorf("expected %d-byte SHA-256 hash, got %d", sha256.Size, len(decoded))
	}
	var parsed [32]byte
	copy(parsed[:], decoded)
	return parsed, nil
}

type rateLimiter struct {
	mu         sync.Mutex
	entries    map[string]rateWindow
	limit      int
	windowSize time.Duration
}

type rateWindow struct {
	start time.Time
	count int
}

func newRateLimiter(limit int, windowSize time.Duration) *rateLimiter {
	return &rateLimiter{
		entries:    make(map[string]rateWindow),
		limit:      limit,
		windowSize: windowSize,
	}
}

func (l *rateLimiter) Allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	window := l.entries[key]
	if window.start.IsZero() || now.Sub(window.start) >= l.windowSize {
		l.entries[key] = rateWindow{start: now, count: 1}
		return true
	}
	if window.count >= l.limit {
		return false
	}
	window.count++
	l.entries[key] = window
	return true
}

type auditEvent struct {
	Time      time.Time `json:"time"`
	Actor     string    `json:"actor"`
	AgentID   string    `json:"agent_id,omitempty"`
	CommandID string    `json:"command_id,omitempty"`
	Action    string    `json:"action"`
	Status    string    `json:"status"`
}

func (s *Server) audit(actor, agentID, commandID, action, status string) {
	event := auditEvent{
		Time:      time.Now().UTC(),
		Actor:     actor,
		AgentID:   agentID,
		CommandID: commandID,
		Action:    action,
		Status:    status,
	}
	payload, err := json.Marshal(event)
	if err != nil {
		log.Printf("[audit] marshal_error action=%s status=%s", action, status)
		return
	}
	log.Printf("[audit] %s", payload)
}
