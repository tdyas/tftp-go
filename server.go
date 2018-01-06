package tftp

//import "context"
import (
	"errors"
	"io"
	"log"
	"net"
	"os"
	"path"
	"strconv"
)

var RootMustBeAADirectoryError = errors.New("the TFTP root must be a directory")

func readExact(reader io.Reader, buffer []byte) (int, error) {
	n := 0
	for n < len(buffer) {
		amtRead, err := reader.Read(buffer[n:])
		if err != nil {
			return n, nil
		}

		if amtRead == 0 {
			break
		}

		n += amtRead
	}

	return n, nil
}

type ServerConfig struct {
	MaxBlockSize   uint16
	DisableOptions bool
}

type Server struct {
	config *ServerConfig
	conn   net.PacketConn
	root   string
	log    *log.Logger
	done   chan bool
}

func (server *Server) handleRRQ(state *connectionState, request *ReadRequest, replyConn net.PacketConn, remoteAddr net.Addr) {
	server.log.Printf("RRQ(%v): file=%v mode=%v", remoteAddr, request.Filename, request.Mode)

	// Ensure the file exists
	fullPath := path.Join(server.root, request.Filename)
	stat, err := os.Stat(fullPath)
	if err != nil || stat.IsDir() {
		state.send(&Error{Code: ERR_FILE_NOT_FOUND, Message: "File not found"})
		return
	}
	server.log.Printf("fullPath=%v, stat=%#v", fullPath, stat)

	f, err := os.Open(fullPath)
	if err != nil {
		state.send(&Error{Code: ERR_FILE_NOT_FOUND, Message: "File not found"})
		return
	}
	defer f.Close()

	var hasBlockSizeOption = false
	if requestedBlockSizeString, ok := request.Options["blksize"]; ok {
		requestedBlockSize, err := strconv.Atoi(requestedBlockSizeString)
		if err != nil || requestedBlockSize < MIN_BLOCK_SIZE {
			state.send(&Error{Code: ERR_INVALID_OPTIONS, Message: "Invalid blksize option"})
			return
		}

		if requestedBlockSize > int(server.config.MaxBlockSize) {
			requestedBlockSize = int(server.config.MaxBlockSize)
		}

		state.blockSize = uint16(requestedBlockSize)
		hasBlockSizeOption = true
	}

	_, hasTransferSizeOption := request.Options["tsize"]

	var hasTimeoutOption = false
	if requestedTimeoutString, ok := request.Options["timeout"]; ok {
		requestedTimeout, err := strconv.Atoi(requestedTimeoutString)
		if err != nil || requestedTimeout < 1 {
			state.send(&Error{Code: ERR_INVALID_OPTIONS, Message: "Invalid timeout option"})
			return
		}

		if requestedTimeout > 5*60 {
			requestedTimeout = 5 * 60
		}

		state.timeout = requestedTimeout
		hasTimeoutOption = true
	}

	if !server.config.DisableOptions && (hasBlockSizeOption || hasTransferSizeOption || hasTimeoutOption) {
		oack := OptionsAck{Options: make(map[string]string)}
		if hasBlockSizeOption {
			oack.Options["blksize"] = strconv.Itoa(int(state.blockSize))
		}
		if hasTransferSizeOption {
			oack.Options["tsize"] = strconv.FormatInt(stat.Size(), 10)
		}
		if hasTimeoutOption {
			oack.Options["timeout"] = strconv.Itoa(state.timeout)
		}

	oackSend:
		for {
			state.send(&oack)

			rawPacket, err := state.receive()
			if err != nil {
				if err.(net.Error).Timeout() {
					continue
				}
				return
			}

			switch packet := rawPacket.(type) {
			case Ack:
				if packet.Block == 0 {
					// Client acknowledged the options. Break out of the outer loop.
					break oackSend
				}

			case Error:
				server.log.Printf("Client returned error: %v", packet.Message)
				return

			default:
				server.log.Printf("Unexpected packet: %#v", packet)
				state.send(&Error{Code: ERR_ILLEGAL_OPERATION, Message: "Illegal operation"})
				return
			}
		}
	}

	server.log.Printf("state.blockSize=%v, timeout=", state.blockSize, state.timeout)

	// Start sending the file.
	fileBuffer := make([]byte, state.blockSize)
	var blockNum uint16 = 1
fileSend:
	for {
		n, err := readExact(f, fileBuffer)
		if err != nil {
			server.log.Printf("readExact failed with: %v", err)
			return
		}

		dataPacket := Data{Block: blockNum, Data: fileBuffer[0:n]}

	dataSend:
		for {
			_, err := state.send(&dataPacket)
			if err != nil {
				server.log.Printf("Failed to send DATA: err=%v", n, err)
				return
			}

			rawPacket, err := state.receive()
			if err != nil {
				if err.(net.Error).Timeout() {
					continue dataSend
				}
				return
			}

			switch packet := rawPacket.(type) {
			case Ack:
				if packet.Block == blockNum {
					// Client acknowledged the last packet. Break out of the outer loop.
					break dataSend
				}

			case Error:
				server.log.Printf("Client returned error: %v", packet.Message)
				return

			default:
				server.log.Printf("Unexpected packet: %#v", packet)
				state.send(&Error{Code: ERR_ILLEGAL_OPERATION, Message: "Illegal operation"})
				return
			}

		}

		blockNum += 1

		if n < int(state.blockSize) {
			break fileSend
		}
	}
}

