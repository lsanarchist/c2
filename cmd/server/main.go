package main

import (
	"flag"
	"log"

	"github.com/lsanarchist/c2/internal/server"
)

func main() {
	addr := flag.String("addr", ":8080", "address to listen on")
	flag.Parse()

	s := server.New()
	log.Fatal(s.ListenAndServe(*addr))
}
