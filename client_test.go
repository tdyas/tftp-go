package tftp

import (
	"bytes"
	"context"
	"log"
	"math/rand"
	"net"
	"testing"
	"time"
)

type testLogWriter struct {
	t *testing.T
}

func (l *testLogWriter) Write(p []byte) (n int, err error) {
	l.t.Log(string(p))
	return len(p), nil
}

func dummyServerLoop(ctx context.Context, t *testing.T, conn1 *PacketChan, conn2 *PacketChan, steps []testStep) {
	var clientAddr net.Addr
	gotFirstPacket := false

	for i, step := range steps {
		t.Logf("step %d of %d", i+1, len(steps))
		if step.Send != nil {
			if clientAddr == nil {
				t.Error("send configured before first receive")
				return
			}
			sent := make(chan error)
			conn2.Outgoing <- Packet{step.Send.ToBytes(), clientAddr, sent}

			select {
			case err := <-sent:
				if err != nil {
					t.Errorf("send failed for packet %v: %v", step.Send, err)
					return
				}
			case <-ctx.Done():
				return
			}
		} else if step.Receive != nil {
			select {
			case rawPacket := <-conn1.Incoming:
				if gotFirstPacket {
					t.Error("received packet on main port after initial packet")
					return
				}

				gotFirstPacket = true
				clientAddr = rawPacket.Addr

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

			case rawPacket := <-conn2.Incoming:
				if !gotFirstPacket {
					t.Error("received packet on main port after initial packet")
					return
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

func runClientTest(t *testing.T, f func(context.Context, net.Addr), steps []testStep) {
	d, _ := time.ParseDuration("100ms")
	ctx, _ := context.WithDeadline(context.Background(), time.Now().Add(d))
	//defer cancel()

	conn1, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Errorf("Unable to open socket: %v", err)
		return
	}

	pchan1, err := NewPacketChan(conn1, 1, 2)
	if err != nil {
		t.Errorf("Unable to open socket: %v", err)
		return
	}
	defer pchan1.Close()

	conn2, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Errorf("Unable to open socket: %v", err)
		return
	}

	pchan2, err := NewPacketChan(conn2, 1, 2)
	if err != nil {
		t.Errorf("Unable to open socket: %v", err)
		return
	}
	defer pchan2.Close()

	join := make(chan bool)
	go func() {
		dummyServerLoop(ctx, t, pchan1, pchan2, steps)
		//cancel()
		join <- true
	}()

	f(ctx, conn1.LocalAddr())

	timeout := time.After(time.Duration(1500) * time.Millisecond)
	select {
	case <-join:
		break

	case <-timeout:
		t.Error("Dummy server loop failed to exit")
	}
}

func TestGetFile(t *testing.T) {
	data := make([]byte, 3*512)
	_, err := rand.Read(data)
	if err != nil {
		t.Errorf("Unable to fill buffer: %v", err)
		return
	}

	t.Run("basic read request", func(t *testing.T) {
		runClientTest(t, func(ctx context.Context, serverAddr net.Addr) {
			var buffer bytes.Buffer
			config := ClientConfig{
				DisableTransferSizeOption: true,
				DisableBlockSizeOption:    true,
				TracePackets:              true,
				Logger:                    log.New(&testLogWriter{t}, "", 0),
			}

			err := GetFile(ctx, serverAddr.String(), "xyzzy", "octet", &config, &buffer)
			if err != nil {
				t.Errorf("GetFile failed: %v", err)
				return
			}
			if buffer.Len() != 768 {
				t.Error("Length does not match")
				return
			}
			if !bytes.Equal(data[0:768], buffer.Bytes()) {
				t.Error("Bytes read do not match")
				return
			}
		}, []testStep{
			{Receive: ReadRequest{Filename: "xyzzy", Mode: "octet"}},
			{Send: Data{Block: 1, Data: data[0:512]}},
			{Receive: Ack{Block: 1}},
			{Send: Data{Block: 2, Data: data[512:768]}},
			{Receive: Ack{Block: 2}},
		})
	})

	t.Run("block-aligned read request", func(t *testing.T) {
		runClientTest(t, func(ctx context.Context, serverAddr net.Addr) {
			var buffer bytes.Buffer
			config := ClientConfig{
				DisableTransferSizeOption: true,
				DisableBlockSizeOption:    true,
				TracePackets:              true,
				Logger:                    log.New(&testLogWriter{t}, "", 0),
			}

			err := GetFile(ctx, serverAddr.String(), "xyzzy", "octet", &config, &buffer)
			if err != nil {
				t.Errorf("GetFile failed: %v", err)
				return
			}
			if buffer.Len() != 1024 {
				t.Error("Length does not match")
				return
			}
			if !bytes.Equal(data[0:1024], buffer.Bytes()) {
				t.Error("Bytes read do not match")
				return
			}
		}, []testStep{
			{Receive: ReadRequest{Filename: "xyzzy", Mode: "octet"}},
			{Send: Data{Block: 1, Data: data[0:512]}},
			{Receive: Ack{Block: 1}},
			{Send: Data{Block: 2, Data: data[512:1024]}},
			{Receive: Ack{Block: 2}},
			{Send: Data{Block: 3, Data: []byte{}}},
			{Receive: Ack{Block: 3}},
		})
	})

	t.Run("read with tsize option", func(t *testing.T) {
		runClientTest(t, func(ctx context.Context, serverAddr net.Addr) {
			var buffer bytes.Buffer
			config := ClientConfig{
				DisableBlockSizeOption: true,
				TracePackets:           true,
				Logger:                 log.New(&testLogWriter{t}, "", 0),
			}

			err := GetFile(ctx, serverAddr.String(), "xyzzy", "octet", &config, &buffer)
			if err != nil {
				t.Errorf("GetFile failed: %v", err)
				return
			}
			if buffer.Len() != 768 {
				t.Error("Length does not match")
				return
			}
			if !bytes.Equal(data[0:768], buffer.Bytes()) {
				t.Error("Bytes read do not match")
				return
			}
		}, []testStep{
			{Receive: ReadRequest{Filename: "xyzzy", Mode: "octet", Options: map[string]string{"tsize": "0"}}},
			{Send: OptionsAck{Options: map[string]string{"tsize": "768"}}},
			{Receive: Ack{Block: 0}},
			{Send: Data{Block: 1, Data: data[0:512]}},
			{Receive: Ack{Block: 1}},
			{Send: Data{Block: 2, Data: data[512:768]}},
			{Receive: Ack{Block: 2}},
		})
	})

	t.Run("detect transfer size mismatch", func(t *testing.T) {
		runClientTest(t, func(ctx context.Context, serverAddr net.Addr) {
			var buffer bytes.Buffer
			config := ClientConfig{
				DisableBlockSizeOption: true,
				TracePackets:           true,
				Logger:                 log.New(&testLogWriter{t}, "", 0),
			}

			err := GetFile(ctx, serverAddr.String(), "xyzzy", "octet", &config, &buffer)
			if err != TransferSizeError {
				t.Errorf("GetFile failed unexpectedly: %v", err)
				return
			} else if err == nil {
				t.Error("GetFile should not have succeeded")
			}
		}, []testStep{
			{Receive: ReadRequest{Filename: "xyzzy", Mode: "octet", Options: map[string]string{"tsize": "0"}}},
			{Send: OptionsAck{Options: map[string]string{"tsize": "300"}}},
			{Receive: Ack{Block: 0}},
			{Send: Data{Block: 1, Data: data[0:256]}},
			{Receive: Ack{Block: 1}},
		})
	})

	t.Run("read with blksize option - non-block-aligned size", func(t *testing.T) {
		runClientTest(t, func(ctx context.Context, serverAddr net.Addr) {
			var buffer bytes.Buffer
			config := ClientConfig{
				DisableTransferSizeOption: true,
				TracePackets:              true,
				MaxBlockSize:              768,
				Logger:                    log.New(&testLogWriter{t}, "", 0),
			}

			err := GetFile(ctx, serverAddr.String(), "xyzzy", "octet", &config, &buffer)
			if err != nil {
				t.Errorf("GetFile failed: %v", err)
				return
			}
			if buffer.Len() != 1024 {
				t.Error("Length does not match")
				return
			}
			if !bytes.Equal(data[0:1024], buffer.Bytes()) {
				t.Error("Bytes read do not match")
				return
			}
		}, []testStep{
			{Receive: ReadRequest{Filename: "xyzzy", Mode: "octet", Options: map[string]string{"blksize": "768"}}},
			{Send: OptionsAck{Options: map[string]string{"blksize": "768"}}},
			{Receive: Ack{Block: 0}},
			{Send: Data{Block: 1, Data: data[0:768]}},
			{Receive: Ack{Block: 1}},
			{Send: Data{Block: 2, Data: data[768:1024]}},
			{Receive: Ack{Block: 2}},
		})
	})

	t.Run("read with blksize option - block-aligned size", func(t *testing.T) {
		runClientTest(t, func(ctx context.Context, serverAddr net.Addr) {
			var buffer bytes.Buffer
			config := ClientConfig{
				DisableTransferSizeOption: true,
				TracePackets:              true,
				MaxBlockSize:              768,
				Logger:                    log.New(&testLogWriter{t}, "", 0),
			}

			err := GetFile(ctx, serverAddr.String(), "xyzzy", "octet", &config, &buffer)
			if err != nil {
				t.Errorf("GetFile failed: %v", err)
				return
			}
			if buffer.Len() != 1536 {
				t.Error("Length does not match")
				return
			}
			if !bytes.Equal(data[0:1536], buffer.Bytes()) {
				t.Error("Bytes read do not match")
				return
			}
		}, []testStep{
			{Receive: ReadRequest{Filename: "xyzzy", Mode: "octet", Options: map[string]string{"blksize": "768"}}},
			{Send: OptionsAck{Options: map[string]string{"blksize": "768"}}},
			{Receive: Ack{Block: 0}},
			{Send: Data{Block: 1, Data: data[0:768]}},
			{Receive: Ack{Block: 1}},
			{Send: Data{Block: 2, Data: data[768:1536]}},
			{Receive: Ack{Block: 2}},
			{Send: Data{Block: 3, Data: []byte{}}},
			{Receive: Ack{Block: 3}},
		})
	})

	t.Run("read with blksize option modified by server", func(t *testing.T) {
		runClientTest(t, func(ctx context.Context, serverAddr net.Addr) {
			var buffer bytes.Buffer
			config := ClientConfig{
				DisableTransferSizeOption: true,
				TracePackets:              true,
				MaxBlockSize:              768,
				Logger:                    log.New(&testLogWriter{t}, "", 0),
			}

			err := GetFile(ctx, serverAddr.String(), "xyzzy", "octet", &config, &buffer)
			if err != nil {
				t.Errorf("GetFile failed: %v", err)
				return
			}
			if buffer.Len() != 1024 {
				t.Error("Length does not match")
				return
			}
			if !bytes.Equal(data[0:1024], buffer.Bytes()) {
				t.Error("Bytes read do not match")
				return
			}
		}, []testStep{
			{Receive: ReadRequest{Filename: "xyzzy", Mode: "octet", Options: map[string]string{"blksize": "768"}}},
			{Send: OptionsAck{Options: map[string]string{"blksize": "384"}}},
			{Receive: Ack{Block: 0}},
			{Send: Data{Block: 1, Data: data[0:384]}},
			{Receive: Ack{Block: 1}},
			{Send: Data{Block: 2, Data: data[384:768]}},
			{Receive: Ack{Block: 2}},
			{Send: Data{Block: 3, Data: data[768:1024]}},
			{Receive: Ack{Block: 3}},
		})
	})
}

func TestPutFile(t *testing.T) {
	data := make([]byte, 3*512)
	_, err := rand.Read(data)
	if err != nil {
		t.Errorf("Unable to fill buffer: %v", err)
		return
	}

	t.Run("basic write request", func(t *testing.T) {
		runClientTest(t, func(ctx context.Context, serverAddr net.Addr) {
			config := ClientConfig{
				DisableTransferSizeOption: true,
				DisableBlockSizeOption:    true,
				TracePackets:              true,
				Logger:                    log.New(&testLogWriter{t}, "", 0),
			}

			reader := bytes.NewReader(data[0:768])
			err := PutFile(ctx, serverAddr.String(), "xyzzy", "octet", &config, reader)
			if err != nil {
				t.Errorf("PutFile failed: %v", err)
				return
			}
		}, []testStep{
			{Receive: WriteRequest{Filename: "xyzzy", Mode: "octet"}},
			{Send: Ack{Block: 0}},
			{Receive: Data{Block: 1, Data: data[0:512]}},
			{Send: Ack{Block: 1}},
			{Receive: Data{Block: 2, Data: data[512:768]}},
			{Send: Ack{Block: 2}},
		})
	})

	t.Run("write with block-aligned file size", func(t *testing.T) {
		runClientTest(t, func(ctx context.Context, serverAddr net.Addr) {
			config := ClientConfig{
				DisableTransferSizeOption: true,
				DisableBlockSizeOption:    true,
				TracePackets:              true,
				Logger:                    log.New(&testLogWriter{t}, "", 0),
			}

			reader := bytes.NewReader(data[0:1024])
			err := PutFile(ctx, serverAddr.String(), "xyzzy", "octet", &config, reader)
			if err != nil {
				t.Errorf("PutFile failed: %v", err)
				return
			}
		}, []testStep{
			{Receive: WriteRequest{Filename: "xyzzy", Mode: "octet"}},
			{Send: Ack{Block: 0}},
			{Receive: Data{Block: 1, Data: data[0:512]}},
			{Send: Ack{Block: 1}},
			{Receive: Data{Block: 2, Data: data[512:1024]}},
			{Send: Ack{Block: 2}},
			{Receive: Data{Block: 3, Data: []byte{}}},
			{Send: Ack{Block: 3}},
		})
	})

	t.Run("write with tsize option", func(t *testing.T) {
		runClientTest(t, func(ctx context.Context, serverAddr net.Addr) {
			config := ClientConfig{
				DisableTransferSizeOption: false,
				DisableBlockSizeOption:    true,
				TracePackets:              true,
				Logger:                    log.New(&testLogWriter{t}, "", 0),
			}

			reader := bytes.NewReader(data[0:768])
			err := PutFile(ctx, serverAddr.String(), "xyzzy", "octet", &config, reader)
			if err != nil {
				t.Errorf("PutFile failed: %v", err)
				return
			}
		}, []testStep{
			{Receive: WriteRequest{Filename: "xyzzy", Mode: "octet", Options: map[string]string{"tsize": "768"}}},
			{Send: Ack{Block: 0}},
			{Receive: Data{Block: 1, Data: data[0:512]}},
			{Send: Ack{Block: 1}},
			{Receive: Data{Block: 2, Data: data[512:768]}},
			{Send: Ack{Block: 2}},
		})
	})

	t.Run("write with blksize option - non-block-size aligned", func(t *testing.T) {
		runClientTest(t, func(ctx context.Context, serverAddr net.Addr) {
			config := ClientConfig{
				DisableTransferSizeOption: true,
				DisableBlockSizeOption:    false,
				TracePackets:              true,
				MaxBlockSize:              768,
				Logger:                    log.New(&testLogWriter{t}, "", 0),
			}

			reader := bytes.NewReader(data[0:1024])
			err := PutFile(ctx, serverAddr.String(), "xyzzy", "octet", &config, reader)
			if err != nil {
				t.Errorf("PutFile failed: %v", err)
				return
			}
		}, []testStep{
			{Receive: WriteRequest{Filename: "xyzzy", Mode: "octet", Options: map[string]string{"blksize": "768"}}},
			{Send: OptionsAck{Options: map[string]string{"blksize": "768"}}},
			{Receive: Data{Block: 1, Data: data[0:768]}},
			{Send: Ack{Block: 1}},
			{Receive: Data{Block: 2, Data: data[768:1024]}},
			{Send: Ack{Block: 2}},
		})
	})

	t.Run("write with blksize option - block-size aligned", func(t *testing.T) {
		runClientTest(t, func(ctx context.Context, serverAddr net.Addr) {
			config := ClientConfig{
				DisableTransferSizeOption: true,
				DisableBlockSizeOption:    false,
				TracePackets:              true,
				MaxBlockSize:              768,
				Logger:                    log.New(&testLogWriter{t}, "", 0),
			}

			reader := bytes.NewReader(data[0:1536])
			err := PutFile(ctx, serverAddr.String(), "xyzzy", "octet", &config, reader)
			if err != nil {
				t.Errorf("PutFile failed: %v", err)
				return
			}
		}, []testStep{
			{Receive: WriteRequest{Filename: "xyzzy", Mode: "octet", Options: map[string]string{"blksize": "768"}}},
			{Send: OptionsAck{Options: map[string]string{"blksize": "768"}}},
			{Receive: Data{Block: 1, Data: data[0:768]}},
			{Send: Ack{Block: 1}},
			{Receive: Data{Block: 2, Data: data[768:1536]}},
			{Send: Ack{Block: 2}},
			{Receive: Data{Block: 3, Data: []byte{}}},
			{Send: Ack{Block: 3}},
		})
	})

	t.Run("write with blksize option modified by server", func(t *testing.T) {
		runClientTest(t, func(ctx context.Context, serverAddr net.Addr) {
			config := ClientConfig{
				DisableTransferSizeOption: true,
				DisableBlockSizeOption:    false,
				TracePackets:              true,
				MaxBlockSize:              768,
				Logger:                    log.New(&testLogWriter{t}, "", 0),
			}

			reader := bytes.NewReader(data[0:1024])
			err := PutFile(ctx, serverAddr.String(), "xyzzy", "octet", &config, reader)
			if err != nil {
				t.Errorf("PutFile failed: %v", err)
				return
			}
		}, []testStep{
			{Receive: WriteRequest{Filename: "xyzzy", Mode: "octet", Options: map[string]string{"blksize": "768"}}},
			{Send: OptionsAck{Options: map[string]string{"blksize": "384"}}},
			{Receive: Data{Block: 1, Data: data[0:384]}},
			{Send: Ack{Block: 1}},
			{Receive: Data{Block: 2, Data: data[384:768]}},
			{Send: Ack{Block: 2}},
			{Receive: Data{Block: 3, Data: data[768:1024]}},
			{Send: Ack{Block: 3}},
		})
	})
}
