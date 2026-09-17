package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/lsanarchist/c2/internal/server"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8443", "address to listen on")
	devHTTP := flag.Bool("dev-http", false, "allow HTTP without TLS on loopback only")
	tlsCert := flag.String("tls-cert", "", "path to TLS certificate file (required unless -dev-http)")
	tlsKey := flag.String("tls-key", "", "path to TLS key file (required unless -dev-http)")
	flag.Parse()

	s, err := server.New(server.Config{
		OperatorTokenHashes: splitCSVEnv("C2_OPERATOR_TOKEN_HASHES"),
		AgentTokenHashes:    parseAgentTokenHashesEnv("C2_AGENT_TOKEN_HASHES"),
	})
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := s.ListenAndServe(ctx, server.ListenConfig{
		Addr:        *addr,
		DevHTTP:     *devHTTP,
		TLSCertFile: *tlsCert,
		TLSKeyFile:  *tlsKey,
	}); err != nil {
		log.Fatal(err)
	}
}

func splitCSVEnv(name string) []string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func parseAgentTokenHashesEnv(name string) map[string]string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return nil
	}
	pairs := strings.Split(value, ",")
	out := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		items := strings.SplitN(strings.TrimSpace(pair), ":", 2)
		if len(items) != 2 {
			continue
		}
		agentID := strings.TrimSpace(items[0])
		hash := strings.TrimSpace(items[1])
		if agentID == "" || hash == "" {
			continue
		}
		out[agentID] = hash
	}
	return out
}
