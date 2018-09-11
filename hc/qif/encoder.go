package main

import (
	"bytes"
	"io"
	"log"
	"os"

	"github.com/martinthomson/minhq/hc"
	hqio "github.com/martinthomson/minhq/io"
)

type encoder struct {
	inputFile  *os.File
	input      *Reader
	outputFile *os.File
	output     hqio.BitWriter

	acknowledge bool

	stream       uint64
	updateStream bytes.Buffer
	qpack        *hc.QpackEncoder
}

func newEncoder(inputName string, outputName string) *encoder {
	enc := new(encoder)

	var err error
	if inputName == "" {
		enc.inputFile = os.Stdin
	} else {
		enc.inputFile, err = os.Open(inputName)
	}
	check(err)
	defer func(enc *encoder) {
		if enc.outputFile == nil {
			enc.inputFile.Close()
		}
	}(enc)

	if outputName == "" {
		enc.outputFile = os.Stdout
	} else {
		// TODO add options to control behavior regarding the name
		enc.outputFile, err = os.OpenFile(outputName, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	}
	check(err)

	enc.input = NewReader(enc.inputFile)
	enc.output = hqio.NewBitWriter(enc.outputFile)
	enc.qpack = hc.NewQpackEncoder(&enc.updateStream, 4096, 3072)
	return enc
}

func (enc *encoder) writeBlock(logger *log.Logger, id uint64, block *bytes.Buffer) {
	if block.Len() <= 0 {
		return
	}
	logger.Printf("%x [%d] %x\n", id, block.Len(), block.Bytes())

	check(enc.output.WriteBits(id, 64))
	check(enc.output.WriteBits(uint64(block.Len()), 32))
	n, err := io.Copy(enc.output, block)
	check(err)
	if n < int64(block.Len()) {
		check(io.ErrShortWrite)
	}
	block.Reset()
}

func (enc *encoder) Encode(logger *log.Logger) {
	enc.qpack.SetLogger(logger)
	for {
		block, err := enc.input.ReadHeaderBlock()
		if err == io.EOF {
			return
		}
		check(err)

		for _, h := range block {
			logger.Printf("%v\n", h)
		}
		logger.Println()

		enc.stream++
		var headerStream bytes.Buffer
		check(enc.qpack.WriteHeaderBlock(&headerStream, enc.stream, block...))

		enc.writeBlock(logger, 0, &enc.updateStream)
		enc.writeBlock(logger, enc.stream, &headerStream)

		if enc.acknowledge {
			enc.qpack.AcknowledgeHeader(enc.stream)
		}
	}
}

func (enc *encoder) Close() error {
	check(enc.inputFile.Close())
	check(enc.outputFile.Close())
	return nil
}
