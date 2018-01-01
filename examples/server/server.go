package main

import (
	"github.com/tdyas/tftp"
	"os"
	"log"
)

func main() {
	dir, err := os.Getwd()
	log.Printf("root: %v", dir)
	server, err := tftp.NewServer(":9090", dir)
	if err != nil {
		log.Printf("Error: %v", err)
		os.Exit(1)
	}

	server.Join()
	os.Exit(0)
}
