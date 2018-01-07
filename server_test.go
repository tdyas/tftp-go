package tftp

import (
	"io"
	"io/ioutil"
	"math/rand"
	"net"
	"testing"
	"bytes"
	"strconv"
	"time"
)

type testStep struct {
	Send    packetMethods
	Receive packetMethods
}

func runTest(t *testing.T, server *Server, testName string, steps []testStep) {
	fullTestName := "Test '" + testName + "'"
	buffer := make([]byte, 65535)

	mainRemoteAddr := server.LocalAddr()
	var remoteAddr net.Addr

	// Create a local socket as the "client" for this test.
	clientConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Errorf("%s: Unable to open socket: %v", fullTestName, err)
		return
	}
	defer clientConn.Close()

	// Loop through the test steps and drive the server.
	for _, step := range steps {
		if step.Send != nil {
			sendAddr := remoteAddr
			if remoteAddr == nil {
				sendAddr = mainRemoteAddr
			}

			_, err := clientConn.WriteTo(step.Send.ToBytes(), sendAddr)
			if err != nil {
				t.Errorf("%s: Unable to write to socket: %v", fullTestName, err)
				return
			}
		} else if step.Receive != nil {
			clientConn.SetReadDeadline(time.Now().Add(10 * time.Millisecond))

			n, thisRemoteAddr, err := clientConn.ReadFrom(buffer)
			if err != nil {
				t.Errorf("%s: Unable to read from socket: %v", fullTestName, err)
				return
			}

			if remoteAddr == nil {
				remoteAddr = thisRemoteAddr
			}

			expectedBytes := step.Receive.ToBytes()
			actualBytes := buffer[0:n]

			packet, err := PacketFromBytes(actualBytes)
			if err != nil {
				t.Errorf("%s: Unable to decode packet: %v", fullTestName, err)
				return
			}

			if !bytes.Equal(expectedBytes, actualBytes) {
				t.Errorf("%s: packet mismatch: expected=[%s], actual=[%s]",
					fullTestName, step.Receive, packet)
				return
			}
		}
	}
}

func TestReadSupport(t *testing.T) {
	data := make([]byte, 3*512)
	_, err := rand.Read(data)
	if err != nil {
		t.Errorf("Unable to fill buffer: %v", err)
		return
	}

	var config = ServerConfig{
		TracePackets: true,
		GetReadStream: func(filename string) (io.ReadCloser, int64, error) {
			sizeToRead, err := strconv.Atoi(filename)
			if err != nil {
				return nil, -1, FileNotFoundError
			}

			return ioutil.NopCloser(bytes.NewReader(data[0:sizeToRead])), int64(sizeToRead), nil
		},
	}

	server, err := NewServer("127.0.0.1:0", &config)
	if err != nil {
		t.Errorf("Unable to create server: %v", err)
		return
	}
	defer server.Close()

	runTest(t, server, "file not found", []testStep{
		{Send: ReadRequest{Filename: "xyzzy", Mode: "octet"}},
		{Receive: Error{Code: ERR_FILE_NOT_FOUND, Message: "File not found"}},
	})

	runTest(t, server, "basic read", []testStep{
		{Send: ReadRequest{Filename: "768", Mode: "octet"}},
		{Receive: Data{Block: 1, Data: data[0:512]}},
		{Send: Ack{Block: 1}},
		{Receive: Data{Block: 2, Data: data[512:768]}},
		{Send: Ack{Block: 2}},
	})

	runTest(t, server, "block aligned", []testStep{
		{Send: ReadRequest{Filename: "1024", Mode: "octet"}},
		{Receive: Data{Block: 1, Data: data[0:512]}},
		{Send: Ack{Block: 1}},
		{Receive: Data{Block: 2, Data: data[512:1024]}},
		{Send: Ack{Block: 2}},
		{Receive: Data{Block: 3, Data: []byte{}}},
		{Send: Ack{Block: 3}},
	})
}
