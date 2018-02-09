package io

import gio "io"

// AsyncReader provides a reader that you can send bytes to progressively.
type AsyncReader struct {
	input  chan []byte
	buffer []byte
	eof    bool
}

// NewAsyncReader makes a new AsyncReader.
func NewAsyncReader() *AsyncReader {
	return &AsyncReader{make(chan []byte), []byte{}, false}
}

// Read some octets into p.
func (nr *AsyncReader) Read(p []byte) (int, error) {
	if nr.eof {
		return 0, gio.EOF
	}
	if len(nr.buffer) == 0 {
		nr.buffer = <-nr.input
	}
	if len(nr.buffer) == 0 {
		nr.eof = true
		return 0, gio.EOF
	}
	count := copy(p, nr.buffer)
	nr.buffer = nr.buffer[count:]
	p = p[count:]
	return count, nil
}

// Send the reader some octets.
func (nr *AsyncReader) Send(p []byte) {
	if len(p) == 0 {
		panic("Can't send a zero-length slice")
	}
	nr.input <- p
}

// Close the reader.
func (nr *AsyncReader) Close() {
	nr.input <- []byte{}
}
