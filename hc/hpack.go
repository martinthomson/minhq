package hc

import (
	"io"
)

const hpackOverhead = TableCapacity(32)

// hpackEntry is an entry in the dynamic table.
type hpackEntry struct {
	BasicDynamicEntry
}

func (hd *hpackEntry) Size() TableCapacity {
	return hpackOverhead + TableCapacity(len(hd.Name())+len(hd.Value()))
}

// Index returns the index into the HPACK table.
func (hd *hpackEntry) Index(base int) int {
	if base < hd.Base() {
		// This entry can't be referenced from the given base.
		return 0
	}
	return len(hpackStaticTable) + 1 + base - hd.Base()
}

// HpackTable is an HPACK implementation of the table.  No extra trimmings.
type HpackTable struct {
	tableCommon
}

// Insert for HPACK is simple.
func (table *HpackTable) Insert(name string, value string, evict evictionCheck) DynamicEntry {
	entry := &hpackEntry{BasicDynamicEntry{name, value, 0}}
	table.insert(entry, nil)
	return entry
}

// GetStatic returns the entry at the specific index.
func (table *HpackTable) GetStatic(i int) Entry {
	// Note the shift to 1-based indexing here.
	if i <= 0 || i > len(hpackStaticTable) {
		return nil
	}
	return hpackStaticTable[i-1]
}

// Get retrieves an entry.
func (table *HpackTable) Get(i int) Entry {
	if i <= 0 {
		return nil
	}
	e := table.GetStatic(i)
	if e != nil {
		return e
	}
	return table.GetDynamic(i-len(hpackStaticTable)-1, table.Base())
}

// Lookup finds an entry, see Table.Lookup.
func (table *HpackTable) Lookup(name string, value string) (Entry, Entry) {
	return table.lookupImpl(hpackStaticTable, name, value, len(table.dynamic))
}

// Index returns the HPACK table index for the given entry.
func (table *HpackTable) Index(e Entry) int {
	_, dynamic := e.(DynamicEntry)
	if dynamic {
		return len(hpackStaticTable) + 1 + table.Base() - e.Base()
	}
	return e.Base()
}

// HpackDecoder is the top-level class for header decompression.
type HpackDecoder struct {
	decoderCommon
	table *HpackTable
}

// NewHpackDecoder makes a new decoder and sets it up.
func NewHpackDecoder() *HpackDecoder {
	decoder := new(HpackDecoder)
	decoder.table = new(HpackTable)
	decoder.Table = decoder.table
	return decoder
}

func (decoder *HpackDecoder) readIndexed(reader *Reader) (*HeaderField, error) {
	index, err := reader.ReadIndex(7)
	if err != nil {
		return nil, err
	}
	entry := decoder.table.Get(index)
	if entry == nil {
		return nil, ErrIndexError
	}
	return &HeaderField{entry.Name(), entry.Value(), false}, nil
}

func (decoder *HpackDecoder) readNameValue(reader *Reader, prefix byte) (string, string, error) {
	index, err := reader.ReadIndex(prefix)
	if err != nil {
		return "", "", err
	}
	var name string
	if index == 0 {
		name, err = reader.ReadString(7)
		if err != nil {
			return "", "", err
		}
	} else {
		entry := decoder.table.Get(index)
		if entry == nil {
			return "", "", ErrIndexError
		}
		name = entry.Name()
	}
	value, err := reader.ReadString(7)
	if err != nil {
		return "", "", err
	}
	return name, value, nil
}

func (decoder *HpackDecoder) readIncremental(reader *Reader) (*HeaderField, error) {
	name, value, err := decoder.readNameValue(reader, 6)
	if err != nil {
		return nil, err
	}
	decoder.Table.Insert(name, value, nil)
	return &HeaderField{name, value, false}, nil
}

func (decoder *HpackDecoder) readCapacity(reader *Reader) error {
	capacity, err := reader.ReadInt(5)
	if err != nil {
		return err
	}
	decoder.table.SetCapacity(TableCapacity(capacity))
	return nil
}

func (decoder *HpackDecoder) readLiteral(reader *Reader) (*HeaderField, error) {
	ni, err := reader.ReadBit()
	if err != nil {
		return nil, err
	}

	name, value, err := decoder.readNameValue(reader, 4)
	if err != nil {
		return nil, err
	}
	return &HeaderField{name, value, ni == 1}, nil
}

