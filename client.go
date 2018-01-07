package tftp

import (
	"errors"
	"io"
	"log"
	"net"
	"os"
	"strconv"
)

var TftpProtocolViolation = errors.New("TFTP protocol violation")

type TftpRemoteError struct {
	Code    int
	Message string
}

func (e *TftpRemoteError) Error() string {
	return e.Message
}

func GetFile(address string, filename string, mode string, options int, writer io.Writer) error {
	// Bind a random but specific local socket for this request.
	mainRemoteAddr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return err
	}
	conn, err := net.ListenPacket("udp", ":0")
	if err != nil {
		log.Printf("Could not create socket: %v", err)
		return err
	}
	defer conn.Close()

	state := connectionState{
		buffer:         make([]byte, 65535),
		log:            log.New(os.Stderr, "TFTP: ", log.LstdFlags),
		conn:           conn,
		mainRemoteAddr: mainRemoteAddr,
		blockSize:      DEFAULT_BLOCKSIZE,
		timeout:        5,
		tracePackets:   true,
	}

	enableTransferSizeOption := true
	requestedBlockSize := 16384
	enableBlockSizeOption := false

	// Build the read request.
	rrq := ReadRequest{
		Filename: filename,
		Mode:     mode,
		Options:  make(map[string]string),
	}
	if enableTransferSizeOption {
		rrq.Options["tsize"] = "0"
	}
	if enableBlockSizeOption {
		rrq.Options["blksize"] = strconv.Itoa(requestedBlockSize)
	}

	var currentBlockNum uint16 = 1
	var currentDataPacket *Data = nil
	var actualTransferSize = 0

rrqLoop:
	for {
		state.send(&rrq)

		// Wait for the reply packet. The `receive` method will automatically latch onto to the IP address
		// of the first reply packet to return.
		rawPacket, err := state.receive()
		if err != nil {
			if timeoutError, ok := err.(net.Error); ok && timeoutError.Timeout() {
				continue rrqLoop
			}
			return err
		}

		switch packet := rawPacket.(type) {
		case Data:
			if len(rrq.Options) > 0 {
				log.Printf("TFTP server %v does not support custom options.", mainRemoteAddr)
			}
			if packet.Block != currentBlockNum {
				log.Printf("TFTP server sent unexpected block number.")
				state.send(&Error{Code: ERR_ILLEGAL_OPERATION, Message: "Illegal operation."})
				return TftpProtocolViolation
			}
			currentDataPacket = &packet
			writer.Write(packet.Data)
			actualTransferSize += len(packet.Data)
			break rrqLoop

		case OptionsAck:
			// Parse the options to understand what the server did and did not accept.
			if value, ok := packet.Options["blksize"]; ok {
				accetpedBlockSize, err := strconv.Atoi(value)
				if err == nil {
					// TODO: validate the accepted blocksize
					log.Printf("using block size = %v", accetpedBlockSize)
					state.blockSize = uint16(accetpedBlockSize)
				}
			}
			if value, ok := packet.Options["tsize"]; ok {
				expectedTransferSize, err := strconv.Atoi(value)
				if err == nil {
					log.Printf("expected transfer size = %v", expectedTransferSize)
					//state.expectedTransferSize = expectedTransferSize
				}
			}

			// Reset the block number so that the data receive loop will send the correct acknowlegement.
			currentBlockNum = 0
			break rrqLoop

		case Error:
			// Log the error and terminate the connection.
			log.Printf("Server returned error: %v", packet.Message)
			return &TftpRemoteError{
				Code:    int(packet.Code),
				Message: packet.Message}

		default:
			log.Printf("Server returned unexpected packet type: %v", packet)
			return TftpProtocolViolation
		}
	}

dataLoop:
	for {
		// Send the acknowledgement for the data packet or OACK.
		ack := Ack{Block: currentBlockNum}
		state.send(&ack)

		// Transfer ends when we receive a packet with less data than the block size.
		if currentDataPacket != nil && uint16(len(currentDataPacket.Data)) < state.blockSize {
			break dataLoop
		}

		rawPacket, err := state.receive()
		if err != nil {
			if timeoutError, ok := err.(net.Error); ok && timeoutError.Timeout() {
				continue dataLoop
			}
			return err
		}

		switch packet := rawPacket.(type) {
		case Data:
			if packet.Block == currentBlockNum+1 {
				currentBlockNum = packet.Block
				currentDataPacket = &packet
				writer.Write(packet.Data)
				actualTransferSize += len(packet.Data)
				continue dataLoop
			}

		case Error:
			// Log the error and terminate the connection.
			log.Printf("Server returned error: %v", packet.Message)
			return &TftpRemoteError{
				Code:    int(packet.Code),
				Message: packet.Message}

		default:
			log.Printf("Server returned unexpected packet type: %v", packet)
			return TftpProtocolViolation
		}
	}

	return nil
}
