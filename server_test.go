package tftp

import (
	"bytes"
	"io"
	"io/ioutil"
	"math/rand"
	"net"
	"strconv"
	"time"
	"testing"
	"log"
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
		GetWriteStream: func(filename string) (io.WriteCloser, error) {
			return nil, OperationNotSupportedError
		},
		Logger: log.New(ioutil.Discard, "", 0),
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

	runTest(t, server, "block aligned read", []testStep{
		{Send: ReadRequest{Filename: "1024", Mode: "octet"}},
		{Receive: Data{Block: 1, Data: data[0:512]}},
		{Send: Ack{Block: 1}},
		{Receive: Data{Block: 2, Data: data[512:1024]}},
		{Send: Ack{Block: 2}},
		{Receive: Data{Block: 3, Data: []byte{}}},
		{Send: Ack{Block: 3}},
	})

	runTest(t, server, "blksize option rejected (too small)", []testStep{
		{Send: ReadRequest{Filename: "1024", Mode: "octet", Options: map[string]string{
			"blksize": strconv.Itoa(MIN_BLOCK_SIZE - 1)}}},
		{Receive: Error{Code: 8, Message: "Invalid blksize option"}},
	})

	runTest(t, server, "blksize option rejected (not a number)", []testStep{
		{Send: ReadRequest{Filename: "1024", Mode: "octet", Options: map[string]string{
			"blksize": "xyzzy"}}},
		{Receive: Error{Code: 8, Message: "Invalid blksize option"}},
	})

	runTest(t, server, "too large blksize option clamped", []testStep{
		{Send: ReadRequest{Filename: "1024", Mode: "octet", Options: map[string]string{
			"blksize": strconv.Itoa(MAX_BLOCK_SIZE + 1)}}},
		{Receive: OptionsAck{map[string]string{"blksize": strconv.Itoa(MAX_BLOCK_SIZE)}}},
		{Send: Ack{0}},
		{Receive: Data{Block: 1, Data: data[0:1024]}},
		{Send: Ack{1}},
	})

	runTest(t, server, "larger blksize read", []testStep{
		{Send: ReadRequest{Filename: "1024", Mode: "octet", Options: map[string]string{
			"blksize": "768"}}},
		{Receive: OptionsAck{map[string]string{"blksize": "768"}}},
		{Send: Ack{0}},
		{Receive: Data{Block: 1, Data: data[0:768]}},
		{Send: Ack{1}},
		{Receive: Data{Block: 2, Data: data[768:1024]}},
		{Send: Ack{2}},
	})

	runTest(t, server, "tsize option", []testStep{
		{Send: ReadRequest{Filename: "768", Mode: "octet", Options: map[string]string{
			"tsize": "0"}}},
		{Receive: OptionsAck{map[string]string{"tsize": "768"}}},
		{Send: Ack{0}},
		{Receive: Data{Block: 1, Data: data[0:512]}},
		{Send: Ack{Block: 1}},
		{Receive: Data{Block: 2, Data: data[512:768]}},
		{Send: Ack{Block: 2}},
	})

	runTest(t, server, "timeout option", []testStep{
		{Send: ReadRequest{Filename: "768", Mode: "octet", Options: map[string]string{
			"timeout": "2"}}},
		{Receive: OptionsAck{map[string]string{"timeout": "2"}}},
		{Send: Ack{0}},
		{Receive: Data{Block: 1, Data: data[0:512]}},
		{Send: Ack{Block: 1}},
		{Receive: Data{Block: 2, Data: data[512:768]}},
		{Send: Ack{Block: 2}},
	})

	runTest(t, server, "no write support", []testStep{
		{Send: WriteRequest{Filename: "xyzzy", Mode: "octet"}},
		{Receive: Error{Code: ERR_ILLEGAL_OPERATION, Message: "Write requests are not supported."}},
	})
}

type closableBuffer struct {
	bytes.Buffer
}

func (b *closableBuffer) Close() error {
	return nil
}

func TestWriteSupport(t *testing.T) {
	data := make([]byte, 3*512)
	_, err := rand.Read(data)
	if err != nil {
		t.Errorf("Unable to fill buffer: %v", err)
		return
	}

	var buffer closableBuffer

	var config = ServerConfig{
		TracePackets: true,
		GetReadStream: func(filename string) (io.ReadCloser, int64, error) {
			return nil, -1, OperationNotSupportedError
		},
		GetWriteStream: func(filename string) (io.WriteCloser, error) {
			_, err := strconv.Atoi(filename)
			if err != nil {
				return nil, FileExistsError
			}

			buffer.Truncate(0)
			return &buffer, nil
		},
		Logger: log.New(ioutil.Discard, "", 0),
	}

	server, err := NewServer("127.0.0.1:0", &config)
	if err != nil {
		t.Errorf("Unable to create server: %v", err)
		return
	}
	defer server.Close()

	runTest(t, server, "file already exists", []testStep{
		{Send: WriteRequest{Filename: "xyzzy", Mode: "octet"}},
		{Receive: Error{Code: ERR_FILE_EXISTS, Message: "File already exists"}},
	})

	runTest(t, server, "basic write", []testStep{
		{Send: WriteRequest{Filename: "768", Mode: "octet"}},
		{Receive: Ack{Block: 0}},
		{Send: Data{Block: 1, Data: data[0:512]}},
		{Receive: Ack{Block: 1}},
		{Send: Data{Block: 2, Data: data[512:768]}},
		{Receive: Ack{Block: 2}},
	})
	if !bytes.Equal(data[0:768], buffer.Bytes()) {
		t.Errorf("Test 'basic write': Written results do not match.")
	}

	runTest(t, server, "block aligned write", []testStep{
		{Send: WriteRequest{Filename: "1024", Mode: "octet"}},
		{Receive: Ack{Block: 0}},
		{Send: Data{Block: 1, Data: data[0:512]}},
		{Receive: Ack{Block: 1}},
		{Send: Data{Block: 2, Data: data[512:1024]}},
		{Receive: Ack{Block: 2}},
		{Send: Data{Block: 3, Data: []byte{}}},
		{Receive: Ack{Block: 3}},
	})
	if !bytes.Equal(data[0:1024], buffer.Bytes()) {
		t.Errorf("Test 'block aligned write': Written results do not match.")
	}

	runTest(t, server, "no read support", []testStep{
		{Send: ReadRequest{Filename: "xyzzy", Mode: "octet"}},
		{Receive: Error{Code: ERR_FILE_NOT_FOUND, Message: "File not found"}},
	})
}
