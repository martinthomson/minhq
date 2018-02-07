package hc

import (
	"errors"
	"io"
)

// ErrTableUpdateInHeaderBlock shouldn't exist, but this is an early version of QCRAM.
var ErrTableUpdateInHeaderBlock = errors.New("header table update in header block")

// ErrHeaderInTableUpdate shouldn't exist, but this is an early version of QCRAM.
var ErrHeaderInTableUpdate = errors.New("header emission in table update")

// ErrBlockingTableUpdate is where a table update depends on changes that
// haven't happened yet, which is nonsensical.
var ErrBlockingTableUpdate = errors.New("blocking required for table update")

func setCapacity(table *Table, c TableCapacity) {
	if table.Base() > 0 {
		panic("SetCapacity called when table isn't empty")
	}
	table.SetCapacity(c)
}

// QcramDecoder is the top-level class for header decompression.
type QcramDecoder struct {
	decoderCommon
	inserts chan int
}

// SetCapacity sets the capacity of the table. This can't be set once the table
// has been used.
func (decoder *QcramDecoder) SetCapacity(c TableCapacity) {
	setCapacity(&decoder.Table, c)
}

func (decoder *QcramDecoder) readIncremental(reader *Reader, base int) error {
	name, value, err := decoder.readNameValue(reader, 6, base)
	if err != nil {
		return err
	}
	decoder.Table.Insert(name, value)
	return nil
}

func (decoder *QcramDecoder) readDuplicate(reader *Reader, base int) error {
	index, err := reader.ReadIndex(5)
	if err != nil {
		return err
	}
	entry := decoder.Table.GetWithBase(index, base)
	if entry == nil {
		return ErrIndexError
	}
	decoder.Table.Insert(entry.Name(), entry.Value())
	return nil
}

// ReadTableChanges reads inserts to the table.
func (decoder *QcramDecoder) ReadTableChanges(r io.Reader) error {
	reader := NewReader(r)

	base := decoder.Table.Base()
	for {
		b, err := reader.ReadBits(2)
		if err == io.EOF {
			break // Success
		}
		if err != nil {
			return err
		}

		if b > 1 {
			return ErrHeaderInTableUpdate
		}
		if b == 1 {
			err = decoder.readIncremental(reader, base)
			if err != nil {
				return err
			}
			continue
		}
		b, err = reader.ReadBits(1)
		if err != nil {
			return err
		}
		if b != 1 {
			return ErrHeaderInTableUpdate
		}
		err = decoder.readDuplicate(reader, base)
		if err != nil {
			return err
		}
	}
	decoder.inserts <- decoder.Table.Base()
	return nil
}

func (decoder *QcramDecoder) readIndexed(reader *Reader, base int) (*HeaderField, error) {
	index, err := reader.ReadIndex(7)
	if err != nil {
		return nil, err
	}
	entry := decoder.Table.GetWithBase(index, base)
	if entry == nil {
		return nil, ErrIndexError
	}
	return &HeaderField{entry.Name(), entry.Value(), false}, nil
}

func (decoder *QcramDecoder) readLiteral(reader *Reader, base int) (*HeaderField, error) {
	neverIndex, err := reader.ReadBits(3)
	if err != nil {
		return nil, err
	}
	if neverIndex > 1 {
		return nil, ErrTableUpdateInHeaderBlock
	}

	name, value, err := decoder.readNameValue(reader, 4, base)
	if err != nil {
		return nil, err
	}
	return &HeaderField{name, value, neverIndex == 1}, nil
}

func (decoder *QcramDecoder) readBase(reader *Reader) (int, error) {
	base, err := reader.ReadIndex(8)
	if err != nil {
		return 0, err
	}

	for decoder.Table.Base() < base {
		<-decoder.inserts
	}
	return base, nil
}

// ReadHeaderBlock decodes header fields as they arrive.
func (decoder *QcramDecoder) ReadHeaderBlock(r io.Reader) ([]HeaderField, error) {
	reader := NewReader(r)
	base, err := decoder.readBase(reader)
	if err != nil {
		return nil, err
	}

	headers := []HeaderField{}
	for {
		b, err := reader.ReadBits(1)
		if err == io.EOF {
			break // Success!
		}
		if err != nil {
			return nil, err
		}

		if b == 1 {
			h, err := decoder.readIndexed(reader, base)
			if err != nil {
				return nil, err
			}
			headers = append(headers, *h)
			continue
		}

		h, err := decoder.readLiteral(reader, base)
		if err != nil {
			return nil, err
		}
		headers = append(headers, *h)
	}

	// Sanity-check header ordering.
	pseudo := true
	for _, h := range headers {
		if h.Name[0] == ':' {
			if !pseudo {
				return nil, ErrPseudoHeaderOrdering
			}
		} else {
			pseudo = false
		}
	}
	return headers, nil
}

