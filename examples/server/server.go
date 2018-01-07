package main

import (
	"os"
	"log"
	"fmt"
	"github.com/tdyas/tftp-go"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Println("ERROR: Must specify directory.")
		os.Exit(1)
	}

	dir := os.Args[1]
	log.Printf("root: %v", dir)
	var config = tftp.ServerConfig{
		ReadRoot: dir,
		WriteRoot: dir,
		TracePackets: true,
		}
	server, err := tftp.NewServer(":9090", &config)
	if err != nil {
		log.Printf("Error: %v", err)
		os.Exit(1)
	}

	server.Join()
	os.Exit(0)
}