func (server *Server) handleWRQ(state *connectionState, request *WriteRequest, replyConn net.PacketConn, remoteAddr net.Addr) {
	server.log.Printf("WRQ(%v): file=%v mode=%v", remoteAddr, request.Filename, request.Mode)

	// Ensure the file does not already exist.
	fullPath := path.Join(server.root, request.Filename)
	stat, err := os.Stat(fullPath)
	if err == nil {
		state.send(&Error{Code: ERR_FILE_EXISTS, Message: "File already exists"})
		return
	}
	server.log.Printf("fullPath=%v, stat=%#v", fullPath, stat)

	f, err := os.OpenFile(fullPath, os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		state.send(&Error{Code: ERR_NOT_DEFINED, Message: "Unable to create file"})
		return
	}
	defer f.Close()

	var hasBlockSizeOption = false
	if requestedBlockSizeString, ok := request.Options["blksize"]; ok {
		// RFC 2348: "Valid values range between '8' and '65464' octets, inclusive.  The
		// blocksize refers to the number of data octets; it does not include the four octets
		// of TFTP header."
		requestedBlockSize, err := strconv.Atoi(requestedBlockSizeString)
		if err != nil || requestedBlockSize < MIN_BLOCK_SIZE {
			state.send(&Error{Code: ERR_INVALID_OPTIONS, Message: "Invalid blksize option"})
			return
		}

		if requestedBlockSize > int(server.config.MaxBlockSize) {
			requestedBlockSize = int(server.config.MaxBlockSize)
		}

		state.blockSize = uint16(requestedBlockSize)
		hasBlockSizeOption = true
	}

	expectedTranferSizeString, hasTransferSizeOption := request.Options["tsize"]

	var hasTimeoutOption = false
	if requestedTimeoutString, ok := request.Options["timeout"]; ok {
		requestedTimeout, err := strconv.Atoi(requestedTimeoutString)
		if err != nil || requestedTimeout < 1 {
			state.send(&Error{Code: ERR_INVALID_OPTIONS, Message: "Invalid timeout option"})
			return
		}

		if requestedTimeout > 5*60 {
			requestedTimeout = 5 * 60
		}

		state.timeout = requestedTimeout
		hasTimeoutOption = true
	}

	var nextDataPacket *Data

	if hasBlockSizeOption || hasTransferSizeOption || hasTimeoutOption {
		oack := OptionsAck{Options: make(map[string]string)}
		if hasBlockSizeOption {
			oack.Options["blksize"] = strconv.Itoa(int(state.blockSize))
		}
		if hasTransferSizeOption {
			oack.Options["tsize"] = expectedTranferSizeString
		}
		if hasTimeoutOption {
			oack.Options["timeout"] = strconv.Itoa(state.timeout)
		}

	oackSend:
		for {
			state.send(&oack)

			rawPacket, err := state.receive()
			if err != nil {
				if err.(net.Error).Timeout() {
					continue
				}
				return
			}

			switch packet := rawPacket.(type) {
			case Data:
				if packet.Block == 1 {
					// Client acknowledged the options. Break out of the outer loop.
					nextDataPacket = &packet
					break oackSend
				}

			case Error:
				server.log.Printf("Client returned error: %v", packet.Message)
				return

			default:
				server.log.Printf("Unexpected packet: %#v", packet)
				state.send(&Error{Code: ERR_ILLEGAL_OPERATION, Message: "Illegal operation"})
				return
			}
		}
	} else {
		ack := Ack{Block: 0}
	ackSend:
		for {
			state.send(&ack)

			rawPacket, err := state.receive()
			if err != nil {
				if err.(net.Error).Timeout() {
					continue
				}
				return
			}

			switch packet := rawPacket.(type) {
			case Data:
				// Client acknowledged the options. Break out of the outer loop.
				nextDataPacket = &packet
				break ackSend

			case Error:
				server.log.Printf("Client returned error: %v", packet.Message)
				return

			default:
				server.log.Printf("Unexpected packet: %#v", packet)
				state.send(&Error{Code: ERR_ILLEGAL_OPERATION, Message: "Illegal operation"})
				return
			}
		}
	}

	server.log.Printf("state.blockSize=%v, timeout=", state.blockSize, state.timeout)

	var blockNum uint16 = 1

dataBlockLoop:
	for {
		// Write the next data packet to the file.
		f.Write(nextDataPacket.Data)

		// Send an acknowledgement to the client.
		ack := Ack{Block: blockNum}

	ackSend2:
		for {
			state.send(&ack)

			if len(nextDataPacket.Data) < int(state.blockSize) {
				break dataBlockLoop
			}

			rawPacket, err := state.receive()
			if err != nil {
				if err.(net.Error).Timeout() {
					continue ackSend2
				}
				return
			}

			switch packet := rawPacket.(type) {
			case Data:
				// Client acknowledged the options. Break out of the outer loop.
				nextDataPacket = &packet
				blockNum += 1
				continue dataBlockLoop

			case Error:
				server.log.Printf("Client returned error: %v", packet.Message)
				return

			default:
				server.log.Printf("Unexpected packet: %#v", packet)
				state.send(&Error{Code: ERR_ILLEGAL_OPERATION, Message: "Illegal operation"})
				return
			}
		}

	}
}

