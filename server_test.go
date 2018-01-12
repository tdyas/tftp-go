package tftp

import (
	"bytes"
	"context"
	"io"
	"io/ioutil"
	"math/rand"
	"net"
	"strconv"
	"testing"
	"log"
	"time"
)

type testStep struct {
	Send    packetMethods
	Receive packetMethods
}

func runTest(t *testing.T, server *Server, steps []testStep) {
	d, _ := time.ParseDuration("50ms")
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(d))
	defer cancel()

	mainRemoteAddr := server.LocalAddr()
	var remoteAddr net.Addr

	// Create a local socket as the "client" for this test.
	clientConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Errorf("Unable to open socket: %v", err)
		return
	}

	clientPChan, err := NewPacketChan(clientConn, 1, 1)
	if err != nil {
		t.Errorf("Unable to open socket: %v", err)
		return
	}
	defer clientPChan.Close()

	// Loop through the test steps and drive the server.
	for _, step := range steps {
		if step.Send != nil {
			sendAddr := remoteAddr
			if remoteAddr == nil {
				sendAddr = mainRemoteAddr
			}

			clientPChan.Outgoing <- Packet{step.Send.ToBytes(), sendAddr}
		} else if step.Receive != nil {
			select {
			case rawPacket := <-clientPChan.Incoming:
				if remoteAddr == nil {
					remoteAddr = rawPacket.Addr
				}

				expectedBytes := step.Receive.ToBytes()
				actualBytes := rawPacket.Data

				packet, err := PacketFromBytes(actualBytes)
				if err != nil {
					t.Errorf("Unable to decode packet: %v", err)
					return
				}

				if !bytes.Equal(expectedBytes, actualBytes) {
					t.Errorf("packet mismatch: expected=[%s], actual=[%s]", step.Receive, packet)
					return
				}

			case <-ctx.Done():
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

	t.Run("file not found", func(t *testing.T) {
		runTest(t, server, []testStep{
			{Send: ReadRequest{Filename: "xyzzy", Mode: "octet"}},
			{Receive: Error{Code: ERR_FILE_NOT_FOUND, Message: "File not found"}},
		})
	})

	t.Run("basic read request", func(t *testing.T) {
		runTest(t, server, []testStep{
			{Send: ReadRequest{Filename: "768", Mode: "octet"}},
			{Receive: Data{Block: 1, Data: data[0:512]}},
			{Send: Ack{Block: 1}},
			{Receive: Data{Block: 2, Data: data[512:768]}},
			{Send: Ack{Block: 2}},
		})
	})

	t.Run("block aligned read", func(t *testing.T) {
		runTest(t, server, []testStep{
			{Send: ReadRequest{Filename: "1024", Mode: "octet"}},
			{Receive: Data{Block: 1, Data: data[0:512]}},
			{Send: Ack{Block: 1}},
			{Receive: Data{Block: 2, Data: data[512:1024]}},
			{Send: Ack{Block: 2}},
			{Receive: Data{Block: 3, Data: []byte{}}},
			{Send: Ack{Block: 3}},
		})
	})

	t.Run("blksize option rejected (too small)", func(t *testing.T) {
		runTest(t, server, []testStep{
			{Send: ReadRequest{Filename: "1024", Mode: "octet", Options: map[string]string{
				"blksize": strconv.Itoa(MIN_BLOCK_SIZE - 1)}}},
			{Receive: Error{Code: 8, Message: "Invalid blksize option"}},
		})
	})

	t.Run("blksize option rejected (not a number)", func(t *testing.T) {
		runTest(t, server, []testStep{
			{Send: ReadRequest{Filename: "1024", Mode: "octet", Options: map[string]string{
				"blksize": "xyzzy"}}},
			{Receive: Error{Code: 8, Message: "Invalid blksize option"}},
		})
	})

	t.Run("too large blksize option clamped", func(t *testing.T) {
		runTest(t, server, []testStep{
			{Send: ReadRequest{Filename: "1024", Mode: "octet", Options: map[string]string{
				"blksize": strconv.Itoa(MAX_BLOCK_SIZE + 1)}}},
			{Receive: OptionsAck{map[string]string{"blksize": strconv.Itoa(MAX_BLOCK_SIZE)}}},
			{Send: Ack{0}},
			{Receive: Data{Block: 1, Data: data[0:1024]}},
			{Send: Ack{1}},
		})
	})

	t.Run("larger blksize read", func(t *testing.T) {
		runTest(t, server, []testStep{
			{Send: ReadRequest{Filename: "1024", Mode: "octet", Options: map[string]string{
				"blksize": "768"}}},
			{Receive: OptionsAck{map[string]string{"blksize": "768"}}},
			{Send: Ack{0}},
			{Receive: Data{Block: 1, Data: data[0:768]}},
			{Send: Ack{1}},
			{Receive: Data{Block: 2, Data: data[768:1024]}},
			{Send: Ack{2}},
		})
	})

	t.Run("tsize option", func(t *testing.T) {
		runTest(t, server, []testStep{
			{Send: ReadRequest{Filename: "768", Mode: "octet", Options: map[string]string{
				"tsize": "0"}}},
			{Receive: OptionsAck{map[string]string{"tsize": "768"}}},
			{Send: Ack{0}},
			{Receive: Data{Block: 1, Data: data[0:512]}},
			{Send: Ack{Block: 1}},
			{Receive: Data{Block: 2, Data: data[512:768]}},
			{Send: Ack{Block: 2}},
		})
	})

	t.Run("timeout option", func(t *testing.T) {
		runTest(t, server, []testStep{
			{Send: ReadRequest{Filename: "768", Mode: "octet", Options: map[string]string{
				"timeout": "2"}}},
			{Receive: OptionsAck{map[string]string{"timeout": "2"}}},
			{Send: Ack{0}},
			{Receive: Data{Block: 1, Data: data[0:512]}},
			{Send: Ack{Block: 1}},
			{Receive: Data{Block: 2, Data: data[512:768]}},
			{Send: Ack{Block: 2}},
		})
	})

	t.Run("no write support", func(t *testing.T) {
		runTest(t, server, []testStep{
			{Send: WriteRequest{Filename: "xyzzy", Mode: "octet"}},
			{Receive: Error{Code: ERR_ILLEGAL_OPERATION, Message: "Write requests are not supported."}},
		})
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

	t.Run("file already exists", func(t *testing.T) {
		runTest(t, server, []testStep{
			{Send: WriteRequest{Filename: "xyzzy", Mode: "octet"}},
			{Receive: Error{Code: ERR_FILE_EXISTS, Message: "File already exists"}},
		})
	})

	t.Run("basic write", func(t *testing.T) {
		runTest(t, server, []testStep{
			{Send: WriteRequest{Filename: "768", Mode: "octet"}},
			{Receive: Ack{Block: 0}},
			{Send: Data{Block: 1, Data: data[0:512]}},
			{Receive: Ack{Block: 1}},
			{Send: Data{Block: 2, Data: data[512:768]}},
			{Receive: Ack{Block: 2}},
		})
		if !bytes.Equal(data[0:768], buffer.Bytes()) {
			t.Errorf("Results do not match")
		}
	})

	t.Run("block aligned write", func(t *testing.T) {
		runTest(t, server, []testStep{
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
			t.Errorf("Results do not match")
		}
	})

	t.Run("blksize option rejected (too small)", func(t *testing.T) {
		runTest(t, server, []testStep{
			{Send: WriteRequest{Filename: "1024", Mode: "octet", Options: map[string]string{
				"blksize": strconv.Itoa(MIN_BLOCK_SIZE - 1)}}},
			{Receive: Error{Code: 8, Message: "Invalid blksize option"}},
		})
	})

	t.Run("blksize option rejected (not a number)", func(t *testing.T) {
		runTest(t, server, []testStep{
			{Send: WriteRequest{Filename: "1024", Mode: "octet", Options: map[string]string{
				"blksize": "xyzzy"}}},
			{Receive: Error{Code: 8, Message: "Invalid blksize option"}},
		})
	})

	t.Run("too large blksize option clamped", func(t *testing.T) {
		runTest(t, server, []testStep{
			{Send: WriteRequest{Filename: "1024", Mode: "octet", Options: map[string]string{
				"blksize": strconv.Itoa(MAX_BLOCK_SIZE + 1)}}},
			{Receive: OptionsAck{map[string]string{"blksize": strconv.Itoa(MAX_BLOCK_SIZE)}}},
			{Send: Data{Block: 1, Data: data[0:1024]}},
			{Receive: Ack{1}},
		})
		if !bytes.Equal(data[0:1024], buffer.Bytes()) {
			t.Errorf("Results do not match")
		}
	})

	t.Run("larger blksize write", func(t *testing.T) {
		runTest(t, server, []testStep{
			{Send: WriteRequest{Filename: "1024", Mode: "octet", Options: map[string]string{
				"blksize": "768"}}},
			{Receive: OptionsAck{map[string]string{"blksize": "768"}}},
			{Send: Data{Block: 1, Data: data[0:768]}},
			{Receive: Ack{1}},
			{Send: Data{Block: 2, Data: data[768:1024]}},
			{Receive: Ack{2}},
		})
		if !bytes.Equal(data[0:1024], buffer.Bytes()) {
			t.Errorf("Results do not match")
		}
	})

	t.Run("no read support", func(t *testing.T) {
		runTest(t, server, []testStep{
			{Send: ReadRequest{Filename: "xyzzy", Mode: "octet"}},
			{Receive: Error{Code: ERR_FILE_NOT_FOUND, Message: "File not found"}},
		})

	})
}