// This is used by the writer to track which table entries are needed to write
// out a particular header field.
type qcramWriterState struct {
	headers     []HeaderField
	matches     []Entry
	nameMatches []Entry
	largestBase int
}

func (state *qcramWriterState) init(headers []HeaderField) {
	state.headers = headers
	state.matches = make([]Entry, len(headers))
	state.nameMatches = make([]Entry, len(headers))
}

func (state *qcramWriterState) updateLargestBase(e Entry) {
	if e == nil {
		return
	}
	dyn, ok := e.(dynamicEntry)
	if ok && dyn.Base() > state.largestBase {
		state.largestBase = dyn.Base()
	}
}

// QcramEncoder is the top-level class for header compression.
type QcramEncoder struct {
	encoderCommon
}

// SetCapacity sets the capacity of the table. This can't be set once the table
// has been used.
func (encoder *QcramEncoder) SetCapacity(c TableCapacity) {
	setCapacity(&encoder.Table, c)
}

// writeIncremental writes the entry at state.xxx[i] to the control stream.
func (encoder *QcramEncoder) writeIncremental(writer *Writer, state *qcramWriterState, i int, base int) error {
	h := state.headers[i]
	entry := encoder.Table.Insert(h.Name, h.Value)
	if entry == nil {
		// Leaving h unmodified causes a literal to be written.
		return nil
	}

	err := writer.WriteBits(1, 2)
	if err != nil {
		return err
	}

	err = encoder.writeNameValue(writer, h, state.nameMatches[i], 6, base)
	if err != nil {
		return err
	}
	state.matches[i] = entry
	return nil
}

// writeTableChanges writes out the changes to the header table. It returns the
// largest value of base that can be used for this to work.
func (encoder *QcramEncoder) writeTableChanges(controlWriter io.Writer, state *qcramWriterState) error {
	w := NewWriter(controlWriter)

	base := encoder.Table.Base()

	for i := range state.headers {
		// Make sure to write into the slice rather than use a copy of each header.
		h := state.headers[i]
		if h.Sensitive {
			continue
		}
		m, nm := encoder.Table.Lookup(h.Name, h.Value)
		// TODO decide what needs duplicating
		// Probably decide based on a threshold basis
		if m != nil {
			state.matches[i] = m
			state.updateLargestBase(m)
		} else {
			state.nameMatches[i] = nm
			if encoder.shouldIndex(h) {
				err := encoder.writeIncremental(w, state, i, base)
				if err != nil {
					return err
				}
				state.largestBase = encoder.Table.Base()
			} else {
				state.updateLargestBase(nm)
			}
		}

	}
	return nil
}

func (encoder *QcramEncoder) writeIndexed(writer *Writer, state *qcramWriterState, i int) error {
	err := writer.WriteBit(1)
	if err != nil {
		return err
	}
	return writer.WriteInt(uint64(state.matches[i].Index(state.largestBase)), 7)
}

func (encoder QcramEncoder) writeLiteral(writer *Writer, state *qcramWriterState, i int) error {
	h := state.headers[i]
	var code uint64
	if h.Sensitive {
		code = 1
	}
	err := writer.WriteBits(code, 4)
	if err != nil {
		return err
	}

	return encoder.writeNameValue(writer, h, state.nameMatches[i], 4, state.largestBase)
}

func (encoder *QcramEncoder) writeHeaderBlock(headerWriter io.Writer, state *qcramWriterState) error {
	w := NewWriter(headerWriter)
	err := w.WriteInt(uint64(state.largestBase), 8)
	if err != nil {
		return err
	}

	for i := range state.headers {
		var err error
		if state.matches[i] != nil {
			err = encoder.writeIndexed(w, state, i)
		} else {
			err = encoder.writeLiteral(w, state, i)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func validatePseudoHeaders(headers []HeaderField) error {
	pseudo := true
	for _, h := range headers {
		if h.Name[0] == ':' {
			if !pseudo {
				return ErrPseudoHeaderOrdering
			}
		} else {
			pseudo = false
		}
	}
	return nil
}

// WriteHeaderBlock writes out a header block.  controlWriter is the control stream writer
func (encoder *QcramEncoder) WriteHeaderBlock(controlWriter io.Writer, headerWriter io.Writer, headers ...HeaderField) error {
	err := validatePseudoHeaders(headers)
	if err != nil {
		return err
	}

	var state qcramWriterState
	state.init(headers)
	err = encoder.writeTableChanges(controlWriter, &state)
	if err != nil {
		return err
	}

	return encoder.writeHeaderBlock(headerWriter, &state)
}
