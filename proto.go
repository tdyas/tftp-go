package tftp

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	ERR_NOT_DEFINED         = 0 // Not defined, see error message (if any).
	ERR_FILE_NOT_FOUND      = 1 // File not found.
	ERR_ACCESS_VIOLATION    = 2 // Access violation.
	ERR_DISK_FULL           = 3 // Disk full or allocation exceeded.
	ERR_ILLEGAL_OPERATION   = 4 // Illegal TFTP operation.
	ERR_UNKNOWN_TRANSFER_ID = 5 // Unknown transfer ID.
	ERR_FILE_EXISTS         = 6 // File already exists.
	ERR_NO_USER             = 7 // No such user.
	ERR_INVALID_OPTIONS     = 8
)

const (
	DEFAULT_BLOCKSIZE = 512
	MIN_BLOCK_SIZE    = 8
	MAX_BLOCK_SIZE    = 65464
)

type ReadRequest struct {
	Filename string
	Mode     string
	Options  map[string]string
}

type WriteRequest struct {
	Filename string
	Mode     string
	Options  map[string]string
}

type Data struct {
	Block uint16
	Data  []byte
}

type Ack struct {
	Block uint16
}

type Error struct {
	Code    uint16
	Message string
}

type OptionsAck struct {
	Options map[string]string
}

var MalformedPacketError = errors.New("Malformed TFTP packet")

func stringsToMap(strs [][]byte) map[string]string {
	result := make(map[string]string)
	i := 0
	for i < len(strs) {
		result[string(strs[i])] = string(strs[i+1])
		i = i + 2
	}
	return result
}

func PacketFromBytes(buffer []byte) (interface{}, error) {
	if len(buffer) < 2 {
		return nil, MalformedPacketError
	}

	code := binary.BigEndian.Uint16(buffer[0:2])

	switch code {
	case 1:
		strs := bytes.Split(buffer[2:], []byte{0})
		if len(strs[len(strs)-1]) != 0 {
			return nil, MalformedPacketError
		}
		strs = strs[0 : len(strs)-1]
		if len(strs) < 2 {
			return nil, MalformedPacketError
		}
		if (len(strs) % 2) != 0 {
			return nil, MalformedPacketError
		}

		var options map[string]string = nil
		if len(strs) > 2 {
			options = stringsToMap(strs[2:])
		}

		rrq := ReadRequest{
			Filename: string(strs[0]),
			Mode:     string(strs[1]),
			Options:  options,
		}
		return rrq, nil

	case 2:
		strs := bytes.Split(buffer[2:], []byte{0})
		if len(strs[len(strs)-1]) != 0 {
			return nil, MalformedPacketError
		}
		strs = strs[0 : len(strs)-1]
		if len(strs) < 2 {
			return nil, MalformedPacketError
		}
		if (len(strs) % 2) != 0 {
			return nil, MalformedPacketError
		}

		var options map[string]string = nil
		if len(strs) > 2 {
			options = stringsToMap(strs[2:])
		}

		wrq := WriteRequest{
			Filename: string(strs[0]),
			Mode:     string(strs[1]),
			Options:  options,
		}
		return wrq, nil

	case 3:
		if len(buffer) < 4 {
			return nil, MalformedPacketError
		}
		data := Data{
			Block: binary.BigEndian.Uint16(buffer[2:4]),
			Data:  buffer[4:],
		}
		return data, nil

	case 4:
		if len(buffer) < 4 {
			return nil, MalformedPacketError
		}
		ack := Ack{
			Block: binary.BigEndian.Uint16(buffer[2:4]),
		}
		return ack, nil

	case 5:
		if len(buffer) < 4 {
			return nil, MalformedPacketError
		}
		errorPacket := Error{
			Code:    binary.BigEndian.Uint16(buffer[2:4]),
			Message: string(buffer[4:]),
		}
		return errorPacket, nil

	case 6:
		strs := bytes.Split(buffer[2:], []byte{0})
		if len(strs[len(strs)-1]) != 0 {
			return nil, MalformedPacketError
		}
		strs = strs[0 : len(strs)-1]
		if (len(strs) % 2) != 0 {
			return nil, MalformedPacketError
		}
		oack := OptionsAck{
			Options: stringsToMap(strs),
		}
		return oack, nil

	default:
		return nil, MalformedPacketError
	}
}

