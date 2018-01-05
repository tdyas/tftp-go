package tftp

import (
	"fmt"
	"net"
	"time"
	"log"
)

type connectionState struct {
	buffer         []byte
	log            *log.Logger
	conn           net.PacketConn
	mainRemoteAddr net.Addr
	remoteAddr     net.Addr
	blockSize      uint16
	timeout        int
	tracePackets   bool
}

type packetMethods interface {
	fmt.Stringer
	ToBytes() []byte
}

func (state *connectionState) send(packet packetMethods) (n int, err error) {
	if state.tracePackets {
		state.log.Printf("sending %s", packet.String())
	}

	remoteAddr := state.remoteAddr
	if remoteAddr == nil {
		remoteAddr = state.mainRemoteAddr
		if remoteAddr == nil {
			panic("No address was set in TFTP connection state.")
		}
	}

	return state.conn.WriteTo(packet.ToBytes(), remoteAddr)
}

func (state *connectionState) receive() (interface{}, error) {
	state.conn.SetReadDeadline(time.Now().Add(time.Duration(state.timeout) * time.Second))
	n, remoteAddr, err := state.conn.ReadFrom(state.buffer)
	if err != nil {
		return nil, err
	}

	packet, err := PacketFromBytes(state.buffer[0:n])
	if err != nil {
		return nil, err
	}

	if state.remoteAddr == nil {
		state.remoteAddr = remoteAddr
		state.log.Printf("remote address: %v", remoteAddr)
	}

	if state.tracePackets {
		state.log.Printf("received %s", packet.(packetMethods).String())
	}

	return packet, nil
}
