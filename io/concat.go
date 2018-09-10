package io

import (
	"io"
)

type concatMessage struct {
	r       io.Reader
	drained chan struct{}
}

// ConcatenatingReader takes a sequence of reads on one (or more)
// different threads and combines them into a single coherent reader.
// If you want ordering, make sure to add readers on the same thread.
type ConcatenatingReader struct {
	pending chan *concatMessage
	current *concatMessage
}

// NewConcatenatingReader allocates the internal channel.
func NewConcatenatingReader() *ConcatenatingReader {
	return &ConcatenatingReader{pending: make(chan *concatMessage)}
}

// Add adds a reader, then holds until it is fully drained.
func (cat *ConcatenatingReader) Add(r io.Reader) {
	message := &concatMessage{r, make(chan struct{})}
	cat.pending <- message
	<-message.drained
}

// Close the reader and cause the reader to receive an EOF.
func (cat *ConcatenatingReader) Close() error {
	close(cat.pending)
	return nil
}

func (cat *ConcatenatingReader) next() bool {
	if cat.current != nil {
		cat.current.drained <- struct{}{}
	}
	cat.current = <-cat.pending
	return cat.current != nil
}

// Read can be called from any thread, but only one thread.
func (cat *ConcatenatingReader) Read(p []byte) (int, error) {
	if cat.current == nil {
		if !cat.next() {
			return 0, io.EOF
		}
	}

	n, err := cat.current.r.Read(p)
	for err == io.EOF {
		if !cat.next() {
			return 0, io.EOF
		}
		n, err = cat.current.r.Read(p)
	}
	return n, err
}
