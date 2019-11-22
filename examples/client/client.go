package main

import (
	"context"
	"fmt"
	tftp "github.com/tdyas/tftp-go"
	"os"
)

func get(address string, srcFilename string, destFilename string) error {
	f, err := os.OpenFile(destFilename, os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		fmt.Printf("ERROR: Unable to create file %v: %v\n", destFilename, err)
		return err
	}
	defer f.Close()

	err = tftp.GetFile(context.Background(), address, srcFilename, "octet", nil, f)
	if err != nil {
		fmt.Printf("ERROR: TFTP read request failed: %v\n", err)
		return err
	}

	return nil
}

func put(address string, srcFilename string, destFilename string) error {
	f, err := os.Open(srcFilename)
	if err != nil {
		fmt.Printf("ERROR: Unable to open file `%s`: %v\n", srcFilename, err)
		return err
	}
	defer f.Close()

	err = tftp.PutFile(context.Background(), address, destFilename, "octet", nil, f)
	if err != nil {
		fmt.Printf("ERROR: TFTP write request failed: %v", err)
		return err
	}

	return nil
}

func main() {
	if len(os.Args) != 4 {
		fmt.Printf("usage: read|write REMOTE SOURCE DEST.\n")
		os.Exit(1)
	}

	address := os.Args[1]
	cmd := os.Args[2]
	srcFilename := os.Args[3]
	destFilename := os.Args[4]

	switch cmd {
	case "get":
		err := get(address, srcFilename, destFilename)
		if err != nil {
			os.Exit(1)
		}

	case "put":
		err := put(address, srcFilename, destFilename)
		if err != nil {
			os.Exit(1)
		}

	default:
		fmt.Printf("ERROR: unknown command `%s`\n", cmd)
		os.Exit(1)
	}
}
