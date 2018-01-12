package tftp

import (
	"net"
	"sync/atomic"
)

// This file wraps a net.PacketConn with channels.

type Packet struct {
	Data []byte
	Addr net.Addr
}

type PacketChan struct {
	Incoming <-chan Packet
	Outgoing chan<- Packet
	conn     net.PacketConn
	err      error
	closed   int32
}

func receiveLoop(conn net.PacketConn, packets chan<- Packet, closed *int32) {
	for {
		buffer := make([]byte, 65535)

		n, remoteAddr, err := conn.ReadFrom(buffer)
		if err != nil {
			if atomic.LoadInt32(closed) != 0 {
				break
			}
		}

		packets <- Packet{Data: buffer[0:n], Addr: remoteAddr}
	}
}

func sendLoop(conn net.PacketConn, packets <-chan Packet, closed *int32) {
	for {
		packet := <-packets

		_, err := conn.WriteTo(packet.Data, packet.Addr)
		if err != nil {
			if atomic.LoadInt32(closed) != 0 {
				break
			}
		}
	}
}

func NewPacketChan(conn net.PacketConn, incomingSize int, outgoingSize int) (*PacketChan, error) {
	incoming := make(chan Packet, incomingSize)
	outgoing := make(chan Packet, outgoingSize)

	pchan := PacketChan{
		Incoming: incoming,
		Outgoing: outgoing,
		conn:     conn,
	}

	go receiveLoop(conn, incoming, &pchan.closed)
	go sendLoop(conn, outgoing, &pchan.closed)

	return &pchan, nil
}

func (self *PacketChan) LocalAddr() net.Addr {
	return self.conn.LocalAddr()
}

func (self *PacketChan) Close() error {
	atomic.StoreInt32(&self.closed, 1)
	return self.conn.Close()
}
