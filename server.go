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
	"time"
)

const (
	DEFAULT_BLOCKSIZE = 512
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

type Server struct {
	conn net.PacketConn
	root string
	log  *log.Logger
	done chan bool
}

type connectionState struct {
	buffer       []byte
	server       *Server
	conn         net.PacketConn
	remoteAddr   net.Addr
	blockSize    uint16
	timeout      int
	tracePackets bool
}

type packetMethods interface {
	ToBytes() []byte
	String() string
}

func (state *connectionState) send(packet packetMethods) (n int, err error) {
	if state.tracePackets {
		state.server.log.Printf("sending %s", packet.String())
	}
	return state.conn.WriteTo(packet.ToBytes(), state.remoteAddr)
}

func (state *connectionState) receive() (interface{}, error) {
	state.conn.SetReadDeadline(time.Now().Add(time.Duration(state.timeout) * time.Second))
	n, _, err := state.conn.ReadFrom(state.buffer)
	if err != nil {
		return nil, err
	}

	packet, err := PacketFromBytes(state.buffer[0:n])
	if err != nil {
		return nil, err
	}

	if state.tracePackets {
		state.server.log.Printf("received %s", packet.(packetMethods).String())
	}

	return packet, nil
}

func (state *connectionState) handleRRQ(request *ReadRequest, replyConn net.PacketConn, remoteAddr net.Addr) {
	state.server.log.Printf("RRQ(%v): file=%v mode=%v", remoteAddr, request.Filename, request.Mode)

	// Ensure the file exists
	fullPath := path.Join(state.server.root, request.Filename)
	stat, err := os.Stat(fullPath)
	if err != nil || stat.IsDir() {
		state.send(&Error{Code: ERR_FILE_NOT_FOUND, Message: "File not found"})
		return
	}
	state.server.log.Printf("fullPath=%v, stat=%#v", fullPath, stat)

	f, err := os.Open(fullPath)
	if err != nil {
		state.send(&Error{Code: ERR_FILE_NOT_FOUND, Message: "File not found"})
		return
	}
	defer f.Close()

	var hasBlockSizeOption = false
	if requestedBlockSizeString, ok := request.Options["blksize"]; ok {
		requestedBlockSize, err := strconv.Atoi(requestedBlockSizeString)
		if err != nil || requestedBlockSize < 1 {
			state.send(&Error{Code: ERR_INVALID_OPTIONS, Message: "Invalid blksize option"})
			return
		}

		if requestedBlockSize > 16384 {
			requestedBlockSize = 16384
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

	if hasBlockSizeOption || hasTransferSizeOption || hasTimeoutOption {
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
				state.server.log.Printf("Client returned error: %v", packet.Message)
				return

			default:
				state.server.log.Printf("Unexpected packet: %#v", packet)
				state.send(&Error{Code: ERR_ILLEGAL_OPERATION, Message: "Illegal operation"})
				return
			}
		}
	}

	state.server.log.Printf("state.blockSize=%v, timeout=", state.blockSize, state.timeout)

	// Start sending the file.
	fileBuffer := make([]byte, state.blockSize)
	var blockNum uint16 = 1
fileSend:
	for {
		n, err := readExact(f, fileBuffer)
		if err != nil {
			state.server.log.Printf("readExact failed with: %v", err)
			return
		}

		dataPacket := Data{Block: blockNum, Data: fileBuffer[0:n]}

	dataSend:
		for {
			_, err := state.send(&dataPacket)
			if err != nil {
				state.server.log.Printf("Failed to send DATA: err=%v", n, err)
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
				state.server.log.Printf("Client returned error: %v", packet.Message)
				return

			default:
				state.server.log.Printf("Unexpected packet: %#v", packet)
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

func (state *connectionState) handleWRQ(request *WriteRequest, replyConn net.PacketConn, remoteAddr net.Addr) {
	state.server.log.Printf("WRQ(%v): file=%v mode=%v", remoteAddr, request.Filename, request.Mode)

	// Ensure the file does not already exist.
	fullPath := path.Join(state.server.root, request.Filename)
	stat, err := os.Stat(fullPath)
	if err == nil {
		state.send(&Error{Code: ERR_FILE_EXISTS, Message: "File already exists"})
		return
	}
	state.server.log.Printf("fullPath=%v, stat=%#v", fullPath, stat)

	f, err := os.OpenFile(fullPath, os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		state.send(&Error{Code: ERR_NOT_DEFINED, Message: "Unable to create file"})
		return
	}
	defer f.Close()

	var hasBlockSizeOption = false
	if requestedBlockSizeString, ok := request.Options["blksize"]; ok {
		requestedBlockSize, err := strconv.Atoi(requestedBlockSizeString)
		if err != nil || requestedBlockSize < 1 {
			state.send(&Error{Code: ERR_INVALID_OPTIONS, Message: "Invalid blksize option"})
			return
		}

		if requestedBlockSize > 16384 {
			requestedBlockSize = 16384
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
				state.server.log.Printf("Client returned error: %v", packet.Message)
				return

			default:
				state.server.log.Printf("Unexpected packet: %#v", packet)
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
				state.server.log.Printf("Client returned error: %v", packet.Message)
				return

			default:
				state.server.log.Printf("Unexpected packet: %#v", packet)
				state.send(&Error{Code: ERR_ILLEGAL_OPERATION, Message: "Illegal operation"})
				return
			}
		}
	}

	state.server.log.Printf("state.blockSize=%v, timeout=", state.blockSize, state.timeout)

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
				state.server.log.Printf("Client returned error: %v", packet.Message)
				return

			default:
				state.server.log.Printf("Unexpected packet: %#v", packet)
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
		server:       server,
		conn:         replyConn,
		remoteAddr:   remoteAddr,
		blockSize:    DEFAULT_BLOCKSIZE,
		timeout:      5,
		tracePackets: true,
	}

	switch request := rawRequest.(type) {
	case ReadRequest:
		state.handleRRQ(&request, replyConn, remoteAddr)

	case WriteRequest:
		state.handleWRQ(&request, replyConn, remoteAddr)

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

func NewServer(address string, root string) (*Server, error) {
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
		conn: conn,
		root: root,
		log:  log.New(os.Stderr, "TFTP: ", log.LstdFlags),
		done: make(chan bool, 1),
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
