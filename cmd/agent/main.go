package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lsanarchist/c2/internal/agent"
)

func main() {
	serverURL := flag.String("server", "https://127.0.0.1:8443", "C2 server URL")
	interval := flag.Duration("interval", 10*time.Second, "check-in interval")
	id := flag.String("id", "", "agent ID (defaults to hostname)")
	credential := flag.String("credential", "", "agent credential (or C2_AGENT_CREDENTIAL env)")
	devHTTP := flag.Bool("dev-http", false, "allow HTTP transport on loopback only")
	tlsServerName := flag.String("tls-server-name", "", "expected TLS server name override")
	caCert := flag.String("ca-cert", "", "custom CA certificate PEM path")
	flag.Parse()

	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}

	agentID := *id
	if agentID == "" {
		agentID = fmt.Sprintf("agent-%s", hostname)
	}

	cred := strings.TrimSpace(*credential)
	if cred == "" {
		cred = strings.TrimSpace(os.Getenv("C2_AGENT_CREDENTIAL"))
	}

	a, err := agent.New(agentID, hostname, *serverURL, *interval, agent.Options{
		Credential:    cred,
		AllowHTTP:     *devHTTP,
		TLSServerName: *tlsServerName,
		CACertFile:    *caCert,
	})
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := a.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
