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
	token := "k"

	for _, tc := range testCases {
		if tc.resetTable {
			encoder = hc.NewQcramEncoder(256, 256)
			// The examples in RFC 7541 index date, which is of questionable utility.
			encoder.SetIndexPreference("date", true)
		} else {
			// We can use the same token here because always acknowledge before encoding
			// the next block.
			encoder.Acknowledged(token)
		}

		if tc.hpack == "828684be58086e6f2d6361636865" {
			fmt.Println("testing")
		}

		if tc.huffman {
			encoder.HuffmanPreference = hc.HuffmanCodingAlways
		} else {
			encoder.HuffmanPreference = hc.HuffmanCodingNever
		}

		var controlBuf bytes.Buffer
		var headerBuf bytes.Buffer
		err := encoder.WriteHeaderBlock(&controlBuf, &headerBuf, token, tc.headers...)
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

		var dynamicTable = &tc.hpackTable
		if tc.qcramTable != nil {
			dynamicTable = tc.qcramTable
		}
		checkDynamicTable(t, &encoder.Table, dynamicTable)
	}
}
func TestQcramDecoderOrdered(t *testing.T) {
	var decoder *hc.QcramDecoder

	for _, tc := range testCases {
		if tc.resetTable {
			decoder = hc.NewQcramDecoder(256)
		}

		if len(tc.qcramControl) > 0 {
			control, err := hex.DecodeString(tc.qcramControl)
			assert.Nil(t, err)
			err = decoder.ReadTableUpdates(bytes.NewReader(control))
			assert.Nil(t, err)
		}

		var dynamicTable = &tc.hpackTable
		if tc.qcramTable != nil {
			dynamicTable = tc.qcramTable
		}
		checkDynamicTable(t, &decoder.Table, dynamicTable)

		encoded, err := hex.DecodeString(tc.qcramHeader)
		assert.Nil(t, err)
		headers, err := decoder.ReadHeaderBlock(bytes.NewReader(encoded))
		assert.Nil(t, err)

		assert.Equal(t, tc.headers, headers)
	}
}
