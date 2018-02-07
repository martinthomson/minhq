package hc_test

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/martinthomson/minhq/hc"
	"github.com/stvp/assert"
)

func TestQcramEncoder(t *testing.T) {
	var encoder *hc.QcramEncoder

	for _, tc := range testCases {
		if tc.resetTable {
			encoder = new(hc.QcramEncoder)
			encoder.SetCapacity(256)
			// The examples in RFC 7541 index date, which is of questionable utility.
			encoder.SetIndexPreference("date", true)
		}
		if tc.huffman {
			encoder.HuffmanPreference = hc.HuffmanCodingAlways
		} else {
			encoder.HuffmanPreference = hc.HuffmanCodingNever
		}

		var controlBuf bytes.Buffer
		var headerBuf bytes.Buffer
		err := encoder.WriteHeaderBlock(&controlBuf, &headerBuf, tc.headers...)
		assert.Nil(t, err)

		fmt.Println("control", hex.EncodeToString(controlBuf.Bytes()))
		fmt.Println("header ", hex.EncodeToString(headerBuf.Bytes()))

		expectedControl, err := hex.DecodeString(tc.qcramControl)
		assert.Nil(t, err)
		if len(expectedControl) == 0 {
			// In a gross violation of expectations resulting from go's insistence on not
			// having constructors, controlBuf.Bytes() returns nil if it hasn't been
			// written to yet.
			assert.Equal(t, 0, controlBuf.Len())
		} else {
			assert.Equal(t, expectedControl, controlBuf.Bytes())
		}

		expectedHeader, err := hex.DecodeString(tc.qcramHeader)
		assert.Nil(t, err)
		assert.Equal(t, expectedHeader, headerBuf.Bytes())

		assert.Equal(t, tc.tableSize, encoder.Table.Used())
		checkDynamicTable(t, &encoder.Table, tc.dynamicTable)
	}
}