func (server *Server) handleRequest(requestBytes []byte, remoteAddr net.Addr) {
	rawRequest, err := PacketFromBytes(requestBytes)
	if err != nil {
		log.Printf("Malformed packet. Ignoring.")
		return
	}

	// Bind a new socket for the reply.
	replyConn, err := net.ListenPacket("udp", ":0")
	if err != nil {
		log.Printf("Could not create reply socket: %v", err)
		return
	}
	defer replyConn.Close()

	state := connectionState{
		buffer:       make([]byte, 65535),
		log:          server.log,
		conn:         replyConn,
		remoteAddr:   remoteAddr,
		blockSize:    DEFAULT_BLOCKSIZE,
		timeout:      5,
		tracePackets: true,
	}

	switch request := rawRequest.(type) {
	case ReadRequest:
		server.handleRRQ(&state, &request, replyConn, remoteAddr)

	case WriteRequest:
		server.handleWRQ(&state, &request, replyConn, remoteAddr)

	default:
		log.Printf("Non-RRQ/WRQ request from %v: %#v (%T)", remoteAddr, request, request)
		reply := Error{Code: ERR_ILLEGAL_OPERATION, Message: "Illegal TFTP operation"}
		replyConn.WriteTo(reply.ToBytes(), remoteAddr)
	}
}

func (server *Server) mainServerLoop() {
	buffer := make([]byte, 65535)

	for {
		n, remoteAddr, err := server.conn.ReadFrom(buffer)
		if err != nil {
			server.log.Printf("Unable to read from socket: %v", err)
			break
		}

		go server.handleRequest(buffer[0:n], remoteAddr)
	}

	server.done <- true
}

func validateServerConfig(config *ServerConfig) *ServerConfig {
	if config == nil {
		config = &ServerConfig{}
	}

	// RFC 2348: "Valid values range between '8' and '65464' octets, inclusive.  The
	// blocksize refers to the number of data octets; it does not include the four octets
	// of TFTP header."
	if config.MaxBlockSize == 0 {
		config.MaxBlockSize = MAX_BLOCK_SIZE
	}
	if config.MaxBlockSize < MIN_BLOCK_SIZE || config.MaxBlockSize > MAX_BLOCK_SIZE {
		panic("MaxBlockSize must be between 8 and 65464 inclusive.")
	}

	return config
}

func NewServer(address string, root string, config *ServerConfig) (*Server, error) {
	config = validateServerConfig(config)

	conn, err := net.ListenPacket("udp", address)
	if err != nil {
		return nil, err
	}

	stat, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !stat.IsDir() {
		return nil, RootMustBeAADirectoryError
	}

	server := Server{
		config: config,
		conn:   conn,
		root:   root,
		log:    log.New(os.Stderr, "TFTP: ", log.LstdFlags),
		done:   make(chan bool, 1),
	}

	go server.mainServerLoop()

	return &server, nil
}

func (s *Server) Close() {
	s.conn.Close()
}

func (s *Server) Join() {
	<-s.done
}