func (rrq ReadRequest) ToBytes() []byte {
	var buffer bytes.Buffer

	binary.Write(&buffer, binary.BigEndian, uint16(1))
	binary.Write(&buffer, binary.BigEndian, []byte(rrq.Filename))
	binary.Write(&buffer, binary.BigEndian, byte(0))
	binary.Write(&buffer, binary.BigEndian, []byte(rrq.Mode))
	binary.Write(&buffer, binary.BigEndian, byte(0))

	for key, value := range rrq.Options {
		binary.Write(&buffer, binary.BigEndian, []byte(key))
		binary.Write(&buffer, binary.BigEndian, byte(0))
		binary.Write(&buffer, binary.BigEndian, []byte(value))
		binary.Write(&buffer, binary.BigEndian, byte(0))
	}

	return buffer.Bytes()
}

func (rrq ReadRequest) String() string {
	var options = ""
	for key, value := range rrq.Options {
		options += fmt.Sprintf(", %v=%v", key, value)
	}
	return fmt.Sprintf("RRQ <file=%v, mode=%v%v>", rrq.Filename, rrq.Mode, options)
}

func (wrq WriteRequest) ToBytes() []byte {
	var buffer bytes.Buffer

	binary.Write(&buffer, binary.BigEndian, uint16(2))
	binary.Write(&buffer, binary.BigEndian, []byte(wrq.Filename))
	binary.Write(&buffer, binary.BigEndian, byte(0))
	binary.Write(&buffer, binary.BigEndian, []byte(wrq.Mode))
	binary.Write(&buffer, binary.BigEndian, byte(0))
	for key, value := range wrq.Options {
		binary.Write(&buffer, binary.BigEndian, []byte(key))
		binary.Write(&buffer, binary.BigEndian, byte(0))
		binary.Write(&buffer, binary.BigEndian, []byte(value))
		binary.Write(&buffer, binary.BigEndian, byte(0))
	}

	return buffer.Bytes()
}

func (wrq WriteRequest) String() string {
	var options = ""
	for key, value := range wrq.Options {
		options += fmt.Sprintf(", %v=%v", key, value)
	}
	return fmt.Sprintf("WRQ <file=%v, mode=%v%v>", wrq.Filename, wrq.Mode, options)
}

func (d Data) ToBytes() []byte {
	var buffer bytes.Buffer

	binary.Write(&buffer, binary.BigEndian, uint16(3))
	binary.Write(&buffer, binary.BigEndian, d.Block)
	binary.Write(&buffer, binary.BigEndian, d.Data)

	return buffer.Bytes()
}

func (d Data) String() string {
	return fmt.Sprintf("DATA <block=%v, %v bytes>", d.Block, len(d.Data))
}

func (ack Ack) ToBytes() []byte {
	var buffer bytes.Buffer

	binary.Write(&buffer, binary.BigEndian, uint16(4))
	binary.Write(&buffer, binary.BigEndian, ack.Block)

	return buffer.Bytes()
}

func (ack Ack) String() string {
	return fmt.Sprintf("ACK <block=%v>", ack.Block)
}

func (e Error) ToBytes() []byte {
	var buffer bytes.Buffer

	binary.Write(&buffer, binary.BigEndian, uint16(5))
	binary.Write(&buffer, binary.BigEndian, e.Code)
	binary.Write(&buffer, binary.BigEndian, []byte(e.Message))
	binary.Write(&buffer, binary.BigEndian, byte(0))

	return buffer.Bytes()
}

func (e Error) String() string {
	return fmt.Sprintf("ERROR <code=%v, msg=%v>", e.Code, e.Message)
}

func (oack OptionsAck) ToBytes() []byte {
	var buffer bytes.Buffer

	binary.Write(&buffer, binary.BigEndian, uint16(6))

	for key, value := range oack.Options {
		binary.Write(&buffer, binary.BigEndian, []byte(key))
		binary.Write(&buffer, binary.BigEndian, byte(0))
		binary.Write(&buffer, binary.BigEndian, []byte(value))
		binary.Write(&buffer, binary.BigEndian, byte(0))
	}

	return buffer.Bytes()
}

func (oack OptionsAck) String() string {
	var options = ""
	first := true
	for key, value := range oack.Options {
		if !first {
			options += ", "
			first = false
		}
		options += fmt.Sprintf("%v=%v", key, value)
	}
	return fmt.Sprintf("OACK <%v>", options)
}

func (e Error) Error() string {
	return fmt.Sprintf("remote error: %v (%v)", e.Message, e.Code)
}

func (e Error) Timeout() bool   { return false }
func (e Error) Temporary() bool { return false }
