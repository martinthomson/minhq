package hc

import (
	"errors"
	"io"
	"time"
)

// ErrTableOverflow is raised when an insert is too large for the table.
// Unlike HPACK, QPACK doesn't allow this.
var ErrTableOverflow = errors.New("inserting entry that is too large for the table")

type headerBlockAck struct {
	id               uint64
	largestReference int
}

// QpackDecoder is the top-level class for header decompression.
// This is intended to be concurrency-safe for reading of header blocks
// (ReadHeaderBlock), but the reading of table updates (ReadTableChanges) can
// only run on one thread at a time.
type QpackDecoder struct {
	decoderCommon
	table        *QpackDecoderTable
	acknowledged chan<- *headerBlockAck
	cancelled    chan<- uint64
	available    chan<- int
	ackDelay     time.Duration
}

// NewQpackDecoder makes and sets up a QpackDecoder.
func NewQpackDecoder(aw io.WriteCloser, capacity TableCapacity) *QpackDecoder {
	decoder := new(QpackDecoder)
	decoder.table = NewQpackDecoderTable(capacity)
	decoder.Table = decoder.table
	available := make(chan int)
	decoder.available = available
	acknowledged := make(chan *headerBlockAck)
	decoder.acknowledged = acknowledged
	cancelled := make(chan uint64)
	decoder.cancelled = cancelled
	decoder.initLogging(nil)
	go decoder.writeAcknowledgements(aw, available, acknowledged, cancelled)
	return decoder
}

func (decoder *QpackDecoder) writeAcknowledgements(aw io.WriteCloser, available <-chan int,
	acknowledged <-chan *headerBlockAck, cancelled <-chan uint64) {
	defer aw.Close()
	w := NewWriter(aw)

	// These values are used to track whether to send Table State Synchronize, which we do on a delayed timer.
	var largestAcknowledged int
	var syncLargest int
	tss := make(chan struct{})
	delayTss := true
	for {
		var v uint64
		var err error
		var remaining byte

		select {
		case ack := <-acknowledged:
			largestAcknowledged = ack.largestReference
			v = ack.id
			remaining = 7
			// Header Acknowledgment: instruction = b1
			err = w.WriteBit(1)
			decoder.logger.Printf("ack header block %v", v)

		case cancel := <-cancelled:
			v = cancel
			remaining = 6
			// Stream Cancellation: instruction = b01
			err = w.WriteBits(1, 2)
			decoder.logger.Printf("ack stream cancellation %v", v)

		case entry, ok := <-available:
			if !ok {
				decoder.logger.Printf("ack closing")
				return // The available channel closed.
			}
			if syncLargest < entry {
				syncLargest = entry
				if delayTss {
					decoder.logger.Printf("defer table state synchronize")
					delayTss = false
					go func() {
						<-time.After(decoder.ackDelay)
						tss <- struct{}{}
					}()
				}
			}
			continue

		case <-tss:
			// This is an incremental instruction, which might not need to be run.
			delayTss = true
			if syncLargest <= largestAcknowledged {
				continue
			}
			v = uint64(syncLargest - largestAcknowledged)
			largestAcknowledged = syncLargest
			remaining = 6
			// Table State Synchronize: instruction = b00
			err = w.WriteBits(0, 2)
			decoder.logger.Printf("table state synchronize %v", v)
		}
		if err != nil {
			// TODO: close the connection instead of just disappearing
			return
		}
		err = w.WriteInt(v, remaining)
		if err != nil {
			return
		}
	}
}

// SetAckDelay sets the delay for the Table State Synchronize instruction.
// This value should probably correspond to the ACK delay used in the transport.
func (decoder *QpackDecoder) SetAckDelay(delay time.Duration) {
	decoder.ackDelay = delay
}

func (decoder *QpackDecoder) readValueAndInsert(reader *Reader, name string) error {
	value, err := reader.ReadString(7)
	if err != nil {
		return err
	}
	if tableOverhead+TableCapacity(len(name)+len(value)) > decoder.Table.Capacity() {
		return ErrTableOverflow
	}
	decoder.logger.Printf("insert %v = %v", name, value)
	added := decoder.Table.Insert(name, value, nil)
	decoder.available <- added.Base()
	return nil
}

func (decoder *QpackDecoder) readInsertWithNameReference(reader *Reader, base int) error {
	static, err := reader.ReadBit()
	if err != nil {
		return err
	}
	nameIndex, err := reader.ReadIndex(6)
	if err != nil {
		return err
	}
	decoder.logger.Printf("insert w/ name ref (static=%v) %v", static == 1, nameIndex)
	var nameEntry Entry
	if static != 0 {
		nameEntry = decoder.table.GetStatic(nameIndex)
	} else {
		nameEntry = decoder.table.GetDynamic(nameIndex, base)
	}
	if nameEntry == nil {
		return ErrIndexError
	}
	return decoder.readValueAndInsert(reader, nameEntry.Name())
}

