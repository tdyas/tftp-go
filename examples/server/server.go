package main

import (
	"github.com/tdyas/tftp"
	"os"
	"log"
	"fmt"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Println("ERROR: Must specify directory.")
		os.Exit(1)
	}

	dir := os.Args[1]
	log.Printf("root: %v", dir)
	var config = tftp.ServerConfig{TracePackets: true}
	server, err := tftp.NewServer(":9090", dir, &config)
	if err != nil {
		log.Printf("Error: %v", err)
		os.Exit(1)
	}

	server.Join()
	os.Exit(0)
}
