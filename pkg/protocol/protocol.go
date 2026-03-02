// Package protocol defines the shared types used for communication
// between the C2 server and agents.
package protocol

// CheckIn is sent by an agent when it registers with the server.
type CheckIn struct {
	ID       string `json:"id"`
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
}

// CheckInResponse is returned by the server after a successful check-in.
// It carries the next pending command for the agent, if any.
type CheckInResponse struct {
	CommandID string `json:"command_id,omitempty"`
	Command   string `json:"command,omitempty"`
}

// Result is sent by an agent after executing a command.
type Result struct {
	AgentID   string `json:"agent_id"`
	CommandID string `json:"command_id"`
	Output    string `json:"output"`
	Error     string `json:"error,omitempty"`
}
