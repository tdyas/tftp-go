package tftp

import (
	"context"
	"errors"
	"io"
	"io/ioutil"
	"log"
	"net"
	"strconv"
)

var TftpProtocolViolation = errors.New("TFTP protocol violation")
var TransferSizeError = errors.New("the actual number of bytes != the expected number of bytes")

type TftpRemoteError struct {
	Code    int
	Message string
}

func (e *TftpRemoteError) Error() string {
	return e.Message
}

type ClientConfig struct {
	DisableOptions            bool
	DisableTransferSizeOption bool
	DisableBlockSizeOption    bool
	MaxBlockSize              uint16
	MaxRetries                int
	TracePackets              bool
	Logger                    *log.Logger
}

func validateClientConfig(userConfig *ClientConfig) (*ClientConfig, error) {
	var config = *userConfig
	if config.Logger == nil {
		config.Logger = log.New(ioutil.Discard, "", 0)
	}
	if config.MaxRetries == 0 {
		config.MaxRetries = 5
	}
	if config.MaxBlockSize == 0 {
		config.MaxBlockSize = 16384
	} else if config.MaxBlockSize < MIN_BLOCK_SIZE || config.MaxBlockSize > MAX_BLOCK_SIZE {
		return nil, errors.New("invalid MaxBlockSize config")
	}

	return &config, nil
}

func GetFile(
	ctx context.Context,
	address string,
	filename string,
	mode string,
	config *ClientConfig,
	writer io.Writer) error {

	// Validate the configuration.
	config, err := validateClientConfig(config)
	if err != nil {
		return err
	}

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
	pchan, err := NewPacketChan(conn, 1, 1)
	if err != nil {
		log.Printf("Could not create socket: %v", err)
		return err
	}
	defer pchan.Close()

	state := connectionState{
		ctx:            ctx,
		logger:         config.Logger,
		conn:           pchan,
		mainRemoteAddr: mainRemoteAddr,
		blockSize:      DEFAULT_BLOCKSIZE,
		timeout:        5,
		maxRetries:     config.MaxRetries,
		tracePackets:   config.TracePackets,
	}

	enableTransferSizeOption := !config.DisableOptions && !config.DisableTransferSizeOption

	enableBlockSizeOption := !config.DisableOptions && !config.DisableBlockSizeOption
	var requestedBlockSize uint16 = DEFAULT_BLOCKSIZE
	if enableBlockSizeOption {
		requestedBlockSize = config.MaxBlockSize
	}

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
		rrq.Options["blksize"] = strconv.Itoa(int(requestedBlockSize))
	}

	var currentBlockNum uint16 = 1
	var currentDataPacket *Data = nil
	var actualTransferSize uint64 = 0
	var expectedTransferSize uint64 = 0

	rawPacket, err := state.sendAndReceiveNext(&rrq, func(rawPacket interface{}) (bool, error) {
		switch packet := rawPacket.(type) {
		case Data:
			return true, nil
		case OptionsAck:
			return true, nil
		default:
			state.log("Server returned unexpected packet type: %v", packet)
			return false, TftpProtocolViolation
		}
	})
	if err != nil {
		return err
	}

	switch packet := rawPacket.(type) {
	case Data:
		if len(rrq.Options) > 0 {
			state.log("TFTP server %v does not support custom options.", mainRemoteAddr)
		}
		if packet.Block != currentBlockNum {
			log.Printf("TFTP server sent unexpected block number.")
			state.send(&Error{Code: ERR_ILLEGAL_OPERATION, Message: "Illegal operation."})
			return TftpProtocolViolation
		}
		currentDataPacket = &packet
		writer.Write(packet.Data)
		actualTransferSize += uint64(len(packet.Data))

	case OptionsAck:
		// Parse the options to understand what the server did and did not accept.
		if value, ok := packet.Options["blksize"]; ok {
			accetpedBlockSize, err := strconv.Atoi(value)
			if err == nil {
				// TODO: validate the accepted blocksize
				state.log("using block size = %v", accetpedBlockSize)
				state.blockSize = uint16(accetpedBlockSize)
			}
		}
		if value, ok := packet.Options["tsize"]; ok {
			s, err := strconv.Atoi(value)
			if err == nil {
				expectedTransferSize = uint64(s)
				state.log("expected transfer size: %d", expectedTransferSize)
			}
		}

		// Reset the block number so that the data receive loop will send the correct acknowledgement.
		currentBlockNum = 0
	}

dataLoop:
	for {
		// Send the acknowledgement for the data packet or OACK.
		ack := Ack{Block: currentBlockNum}

		// Transfer ends when we receive a packet with less data than the block size.
		if currentDataPacket != nil && uint16(len(currentDataPacket.Data)) < state.blockSize {
			state.send(&ack)
			break dataLoop
		}

		rawPacket, err := state.sendAndReceiveNext(&ack, func(rawPacket interface{}) (bool, error) {
			switch packet := rawPacket.(type) {
			case Data:
				if packet.Block == currentBlockNum+1 {
					return true, nil
				} else if packet.Block <= currentBlockNum {
					// Disregard prior DATA packets.
					return false, nil
				} else {
					// Data packets past the current block violate the protocol.
					return false, TftpProtocolViolation
				}

			default:
				return false, TftpProtocolViolation
			}

		})
		if err != nil {
			return err
		}

		packet := rawPacket.(Data)
		currentBlockNum = packet.Block
		currentDataPacket = &packet
		writer.Write(packet.Data)
		actualTransferSize += uint64(len(packet.Data))
	}

	if expectedTransferSize != 0 && actualTransferSize != expectedTransferSize {
		state.log("transfer size mismatch: expected=%d, actual=%d", expectedTransferSize, actualTransferSize)
		return TransferSizeError
	}

	return nil
}