func (decoder *QpackDecoder) readInsertWithNameLiteral(reader *Reader, base int) error {
	name, err := reader.ReadString(5)
	if err != nil {
		return err
	}
	return decoder.readValueAndInsert(reader, name)
}

func (decoder *QpackDecoder) readDuplicate(reader *Reader, base int) error {
	index, err := reader.ReadIndex(5)
	if err != nil {
		return err
	}
	decoder.logger.Printf("duplicate %v", index)
	entry := decoder.Table.GetDynamic(index, base)
	if entry == nil {
		return ErrIndexError
	}
	added := decoder.table.Insert(entry.Name(), entry.Value(), nil)
	decoder.available <- added.Base()
	return nil
}

func (decoder *QpackDecoder) readDynamicUpdate(reader *Reader) error {
	capacity, err := reader.ReadInt(5)
	if err != nil {
		return err
	}
	decoder.logger.Printf("update capacity %v", capacity)
	decoder.Table.SetCapacity(TableCapacity(capacity))
	return nil
}

// ReadTableUpdates reads a single block of table updates.  If you use ServiceUpdates,
// this function should need to be used at all.
func (decoder *QpackDecoder) ReadTableUpdates(r io.Reader) error {
	reader := NewReader(r)

	for {
		base := decoder.Table.Base()
		b, err := reader.ReadBit()
		if err == io.EOF {
			break // Success
		}
		if err != nil {
			return err
		}

		if b == 1 {
			err = decoder.readInsertWithNameReference(reader, base)
			if err != nil {
				return err
			}
			continue
		}
		b, err = reader.ReadBit()
		if err != nil {
			return err
		}
		if b == 1 {
			err = decoder.readInsertWithNameLiteral(reader, base)
			if err != nil {
				return err
			}
			continue
		}
		b, err = reader.ReadBit()
		if err != nil {
			return err
		}
		if b == 1 {
			err = decoder.readDynamicUpdate(reader)
		} else {
			err = decoder.readDuplicate(reader, base)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (decoder *QpackDecoder) readIndexed(reader *Reader, base int) (*HeaderField, error) {
	static, err := reader.ReadBit()
	if err != nil {
		return nil, err
	}
	index, err := reader.ReadIndex(6)
	if err != nil {
		return nil, err
	}
	decoder.logger.Printf("indexed (static=%v) %v", static == 1, index)
	var entry Entry
	if static == 1 {
		entry = decoder.Table.GetStatic(index)
	} else {
		entry = decoder.Table.GetDynamic(index, base)
	}
	if entry == nil {
		return nil, ErrIndexError
	}
	return &HeaderField{entry.Name(), entry.Value(), false}, nil
}

func (decoder *QpackDecoder) readPostBaseIndexed(reader *Reader, base int) (*HeaderField, error) {
	postBase, err := reader.ReadIndex(4)
	if err != nil {
		return nil, err
	}
	decoder.logger.Printf("post-base indexed %v", postBase)
	entry := decoder.Table.GetDynamic(-1-postBase, base)
	if entry == nil {
		return nil, ErrIndexError
	}
	decoder.logger.Printf("entry %v", entry)
	return &HeaderField{entry.Name(), entry.Value(), false}, nil
}

func (decoder *QpackDecoder) readLiteralWithNameReference(reader *Reader, base int) (*HeaderField, error) {
	neverIndex, err := reader.ReadBit()
	if err != nil {
		return nil, err
	}
	static, err := reader.ReadBit()
	if err != nil {
		return nil, err
	}
	nameIndex, err := reader.ReadIndex(4)
	if err != nil {
		return nil, err
	}
	decoder.logger.Printf("literal name ref (sensitive=%v, static=%v) %v",
		neverIndex == 1, static == 1, nameIndex)
	var nameEntry Entry
	if static == 1 {
		nameEntry = decoder.Table.GetStatic(nameIndex)
	} else {
		nameEntry = decoder.Table.GetDynamic(nameIndex, base)
	}
	if nameEntry == nil {
		return nil, ErrIndexError
	}

	value, err := reader.ReadString(7)
	if err != nil {
		return nil, err
	}
	return &HeaderField{nameEntry.Name(), value, neverIndex == 1}, nil
}

func (decoder *QpackDecoder) readLiteralWithPostBaseNameReference(reader *Reader, base int) (*HeaderField, error) {
	neverIndex, err := reader.ReadBit()
	if err != nil {
		return nil, err
	}
	postBase, err := reader.ReadIndex(3)
	if err != nil {
		return nil, err
	}
	decoder.logger.Printf("literal name ref (sensitive=%v) %v",
		neverIndex == 1, postBase)
	nameEntry := decoder.Table.GetDynamic(-1*postBase, base)
	if nameEntry == nil {
		return nil, ErrIndexError
	}

	value, err := reader.ReadString(7)
	if err != nil {
		return nil, err
	}
	return &HeaderField{nameEntry.Name(), value, neverIndex == 1}, nil
}

func (decoder *QpackDecoder) readLiteralWithNameLiteral(reader *Reader, base int) (*HeaderField, error) {
	neverIndex, err := reader.ReadBit()
	if err != nil {
		return nil, err
	}
	name, err := reader.ReadString(3)
	if err != nil {
		return nil, err
	}
	value, err := reader.ReadString(7)
	if err != nil {
		return nil, err
	}
	return &HeaderField{name, value, neverIndex == 1}, nil
}

func (decoder *QpackDecoder) decodeLargestBase(lrRaw uint64) int {
	decoder.logger.Printf("largest reference %v, current base %v",
		lrRaw, decoder.Table.Base())
	if lrRaw == 0 {
		return 0
	}
	maxEntries := uint64(decoder.Table.Capacity() / entryOverhead)
	fullRange := maxEntries * 2

	// Determine the maximum possible value, which is base + maxEntries
	maxValue := uint64(decoder.Table.Base()) + maxEntries
	// Then round down to a multiple of the full range.
	rounded := maxValue / fullRange * fullRange
	// Now add the value (less 1) to this baseline.
	largestReference := rounded + lrRaw - 1
	// If it overflows, cut it back down.
	if largestReference > maxValue && largestReference >= fullRange {
		largestReference -= fullRange
	}
	// Convert largestReference into largest base.
	return int(largestReference) + 1
}

// readBase reads the header block header and blocks until the decoder is
// ready to process the remainder of the block.
func (decoder *QpackDecoder) readBase(reader *Reader) (int, int, error) {
	lrRaw, err := reader.ReadInt(8)
	if err != nil {
		return 0, 0, err
	}
	largestBase := decoder.decodeLargestBase(lrRaw)
	// This blocks until the dynamic table is ready.
	decoder.table.WaitForEntry(largestBase)

	sign, err := reader.ReadBit()
	if err != nil {
		return 0, 0, err
	}
	delta, err := reader.ReadIndex(7)
	if err != nil {
		return 0, 0, err
	}
	if sign == 1 && delta == 0 {
		return 0, 0, errors.New("invalid delta for base index")
	}
	decoder.logger.Printf("base delta %v %v", sign, delta)
	// Sign: 1 means negative, 0 means positive.
	base := largestBase + (delta * (1 - 2*int(sign)))
	decoder.logger.Printf("base %v", base)
	return largestBase, base, nil
}

// ReadHeaderBlock decodes header fields as they arrive.
func (decoder *QpackDecoder) ReadHeaderBlock(r io.Reader, id uint64) ([]HeaderField, error) {
	reader := NewReader(r)
	largestBase, base, err := decoder.readBase(reader)
	if err != nil {
		return nil, err
	}

	headers := []HeaderField{}
	addHeader := func(h *HeaderField) {
		decoder.logger.Printf("add %v", h)
		headers = append(headers, *h)
	}

	for {
		b, err := reader.ReadBit()
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
			addHeader(h)
			continue
		}

		b, err = reader.ReadBit()
		if err != nil {
			return nil, err
		}
		if b == 1 {
			h, err := decoder.readLiteralWithNameReference(reader, base)
			if err != nil {
				return nil, err
			}
			addHeader(h)
			continue
		}

		b, err = reader.ReadBit()
		if err != nil {
			return nil, err
		}
		if b == 1 {
			h, err := decoder.readLiteralWithNameLiteral(reader, base)
			if err != nil {
				return nil, err
			}
			addHeader(h)
			continue
		}

		b, err = reader.ReadBit()
		if err != nil {
			return nil, err
		}
		var h *HeaderField
		if b == 1 {
			h, err = decoder.readPostBaseIndexed(reader, base)
		} else {
			h, err = decoder.readLiteralWithPostBaseNameReference(reader, base)
		}
		if err != nil {
			return nil, err
		}
		addHeader(h)
	}

	if largestBase > 0 {
		decoder.acknowledged <- &headerBlockAck{id, largestBase}
	}
	return headers, nil
}

// Cancelled tells the decoder that the identifier was cancelled.  The decoder
// informs the encoder about this.  This ensures that the encoder can know
// to release any references that might not have been acknowledged.
func (decoder *QpackDecoder) Cancelled(id uint64) {
	decoder.cancelled <- id
}

// Close tells the decoder to stop.  Mostly this is so it can stop providing
// acknowledgments.
func (decoder *QpackDecoder) Close() error {
	close(decoder.available)
	return nil
}
