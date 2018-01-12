package tftp

import (
	"bytes"
	"net"
	"testing"
	"time"
)

func addrEqual(left net.Addr, right net.Addr) bool {
	return left.Network() == right.Network() && left.String() == right.String()
}

func TestPacketChan(t *testing.T) {
	conn1, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Errorf("net.ListenPacket failed: %v", err)
		return
	}

	pchan1, err := NewPacketChan(conn1, 0, 0)
	if err != nil {
		t.Errorf("NewPacketChain failed: %v", err)
		return
	}
	defer pchan1.Close()

	conn2, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Errorf("net.ListenPacket failed: %v", err)
		return
	}

	pchan2, err := NewPacketChan(conn2, 0, 0)
	if err != nil {
		t.Errorf("NewPacketChain failed: %v", err)
		return
	}
	defer pchan2.Close()

	data := []byte{0, 1, 2, 3}

	pchan1.Outgoing <- Packet{Data: data, Addr: conn2.LocalAddr()}
	timeout := time.After(10 * time.Millisecond)

	select {
	case packet := <-pchan2.Incoming:
		if !addrEqual(packet.Addr, pchan1.LocalAddr()) {
			t.Error("packet addresses mismatch")
			return
		}
		if !bytes.Equal(packet.Data, data) {
			t.Error("bytes mismatch")
			return
		}

	case <-timeout:
		t.Error("timed out waiting for packet")
	}
}