func PutFile(
	ctx context.Context,
	address string,
	filename string,
	mode string,
	config *ClientConfig,
	reader io.Reader) error {

	// Validate the configuration.
	config, err := validateClientConfig(config)
	if err != nil {
		return err
	}

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
	pchan, err := NewPacketChan(conn, 1, 1)
	if err != nil {
		log.Printf("Could not create socket: %v", err)
		return err
	}
	defer pchan.Close()

	state := connectionState{
		ctx:            ctx,
		logger:         config.Logger,
		conn:           pchan,
		mainRemoteAddr: mainRemoteAddr,
		blockSize:      DEFAULT_BLOCKSIZE,
		timeout:        5,
		maxRetries:     config.MaxRetries,
		tracePackets:   config.TracePackets,
	}

	enableTransferSizeOption := !config.DisableOptions && !config.DisableTransferSizeOption
	totalSize := SizeOfReader(reader)
	if totalSize == -1 {
		enableTransferSizeOption = false
	}

	var requestedBlockSize uint16 = DEFAULT_BLOCKSIZE
	enableBlockSizeOption := !config.DisableOptions && !config.DisableBlockSizeOption
	if enableBlockSizeOption {
		requestedBlockSize = config.MaxBlockSize
	}

	// Build the write request.
	writeRequest := WriteRequest{
		Filename: filename,
		Mode:     mode,
		Options:  make(map[string]string),
	}
	if !config.DisableOptions {
		if enableTransferSizeOption {
			writeRequest.Options["tsize"] = strconv.FormatInt(totalSize, 10)
		}
		if enableBlockSizeOption {
			writeRequest.Options["blksize"] = strconv.Itoa(int(requestedBlockSize))
		}
	}

	rawPacket, err := state.sendAndReceiveNext(&writeRequest, func(rawPacket interface{}) (bool, error) {
		switch packet := rawPacket.(type) {
		case Ack:
			return true, nil
		case OptionsAck:
			return true, nil
		default:
			state.log("Server returned unexpected packet type: %v", packet)
			return false, TftpProtocolViolation
		}
	})
	if err != nil {
		return err
	}

	switch packet := rawPacket.(type) {
	case Ack:
		if len(writeRequest.Options) > 0 {
			state.log("TFTP server %v does not support custom options.", mainRemoteAddr)
		}
		if packet.Block != 0 {
			log.Printf("TFTP server sent unexpected block number.")
			state.send(&Error{Code: ERR_ILLEGAL_OPERATION, Message: "Illegal operation."})
			return TftpProtocolViolation
		}

	case OptionsAck:
		// Parse the options to understand what the server did and did not accept.
		if value, ok := packet.Options["blksize"]; ok {
			acceptedBlockSize, err := strconv.Atoi(value)
			if err == nil {
				if acceptedBlockSize < MIN_BLOCK_SIZE || acceptedBlockSize > MAX_BLOCK_SIZE {
					state.log("block size %v is outside allowed range", acceptedBlockSize)
					state.send(&Error{Code: ERR_INVALID_OPTIONS, Message: "Invalid blksize option"})
					return TftpProtocolViolation
				}
				state.log("using block size = %v", acceptedBlockSize)
				state.blockSize = uint16(acceptedBlockSize)
			} else {
				state.log("Unable to convert blocksize")
				state.send(&Error{Code: ERR_INVALID_OPTIONS, Message: "Invalid blksize option"})
				return TftpProtocolViolation
			}
		}
	}

	var currentBlockNum uint16 = 1
	fileBuffer := make([]byte, state.blockSize)

	for {
		n, err := readExact(reader, fileBuffer)
		if err != nil {
			log.Printf("readExact failed with: %v", err)
			state.send(&Error{ERR_NOT_DEFINED, "Read error"})
			return err
		}

		dataPacket := Data{Block: currentBlockNum, Data: fileBuffer[0:n]}
		isLastPacket := uint16(n) < state.blockSize

		_, err = state.sendAndReceiveNext(&dataPacket, func(rawPacket interface{}) (bool, error) {
			switch packet := rawPacket.(type) {
			case Ack:
				if packet.Block == currentBlockNum {
					// Client acknowledged the last packet.
					return true, nil
				} else if packet.Block < currentBlockNum {
					// Ignore acknowledgements of earlier packets.
					return false, nil
				} else {
					// Future blocks cannot be acknowledged since we did not send them. Bail out.
					errorPacket := Error{Code: ERR_ILLEGAL_OPERATION, Message: "Illegal operation"}
					state.send(&errorPacket)
					return false, &errorPacket
				}

			default:
				state.log("Server returned unexpected packet type: %v", packet)
				return false, TftpProtocolViolation

			}
		})
		if err != nil {
			return err
		}

		currentBlockNum += 1
		if isLastPacket {
			break
		}
	}

	return nil
}
