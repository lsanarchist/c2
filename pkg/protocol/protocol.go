// Package protocol defines the shared types used for communication
// between the C2 server and agents.
package protocol

import "time"

// CheckIn is sent by an agent when it registers with the server.
type CheckIn struct {
	ID       string `json:"id"`
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
}

// LeasedTask is returned to an agent when work is leased.
type LeasedTask struct {
	TaskID        string    `json:"task_id"`
	Command       string    `json:"command"`
	AttemptID     string    `json:"attempt_id"`
	LeaseExpires  time.Time `json:"lease_expires_at"`
	CancelRequest bool      `json:"cancel_requested,omitempty"`
}

// CheckInResponse is returned by the server after a successful check-in.
type CheckInResponse struct {
	Task *LeasedTask `json:"task,omitempty"`
}

// TaskCreateRequest is sent by the operator to enqueue a task for an agent.
type TaskCreateRequest struct {
	AgentID        string `json:"agent_id"`
	Command        string `json:"command"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// TaskAcknowledge confirms an agent has received the leased task.
type TaskAcknowledge struct {
	AgentID   string `json:"agent_id"`
	TaskID    string `json:"task_id"`
	AttemptID string `json:"attempt_id"`
}

// TaskResult is sent by an agent after command execution.
type TaskResult struct {
	AgentID   string `json:"agent_id"`
	TaskID    string `json:"task_id"`
	AttemptID string `json:"attempt_id"`
	Status    string `json:"status"`
	Output    string `json:"output"`
	Error     string `json:"error,omitempty"`
}

// Task represents operator-facing task state.
type Task struct {
	ID              string     `json:"id"`
	AgentID         string     `json:"agent_id"`
	Command         string     `json:"command"`
	State           string     `json:"state"`
	IdempotencyKey  string     `json:"idempotency_key,omitempty"`
	ActiveAttemptID string     `json:"active_attempt_id,omitempty"`
	LeaseExpiresAt  *time.Time `json:"lease_expires_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
}

// ResultRecord is the persisted result view returned to operator APIs.
type ResultRecord struct {
	TaskID     string    `json:"task_id"`
	AttemptID  string    `json:"attempt_id"`
	AgentID    string    `json:"agent_id"`
	Status     string    `json:"status"`
	Output     string    `json:"output"`
	Error      string    `json:"error,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	DedupeHash string    `json:"dedupe_hash"`
}
