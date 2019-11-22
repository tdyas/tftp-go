package tftp

import (
	"bytes"
	"io"
	"os"
	"strings"
)

func SizeOfReader(reader io.Reader) int64 {
	switch rdr := reader.(type) {
	case *strings.Reader:
		return rdr.Size()

	case *bytes.Reader:
		return rdr.Size()

	case *io.LimitedReader:
		return rdr.N

	case *io.SectionReader:
		return rdr.Size()

	case *os.File:
		stat, err := rdr.Stat()
		if err != nil {
			return -1
		}
		return stat.Size()

	default:
		return -1
	}
}