// ReadHeaderBlock decodes header fields as they arrive.
func (decoder *HpackDecoder) ReadHeaderBlock(r io.Reader) ([]HeaderField, error) {
	reader := NewReader(r)
	headers := []HeaderField{}
	for {
		b, err := reader.ReadBit()
		if err == io.EOF {
			break // Success!
		}
		if err != nil {
			return nil, err
		}

		if b == 1 {
			h, err := decoder.readIndexed(reader)
			if err != nil {
				return nil, err
			}
			headers = append(headers, *h)
			continue
		}

		b, err = reader.ReadBit()
		if err != nil {
			return nil, err
		}

		if b == 1 {
			h, err := decoder.readIncremental(reader)
			if err != nil {
				return nil, err
			}
			headers = append(headers, *h)
			continue
		}

		b, err = reader.ReadBit()
		if err != nil {
			return nil, err
		}

		if b == 1 {
			err := decoder.readCapacity(reader)
			if err != nil {
				return nil, err
			}
			continue
		}

		h, err := decoder.readLiteral(reader)
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

// HpackEncoder is the top-level class for header compression.
type HpackEncoder struct {
	encoderCommon
	table *HpackTable
	// Track changes to capacity so that we can reflect them properly.
	minCapacity  TableCapacity
	nextCapacity TableCapacity
}

// NewHpackEncoder makes a new encoder and sets it up.
func NewHpackEncoder(capacity TableCapacity) *HpackEncoder {
	encoder := new(HpackEncoder)
	encoder.table = new(HpackTable)
	encoder.Table = encoder.table
	encoder.SetCapacity(capacity)
	return encoder
}

func (encoder *HpackEncoder) writeCapacity(writer *Writer, c TableCapacity) error {
	err := writer.WriteBits(1, 3)
	if err != nil {
		return err
	}
	err = writer.WriteInt(uint64(c), 5)
	if err != nil {
		return err
	}
	return nil
}

func (encoder *HpackEncoder) writeCapacityChange(writer *Writer) error {
	if encoder.minCapacity < encoder.Table.Capacity() {
		err := encoder.writeCapacity(writer, encoder.minCapacity)
		if err != nil {
			return err
		}
		encoder.table.SetCapacity(encoder.minCapacity)
	}
	if encoder.nextCapacity > encoder.Table.Capacity() {
		err := encoder.writeCapacity(writer, encoder.nextCapacity)
		if err != nil {
			return err
		}
		encoder.table.SetCapacity(encoder.nextCapacity)
		encoder.minCapacity = encoder.nextCapacity
	}
	return nil
}

func (encoder *HpackEncoder) writeIndexed(writer *Writer, entry Entry) error {
	err := writer.WriteBit(1)
	if err != nil {
		return err
	}
	return writer.WriteInt(uint64(encoder.Table.Index(entry)), 7)
}

// Write out a name/value pair to the specified writer, with the specified
// integer prefix size on the name index.
func (encoder *HpackEncoder) writeNameValue(writer *Writer, h HeaderField,
	nameEntry Entry, prefix byte, base int) error {
	nameIndex := uint64(0)
	if nameEntry != nil {
		nameIndex = uint64(encoder.Table.Index(nameEntry))
	}
	err := writer.WriteInt(nameIndex, prefix)
	if err != nil {
		return err
	}
	if nameEntry == nil {
		err = writer.WriteStringRaw(h.Name, 7, encoder.HuffmanPreference)
		if err != nil {
			return err
		}
	}

	return writer.WriteStringRaw(h.Value, 7, encoder.HuffmanPreference)
}

func (encoder *HpackEncoder) writeIncremental(writer *Writer, h HeaderField, nameEntry Entry) error {
	err := writer.WriteBits(1, 2)
	if err != nil {
		return err
	}

	err = encoder.writeNameValue(writer, h, nameEntry, 6, encoder.Table.Base())
	if err != nil {
		return err
	}
	encoder.Table.Insert(h.Name, h.Value, nil)
	return nil
}

func (encoder HpackEncoder) writeLiteral(writer *Writer, h HeaderField, nameEntry Entry) error {
	code := uint64(0)
	if h.Sensitive {
		code = 1
	}
	err := writer.WriteBits(code, 4)
	if err != nil {
		return err
	}

	return encoder.writeNameValue(writer, h, nameEntry, 4, encoder.Table.Base())
}

// WriteHeaderBlock writes out a header block.
func (encoder *HpackEncoder) WriteHeaderBlock(w io.Writer, headers ...HeaderField) error {
	writer := NewWriter(w)
	err := encoder.writeCapacityChange(writer)
	if err != nil {
		return err
	}
	pseudo := true
	for _, h := range headers {
		if h.Name[0] == ':' {
			if !pseudo {
				return ErrPseudoHeaderOrdering
			}
		} else {
			pseudo = false
		}
		if h.Sensitive {
			// It's not clear here whether the name is sensitive, but let's assume that
			// it might be. It's not exactly rational to put secrets in header field
			// names (how do you find them again?), but it's safer not to assume rational
			// behaviour.
			err = encoder.writeLiteral(writer, h, nil)
		} else {
			m, nm := encoder.Table.Lookup(h.Name, h.Value)
			if m != nil {
				err = encoder.writeIndexed(writer, m)
			} else if encoder.shouldIndex(h) {
				err = encoder.writeIncremental(writer, h, nm)
			} else {
				err = encoder.writeLiteral(writer, h, nm)
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// SetCapacity is used to set the new header table capacity. This could reflect
// the value from the peer's settings. Smaller values than the one provided by
// the peer can be set, if there are constraints on memory and the peer isn't
// trusted to set sane values. Failing to call this will result in no additions
// to the dynamic table and poor compression performance.
func (encoder *HpackEncoder) SetCapacity(c TableCapacity) {
	if c < encoder.minCapacity {
		encoder.minCapacity = c
	}
	encoder.nextCapacity = c
}
