package main

import (
	"github.com/tdyas/tftp"
	"os"
	"log"
)

func main() {
	server, err := tftp.NewServer(":9090")
	if err != nil {
		log.Printf("Error: %v", err)
		os.Exit(1)
	}

	server.Join()
	os.Exit(0)
}
