package tftp

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

type connectionState struct {
	buffer         []byte
	loggerLock     sync.Mutex
	logger         *log.Logger
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

func (state *connectionState) addrsString() string {
	r := state.conn.LocalAddr().String()
	r += "<->"

	if state.mainRemoteAddr != nil && state.remoteAddr != nil {
		r += fmt.Sprintf("(%s,%s)", state.mainRemoteAddr.String(), state.remoteAddr.String())
	} else if state.mainRemoteAddr != nil {
		r += state.mainRemoteAddr.String()
	} else if state.remoteAddr != nil {
		r += state.remoteAddr.String()
	} else {
		r += "???"
	}

	return r
}

func (state *connectionState) log(format string, v ...interface{}) string {
	state.loggerLock.Lock()
	defer state.loggerLock.Unlock()
	return fmt.Sprintf("%s: %s", state.addrsString(), fmt.Sprintf(format, v))
}

func (state *connectionState) send(packet packetMethods) (n int, err error) {
	if state.tracePackets {
		state.log("sending %s", packet.String())
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

	// Ensure that the sender of this packet matches the expected address.
	// RFC 1350: "If a source TID does not match, the packet should be discarded as erroneously sent from
	// somewhere else.  An error packet should be sent to the source of the incorrect packet, while not
	// disturbing the transfer."
	state.log("remoteAddr=%v, state.remoteAddr=%v, state.mainRemoteAddr",
		remoteAddr, state.remoteAddr, state.mainRemoteAddr)
	if state.remoteAddr != nil {
		if state.remoteAddr.String() != remoteAddr.String() {
			errorPacket := Error{Code: ERR_UNKNOWN_TRANSFER_ID, Message: "Unknown transfer ID"}
			state.conn.WriteTo(errorPacket.ToBytes(), remoteAddr)
			return state.receive()
		}
	} else {
		if state.mainRemoteAddr != nil && remoteAddr.String() != state.mainRemoteAddr.String() {
			state.remoteAddr = remoteAddr
			state.log("remote address: %v", remoteAddr)
		}
	}

	packet, err := PacketFromBytes(state.buffer[0:n])
	if err != nil {
		return nil, err
	}

	if state.tracePackets {
		state.log("received %s", packet.(packetMethods).String())
	}

	return packet, nil
}
