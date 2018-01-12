package tftp

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"testing"
	//"time"
)

func dummyServerLoop(conn1 net.PacketConn, conn2 net.PacketConn, c chan error, steps []testStep) {
	var clientAddr net.Addr
	gotFirstPacket := false

	buffer := make([]byte, 65535)

	for _, step := range steps {
		if step.Send != nil {
			if clientAddr == nil {
				c <- errors.New("send configured before first receive")
				return
			}

			_, err := conn2.WriteTo(step.Send.ToBytes(), clientAddr)
			if err != nil {
				c <- err
				return
			}
		} else if step.Receive != nil {
			conn := conn1
			if gotFirstPacket {
				conn = conn2
			}

			//conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			n, remoteAddr, err := conn.ReadFrom(buffer)
			if err != nil {
				fmt.Printf("ReadFrom: %v", err)
			}

			clientAddr = remoteAddr
			gotFirstPacket = true

			expectedBytes := step.Receive.ToBytes()
			actualBytes := buffer[0:n]

			packet, err := PacketFromBytes(actualBytes)
			if err != nil {
				c <- errors.New(fmt.Sprintf("Unable to decode packet: %v", err))
				return
			}

			if !bytes.Equal(expectedBytes, actualBytes) {
				c <- errors.New(fmt.Sprintf("packet mismatch: expected=[%s], actual=[%s]",
					step.Receive, packet))
				return
			}
		}
	}

	c <- nil
}

func runClientTest(t *testing.T, testName string, runClient func(net.Addr), steps []testStep) {
	conn1, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Errorf("Unable to open socket: %v", err)
		return
	}
	defer conn1.Close()

	conn2, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Errorf("Unable to open socket: %v", err)
		return
	}
	defer conn2.Close()

	c := make(chan error)
	go dummyServerLoop(conn1, conn2, c, steps)

	runClient(conn1.LocalAddr())

	err = <-c
	if err != nil {
		t.Errorf("Test '%s': %v", testName, err)
	}
}

func TestGetFile(t *testing.T) {
	data := make([]byte, 3*512)
	_, err := rand.Read(data)
	if err != nil {
		t.Errorf("Unable to fill buffer: %v", err)
		return
	}

	runClientTest(t, "basic read request", func(serverAddr net.Addr) {
		var buffer bytes.Buffer
		config := ClientConfig{TracePackets: true, DisableOptions: true}

		err := GetFile(serverAddr.String(), "xyzzy", "octet", &config, &buffer)
		if err != nil {
			t.Errorf("Test 'basic read request' failed: %v", err)
			return
		}
		if buffer.Len() != 768 {
			t.Error("Test 'basic read request': Length does not match.")
			return
		}
		if !bytes.Equal(data[0:768], buffer.Bytes()) {
			t.Error("Test 'basic read request' failed: bytes read do not match.")
			return
		}
	}, []testStep{
		{Receive: ReadRequest{Filename: "xyzzy", Mode: "octet"}},
		{Send: Data{Block: 1, Data: data[0:512]}},
		{Receive: Ack{Block: 1}},
		{Send: Data{Block: 2, Data: data[512:768]}},
		{Receive: Ack{Block: 2}},
	})

	runClientTest(t, "block aligned read request", func(serverAddr net.Addr) {
		var buffer bytes.Buffer
		config := ClientConfig{TracePackets: true, DisableOptions: true}

		err := GetFile(serverAddr.String(), "xyzzy", "octet", &config, &buffer)
		if err != nil {
			t.Errorf("Test 'basic read request' failed: %v", err)
			return
		}
		if buffer.Len() != 1024 {
			t.Error("Test 'block aligned read request': Length does not match.")
			return
		}
		if !bytes.Equal(data[0:1024], buffer.Bytes()) {
			t.Error("Test 'block aligned read request' failed: bytes read do not match.")
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
}
