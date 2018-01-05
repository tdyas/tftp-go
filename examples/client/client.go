package main

import (
	"fmt"
	"os"
	"github.com/tdyas/tftp"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Printf("ERROR: Must specify address and file to retrieve.\n")
		os.Exit(1)
	}

	address := os.Args[1]
	filename := os.Args[2]

	file, err := os.OpenFile(filename, os.O_WRONLY | os.O_CREATE, 0666)
	if err != nil {
		fmt.Printf("ERROR: Unable to create file %v: %v\n", filename, err)
		os.Exit(1)
	}

	err = tftp.GetFile(address, filename, "octet", 0, file)
	if err != nil {
		fmt.Printf("ERROR: TFTP RRQ failed: %v\n", err)
		os.Exit(1)
	}

	file.Close()
}