package tftp

import (
	"bytes"
	"context"
	"fmt"
	"github.com/stretchr/testify/assert"
	"io"
	"io/ioutil"
	"log"
	"math/rand"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"
)

type testStep struct {
	Send    packetMethods
	Receive packetMethods
}

func runTest(t *testing.T, mainRemoteAddr net.Addr, steps []testStep) {
	t.Parallel()

	ctx, _ := context.WithTimeout(context.Background(), time.Duration(5)*time.Second)

	var remoteAddr net.Addr

	// Create a local socket as the "client" for this test.
	clientConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	assert.NoError(t, err)
	defer clientConn.Close()

	client, err := NewPacketChan(clientConn, 1, 1)
	assert.NoError(t, err)
	defer client.Close()

	// Loop through the test steps and drive the server.
	for _, step := range steps {
		if step.Send != nil {
			sendAddr := remoteAddr
			if remoteAddr == nil {
				sendAddr = mainRemoteAddr
			}

			sent := make(chan error, 1)
			client.Outgoing <- Packet{step.Send.ToBytes(), sendAddr, sent}
			select {
			case err := <-sent:
				assert.NoError(t, err, "send failed for packet %v", step.Send)
			case <-ctx.Done():
				return
			}
		} else if step.Receive != nil {
			select {
			case rawPacket := <-client.Incoming:
				if remoteAddr == nil {
					remoteAddr = rawPacket.Addr
				}

				expectedBytes := step.Receive.ToBytes()
				actualBytes := rawPacket.Data

				packet, err := PacketFromBytes(actualBytes)
				assert.NoError(t, err, "Unable to decode packet")

				t.Logf("received: %v", packet)

				assert.Equal(t, expectedBytes, actualBytes, "packet mismatch: expected=[%s], actual=[%s]", step.Receive, packet)

			case <-ctx.Done():
				assert.FailNow(t, "Context cancelled: %v", ctx.Err())
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
	assert.NoError(t, err)
	defer server.Close()

	mainRemoteAddr := server.LocalAddr()

	// All of the actual tests must be run inside a subtest so that we do not close the
	// TFTP server prematurely (via the `defer` call above). This call to `t.Run` will
	// block until all of the parallel tests have completed.
	t.Run("t", func(t *testing.T) {
		t.Run("file not found", func(t *testing.T) {
			runTest(t, mainRemoteAddr, []testStep{
				{Send: ReadRequest{Filename: "xyzzy", Mode: "octet"}},
				{Receive: Error{Code: ERR_FILE_NOT_FOUND, Message: "File not found"}},
			})
		})

		t.Run("basic read request", func(t *testing.T) {
			runTest(t, mainRemoteAddr, []testStep{
				{Send: ReadRequest{Filename: "768", Mode: "octet"}},
				{Receive: Data{Block: 1, Data: data[0:512]}},
				{Send: Ack{Block: 1}},
				{Receive: Data{Block: 2, Data: data[512:768]}},
				{Send: Ack{Block: 2}},
			})
		})

		t.Run("block aligned read", func(t *testing.T) {
			runTest(t, mainRemoteAddr, []testStep{
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
			runTest(t, mainRemoteAddr, []testStep{
				{Send: ReadRequest{Filename: "1024", Mode: "octet", Options: map[string]string{
					"blksize": strconv.Itoa(MIN_BLOCK_SIZE - 1)}}},
				{Receive: Error{Code: 8, Message: "Invalid blksize option"}},
			})
		})

		t.Run("blksize option rejected (not a number)", func(t *testing.T) {
			runTest(t, mainRemoteAddr, []testStep{
				{Send: ReadRequest{Filename: "1024", Mode: "octet", Options: map[string]string{
					"blksize": "xyzzy"}}},
				{Receive: Error{Code: 8, Message: "Invalid blksize option"}},
			})
		})

		t.Run("too large blksize option clamped", func(t *testing.T) {
			runTest(t, mainRemoteAddr, []testStep{
				{Send: ReadRequest{Filename: "1024", Mode: "octet", Options: map[string]string{
					"blksize": strconv.Itoa(MAX_BLOCK_SIZE + 1)}}},
				{Receive: OptionsAck{map[string]string{"blksize": strconv.Itoa(MAX_BLOCK_SIZE)}}},
				{Send: Ack{0}},
				{Receive: Data{Block: 1, Data: data[0:1024]}},
				{Send: Ack{1}},
			})
		})

		t.Run("larger blksize read", func(t *testing.T) {
			runTest(t, mainRemoteAddr, []testStep{
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
			runTest(t, mainRemoteAddr, []testStep{
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
			runTest(t, mainRemoteAddr, []testStep{
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
			runTest(t, mainRemoteAddr, []testStep{
				{Send: WriteRequest{Filename: "xyzzy", Mode: "octet"}},
				{Receive: Error{Code: ERR_ILLEGAL_OPERATION, Message: "Write requests are not supported."}},
			})
		})
	})
}

type closableBuffer struct {
	bytes.Buffer
	closedSignal chan bool
}

func newCloseableBuffer() *closableBuffer {
	return &closableBuffer{closedSignal: make(chan bool, 1)}
}

func (b *closableBuffer) Close() error {
	b.closedSignal <- true
	close(b.closedSignal)
	return nil
}

func TestWriteSupport(t *testing.T) {
	t.Parallel()

	data := make([]byte, 3*512)
	_, err := rand.Read(data)
	if err != nil {
		t.Errorf("Unable to fill buffer: %v", err)
		return
	}

	var buffersMapLock sync.Mutex
	buffersMap := make(map[string]*closableBuffer)
	var nextBufferKey = 0

	// Allocate a buffer for a single transfer. The "key" for the buffer is passed
	// into config.GetWriteStrean as part of the filename.
	setupBuffer := func(t *testing.T) (*closableBuffer, string) {
		bufferKey := strconv.Itoa(nextBufferKey)
		nextBufferKey++

		buffer := newCloseableBuffer()

		t.Logf("bufferKey = %s", bufferKey)

		buffersMapLock.Lock()
		defer buffersMapLock.Unlock()

		buffersMap[bufferKey] = buffer

		return buffer, bufferKey
	}

	var config = ServerConfig{
		TracePackets: true,
		GetReadStream: func(filename string) (io.ReadCloser, int64, error) {
			return nil, -1, OperationNotSupportedError
		},
		GetWriteStream: func(filename string) (io.WriteCloser, error) {
			var size int
			var bufferKey string
			n, err := fmt.Sscanf(filename, "%d-%s", &size, &bufferKey)
			if err != nil || n != 2 {
				return nil, FileExistsError
			}

			buffersMapLock.Lock()
			defer buffersMapLock.Unlock()
			if buffer, ok := buffersMap[bufferKey]; ok {
				return buffer, nil
			} else {
				return nil, FileExistsError
			}
		},
		Logger: log.New(ioutil.Discard, "", 0),
	}

	server, err := NewServer("127.0.0.1:0", &config)
	assert.NoError(t, err)
	defer server.Close()

	mainRemoteAddr := server.LocalAddr()

	// All of the actual tests must be run inside a subtest so that we do not close the
	// TFTP server prematurely (via the `defer` call above). This call to `t.Run` will
	// block until all of the parallel tests have completed.
	t.Run("t", func(t *testing.T) {
		t.Run("file already exists", func(t *testing.T) {
			runTest(t, server.LocalAddr(), []testStep{
				{Send: WriteRequest{Filename: "xyzzy", Mode: "octet"}},
				{Receive: Error{Code: ERR_FILE_EXISTS, Message: "File already exists"}},
			})
		})

		t.Run("basic write", func(t *testing.T) {
			buffer, bufferKey := setupBuffer(t)
			runTest(t, mainRemoteAddr, []testStep{
				{Send: WriteRequest{Filename: "768-" + bufferKey, Mode: "octet"}},
				{Receive: Ack{Block: 0}},
				{Send: Data{Block: 1, Data: data[0:512]}},
				{Receive: Ack{Block: 1}},
				{Send: Data{Block: 2, Data: data[512:768]}},
				{Receive: Ack{Block: 2}},
			})
			<-buffer.closedSignal
			assert.Equal(t, data[0:768], buffer.Bytes())
		})

		t.Run("block aligned write", func(t *testing.T) {
			buffer, bufferKey := setupBuffer(t)
			runTest(t, mainRemoteAddr, []testStep{
				{Send: WriteRequest{Filename: "1024-" + bufferKey, Mode: "octet"}},
				{Receive: Ack{Block: 0}},
				{Send: Data{Block: 1, Data: data[0:512]}},
				{Receive: Ack{Block: 1}},
				{Send: Data{Block: 2, Data: data[512:1024]}},
				{Receive: Ack{Block: 2}},
				{Send: Data{Block: 3, Data: []byte{}}},
				{Receive: Ack{Block: 3}},
			})
			<-buffer.closedSignal
			assert.Equal(t, data[0:1024], buffer.Bytes())
		})

		t.Run("blksize option rejected (too small)", func(t *testing.T) {
			_, bufferKey := setupBuffer(t)
			runTest(t, mainRemoteAddr, []testStep{
				{Send: WriteRequest{Filename: "1024-" + bufferKey, Mode: "octet", Options: map[string]string{
					"blksize": strconv.Itoa(MIN_BLOCK_SIZE - 1)}}},
				{Receive: Error{Code: 8, Message: "Invalid blksize option"}},
			})
		})

		t.Run("blksize option rejected (not a number)", func(t *testing.T) {
			_, bufferKey := setupBuffer(t)
			runTest(t, server.LocalAddr(), []testStep{
				{Send: WriteRequest{Filename: "1024-" + bufferKey, Mode: "octet", Options: map[string]string{
					"blksize": "xyzzy"}}},
				{Receive: Error{Code: 8, Message: "Invalid blksize option"}},
			})
		})

		t.Run("too large blksize option clamped", func(t *testing.T) {
			buffer, bufferKey := setupBuffer(t)
			runTest(t, mainRemoteAddr, []testStep{
				{Send: WriteRequest{Filename: "1024-" + bufferKey, Mode: "octet", Options: map[string]string{
					"blksize": strconv.Itoa(MAX_BLOCK_SIZE + 1)}}},
				{Receive: OptionsAck{map[string]string{"blksize": strconv.Itoa(MAX_BLOCK_SIZE)}}},
				{Send: Data{Block: 1, Data: data[0:1024]}},
				{Receive: Ack{1}},
			})
			<-buffer.closedSignal
			assert.Equal(t, data[0:1024], buffer.Bytes())
		})

		t.Run("larger blksize write", func(t *testing.T) {
			buffer, bufferKey := setupBuffer(t)
			runTest(t, mainRemoteAddr, []testStep{
				{Send: WriteRequest{Filename: "1024-" + bufferKey, Mode: "octet", Options: map[string]string{
					"blksize": "768"}}},
				{Receive: OptionsAck{map[string]string{"blksize": "768"}}},
				{Send: Data{Block: 1, Data: data[0:768]}},
				{Receive: Ack{1}},
				{Send: Data{Block: 2, Data: data[768:1024]}},
				{Receive: Ack{2}},
			})
			<-buffer.closedSignal
			assert.Equal(t, data[0:1024], buffer.Bytes())
		})

		t.Run("no read support", func(t *testing.T) {
			runTest(t, mainRemoteAddr, []testStep{
				{Send: ReadRequest{Filename: "xyzzy", Mode: "octet"}},
				{Receive: Error{Code: ERR_FILE_NOT_FOUND, Message: "File not found"}},
			})
		})
	})
}
