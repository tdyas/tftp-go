package tftp

import (
	"github.com/stretchr/testify/assert"
	"net"
	"testing"
	"time"
)

func TestPacketChan(t *testing.T) {
	conn1, err := net.ListenPacket("udp", "127.0.0.1:0")
	assert.NoError(t, err)

	pchan1, err := NewPacketChan(conn1, 0, 0)
	assert.NoError(t, err)
	defer pchan1.Close()

	conn2, err := net.ListenPacket("udp", "127.0.0.1:0")
	assert.NoError(t, err)

	pchan2, err := NewPacketChan(conn2, 0, 0)
	assert.NoError(t, err)
	defer pchan2.Close()

	data := []byte{0, 1, 2, 3}

	pchan1.Outgoing <- Packet{Data: data, Addr: conn2.LocalAddr()}
	timeout := time.After(10 * time.Millisecond)

	select {
	case packet := <-pchan2.Incoming:
		assert.Equal(t, packet.Addr, pchan1.LocalAddr())
		assert.Equal(t, packet.Data, data)

	case <-timeout:
		assert.Fail(t, "timed out waiting for packet")
	}
}
