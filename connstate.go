package tftp

import (
	"context"
	"fmt"
	"log"
	"net"
	"time"
)

type connectionState struct {
	ctx            context.Context
	logger         *log.Logger
	conn           *PacketChan
	mainRemoteAddr net.Addr
	remoteAddr     net.Addr
	blockSize      uint16
	timeout        int
	maxRetries     int
	tracePackets   bool
}

type packetMethods interface {
	fmt.Stringer
	ToBytes() []byte
}

type timeoutError int

var errTimedOut = timeoutError(0)

func (e timeoutError) Error() string   { return "timed out" }
func (e timeoutError) Timeout() bool   { return true }
func (e timeoutError) Temporary() bool { return true }

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

func (state *connectionState) log(format string, v ...interface{}) {
	msg := fmt.Sprintf("%s: %s", state.addrsString(), fmt.Sprintf(format, v...))
	state.logger.Println(msg)
}

func (state *connectionState) send(packet packetMethods) {
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

	sent := make(chan error)
	state.conn.Outgoing <- Packet{Data: packet.ToBytes(), Addr: remoteAddr, Sent: sent}
	<-sent
}

func (state *connectionState) receive() (interface{}, error) {
receiveLoop:
	for {
		timeout := time.After(time.Duration(state.timeout) * time.Second)

		select {
		case packet := <-state.conn.Incoming:
			// Ensure that this packet's remote address matches the expected address if we know the address
			// in use by the remote peer.
			if state.remoteAddr != nil {
				if state.remoteAddr.String() != packet.Addr.String() {
					// RFC 1350: "If a source TID does not match, the packet should be discarded as erroneously sent from
					// somewhere else.  An error packet should be sent to the source of the incorrect packet, while not
					// disturbing the transfer."
					errorPacket := Error{Code: ERR_UNKNOWN_TRANSFER_ID, Message: "Unknown transfer ID"}
					state.conn.Outgoing <- Packet{errorPacket.ToBytes(), packet.Addr, nil}
					continue receiveLoop
				}
			} else {
				// Record the peer's address as the expected address if and only if there is a separate
				// "main" remote address in use and this packet's address differs from that main address.
				if state.mainRemoteAddr != nil && packet.Addr.String() != state.mainRemoteAddr.String() {
					state.remoteAddr = packet.Addr
				}
			}

			tftpPacket, err := PacketFromBytes(packet.Data)
			if err != nil {
				return nil, err
			}

			if state.tracePackets {
				state.log("received %s", tftpPacket.(packetMethods).String())
			}

			return tftpPacket, nil

		case <-timeout:
			return nil, &errTimedOut

		case <-state.ctx.Done():
			return nil, state.ctx.Err()
		}
	}
}

type packetDispositionFunc func(packet interface{}) (bool, error)

func (state *connectionState) sendAndReceiveNext(
	packetToSend packetMethods,
	packetDisposition packetDispositionFunc) (interface{}, error) {

	if state.tracePackets {
		state.log("sending %s", packetToSend.String())
	}

	bytesToSend := packetToSend.ToBytes()
	tries := 0

sendLoop:
	for tries < state.maxRetries {
		tries += 1

		// Determine the remote address of the client.
		remoteAddr := state.remoteAddr
		if remoteAddr == nil {
			remoteAddr = state.mainRemoteAddr
			if remoteAddr == nil {
				panic("No address was set in TFTP connection state.")
			}
		}

		// Send the packet to the client.
		sent := make(chan error)
		state.conn.Outgoing <- Packet{Data: bytesToSend, Addr: remoteAddr, Sent: sent}
		<-sent

		timeout := time.After(time.Duration(state.timeout) * time.Second)

	receiveLoop:
		select {
		case packet := <-state.conn.Incoming:
			// Ensure that this packet's remote address matches the expected address if we know the address
			// in use by the remote peer.
			if state.remoteAddr != nil {
				if state.remoteAddr.String() != packet.Addr.String() {
					// RFC 1350: "If a source TID does not match, the packet should be discarded as erroneously sent from
					// somewhere else.  An error packet should be sent to the source of the incorrect packet, while not
					// disturbing the transfer."
					errorPacket := Error{Code: ERR_UNKNOWN_TRANSFER_ID, Message: "Unknown transfer ID"}
					state.conn.Outgoing <- Packet{errorPacket.ToBytes(), packet.Addr, nil}
					goto receiveLoop
				}
			} else {
				// Record the peer's address as the expected address if and only if there is a separate
				// "main" remote address in use and this packet's address differs from that main address.
				if state.mainRemoteAddr != nil && packet.Addr.String() != state.mainRemoteAddr.String() {
					state.remoteAddr = packet.Addr
				}
			}

			tftpPacket, err := PacketFromBytes(packet.Data)
			if err != nil {
				errorPacket := Error{Code: ERR_NOT_DEFINED, Message: "Malformed packet"}
				state.conn.Outgoing <- Packet{errorPacket.ToBytes(), state.remoteAddr, nil}
				return nil, err
			}

			if state.tracePackets {
				state.log("received %s", tftpPacket.(packetMethods).String())
			}

			if errorPacket, ok := tftpPacket.(Error); ok {
				state.log("Remote sent error: %v", errorPacket)
				return nil, errorPacket
			}

			returnPacket, err := packetDisposition(tftpPacket.(packetMethods))

			if err == nil {
				if returnPacket {
					return tftpPacket, nil
				} else {
					goto receiveLoop
				}
			} else {
				return nil, err
			}

		case <-timeout:
			continue sendLoop

		case <-state.ctx.Done():
			errorPacket := Error{Code: ERR_NOT_DEFINED, Message: "Shutdown."}
			state.conn.Outgoing <- Packet{errorPacket.ToBytes(), state.remoteAddr, nil}
			return nil, state.ctx.Err()
		}
	}

	// If we reach here, then peer failed to respond to retransmission.
	return nil, &errTimedOut
}
