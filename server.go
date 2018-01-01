package tftp

//import "context"
import (
	"log"
	"net"
	"os"
)

type Server struct {
	conn net.PacketConn
	log  *log.Logger
	done chan bool
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

	switch request := rawRequest.(type) {
	case ReadRequest:
		log.Printf("RRQ(%v): file=%v mode=%v", remoteAddr, request.Filename, request.Mode)
		reply := Error{Code: ERR_FILE_NOT_FOUND, Message: "File not found"}
		replyConn.WriteTo(reply.ToBytes(), remoteAddr)

	case WriteRequest:
		log.Printf("WRQ(%v): file=%v mode=%v", remoteAddr, request.Filename, request.Mode)
		reply := Error{Code: ERR_FILE_EXISTS, Message: "File already exists"}
		replyConn.WriteTo(reply.ToBytes(), remoteAddr)

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

func NewServer(address string) (*Server, error) {
	conn, err := net.ListenPacket("udp", address)
	if err != nil {
		return nil, err
	}

	server := Server{
		conn: conn,
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
