package main

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"github.com/martinthomson/minhq/hc"
	hqio "github.com/martinthomson/minhq/io"
)

type decoder struct {
	inputFile  *os.File
	input      hqio.BitReader
	outputFile *os.File
	output     io.Writer

	stream uint64
	qpack  *hc.QpackDecoder
}

type devnull struct{}

// Write just throws bytes away, reporting success.
func (devnull *devnull) Write(p []byte) (int, error) {
	return len(p), nil
}

func newDecoder(inputName string, outputName string) *decoder {
	dec := new(decoder)

	var err error
	if inputName == "" {
		dec.inputFile = os.Stdin
	} else {
		dec.inputFile, err = os.Open(inputName)
	}
	check(err)
	defer func(dec *decoder) {
		if dec.outputFile == nil {
			dec.inputFile.Close()
		}
	}(dec)

	if outputName == "" {
		dec.outputFile = os.Stdout
	} else {
		// TODO add options to control behavior regarding the name
		dec.outputFile, err = os.OpenFile(outputName+".hq", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	}
	check(err)

	dec.input = hqio.NewBitReader(dec.inputFile)
	dec.output = dec.outputFile
	dec.qpack = hc.NewQpackDecoder(&devnull{}, 4096)
	return dec
}

func (dec *decoder) readBlock() (uint64, io.Reader, error) {
	stream, err := dec.input.ReadBits(64)
	check(err)
	length, err := dec.input.ReadBits(32)
	fmt.Println(length)
	check(err)
	return stream, &io.LimitedReader{R: dec.input, N: int64(length)}, nil
}

func (dec *decoder) writeBlock(block []hc.HeaderField) {
	for _, hf := range block {
		v := fmt.Sprintf("%s\t%s\n", hf.Name, hf.Value)
		_, err := io.WriteString(dec.output, v)
		check(err)
	}
	dec.output.Write([]byte{'\n'})
}

func (dec *decoder) Decode() {
	updateStream := hqio.NewConcatenatingReader()
	go dec.qpack.ReadTableUpdates(updateStream)
	for {
		stream, reader, err := dec.readBlock()
		if err == io.EOF {
			return
		}
		check(err)

		var blockBytes bytes.Buffer
		_, err = io.Copy(&blockBytes, reader)
		check(err)
		os.Stderr.WriteString(fmt.Sprintf("%x [%d] %x\n",
			stream, blockBytes.Len(), blockBytes.Bytes()))
		reader = &blockBytes

		if stream == 0 {
			updateStream.Add(reader)
		} else {
			headers, err := dec.qpack.ReadHeaderBlock(reader, stream)
			check(err)
			dec.writeBlock(headers)
		}
	}
}

func (dec *decoder) Close() error {
	check(dec.inputFile.Close())
	check(dec.outputFile.Close())
	return nil
}
