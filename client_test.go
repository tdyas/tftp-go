package tftp

import (
	"bytes"
	"context"
	"math/rand"
	"net"
	"testing"
	"time"
	"fmt"
)

func dummyServerLoop(ctx context.Context, t *testing.T, conn1 *PacketChan, conn2 *PacketChan, steps []testStep) {
	var clientAddr net.Addr
	gotFirstPacket := false

	for _, step := range steps {
		fmt.Printf("step: %v\n", step)
		if step.Send != nil {
			if clientAddr == nil {
				t.Error("send configured before first receive")
				return
			}
			conn2.Outgoing <- Packet{step.Send.ToBytes(), clientAddr}
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
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(d))
	defer cancel()

	conn1, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Errorf("Unable to open socket: %v", err)
		return
	}

	pchan1, err := NewPacketChan(conn1, 1, 1)
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

	pchan2, err := NewPacketChan(conn2, 1, 1)
	if err != nil {
		t.Errorf("Unable to open socket: %v", err)
		return
	}
	defer pchan2.Close()

	join := make(chan bool)
	go func() {
		dummyServerLoop(ctx, t, pchan1, pchan2, steps)
		cancel()
		join <- true
	}()

	f(ctx, conn1.LocalAddr())

	<-join
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
			config := ClientConfig{TracePackets: true, DisableOptions: true}

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

	t.Run("block aligned read request", func(t *testing.T) {
		runClientTest(t, func(ctx context.Context, serverAddr net.Addr) {
			var buffer bytes.Buffer
			config := ClientConfig{TracePackets: true, DisableOptions: true}

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
}
