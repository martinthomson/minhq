package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"sync"

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

type ioSink struct{}

// Write just throws bytes away, reporting success.
func (sink *ioSink) Write(p []byte) (int, error) {
	return len(p), nil
}

// Close does nothing.
func (sink *ioSink) Close() error {
	return nil
}

var devnull ioSink

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
	dec.qpack = hc.NewQpackDecoder(&devnull, 4096)
	return dec
}

func (dec *decoder) readBlock() (uint64, io.Reader, error) {
	stream, err := dec.input.ReadBits(64)
	if err == io.EOF {
		return 0, nil, err
	}
	check(err)
	length, err := dec.input.ReadBits(32)
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

func (dec *decoder) Decode(logger *log.Logger) {
	dec.qpack.SetLogger(logger)

	// Setup the update stream.
	updateStream := hqio.NewConcatenatingReader()
	go func() {
		check(dec.qpack.ReadTableUpdates(updateStream))
	}()

	for {
		stream, reader, err := dec.readBlock()
		if err == io.EOF {
			break // Done!
		}
		check(err)

		var blockBytes bytes.Buffer
		_, err = io.Copy(&blockBytes, reader)
		check(err)
		logger.Printf("%x [%d] %x\n", stream, blockBytes.Len(), blockBytes.Bytes())

		reader = &blockBytes

		if stream == 0 {
			updateStream.AddReader(reader)
		} else {
			headers, err := dec.qpack.ReadHeaderBlock(reader, stream)
			check(err)
			dec.writeBlock(headers)
		}
	}
}

func (dec *decoder) DecodeAsync(logger *log.Logger) {
	dec.qpack.SetLogger(logger)

	// Setup the update stream.
	updateStream := hqio.NewConcatenatingReader()
	go func() {
		logger.Println("Reading table updates")
		check(dec.qpack.ReadTableUpdates(updateStream))
	}()

	// This is gross, and it's all Alan's fault.
	// Not only does this need to be asynchronous, it also needs to sort the final
	// results based on stream ID.
	// This block receives results as they arrive, stores them, then feeds them
	// to the sorted channel when everything is done.
	type result struct {
		stream  uint64
		headers []hc.HeaderField
	}
	results := make(chan *result)
	sorted := make(chan *result)
	go func(output <-chan *result, sorted chan<- *result) {
		var allResults []*result
		for res := range results {
			allResults = append(allResults, res)
		}
		sort.Slice(allResults, func(i int, j int) bool {
			return allResults[i].stream < allResults[j].stream
		})
		for _, res := range allResults {
			sorted <- res
		}
		close(sorted)
	}(results, sorted)

	var wg sync.WaitGroup
	for {
		stream, reader, err := dec.readBlock()
		if err == io.EOF {
			break // Done!
		}
		check(err)

		var block bytes.Buffer
		_, err = io.Copy(&block, reader)
		check(err)
		logger.Printf("%x [%d] %x\n", stream, block.Len(), block.Bytes())
		reader = &block

		// Process header table updates on this thread.
		if stream == 0 {
			updateStream.AddReader(reader)
			continue
		}

		// Spawn goroutines for all header blocks, because
		// those might need to wait for headers to arrive.
		wg.Add(1)
		go func(stream uint64, reader io.Reader) {
			defer wg.Done()
			logger.Println("stream", stream)
			headers, err := dec.qpack.ReadHeaderBlock(reader, stream)
			check(err)
			results <- &result{stream, headers}
		}(stream, reader)
	}

	updateStream.Close()
	wg.Wait()
	close(results)
	for res := range sorted {
		dec.writeBlock(res.headers)
	}
}

func (dec *decoder) Close() error {
	check(dec.inputFile.Close())
	check(dec.outputFile.Close())
	return nil
}
