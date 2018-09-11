package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"runtime"
	"strconv"

	"github.com/martinthomson/minhq/hc"
)

var logbuf bytes.Buffer

func usage() {
	msg := "Usage: %s <cmd> [args]\n\n" +
		"Encode from a QIF file:\n" +
		"    ... encode [-a] [-t size] [in [out]]\n\n" +
		"    -a    Treat every block as immediately acknowledged\n\n" +
		"Decode from a QIF file:\n" +
		"    ... decode [in [out]]\n\n"
	fmt.Fprintf(os.Stderr, msg, os.Args[0])
	os.Exit(2)
}

func check(err error) {
	if err != nil {
		fmt.Printf("error: %v\n", err)
		fmt.Println(logbuf.String())

		buf := make([]byte, 1<<16)
		stackSize := runtime.Stack(buf, true)
		fmt.Printf("%s\n", string(buf[0:stackSize]))
		os.Exit(1)
	}
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}

	logger := log.New(&logbuf, "", log.Lmicroseconds|log.Lshortfile)

	args := os.Args[2:]
	switch os.Args[1] {
	case "encode":
		ack := false
		if len(args) >= 1 && args[0] == "-a" {
			ack = true
			args = args[1:]
		}
		tableSize := hc.TableCapacity(4096)
		if len(args) >= 2 && args[0] == "-t" {
			sz, err := strconv.Atoi(args[1])
			check(err)
			tableSize = hc.TableCapacity(sz)
			args = args[2:]
		}
		var enc *encoder
		switch len(args) {
		case 0:
			enc = newEncoder("", "")
		case 1:
			enc = newEncoder(args[0], "")
		default:
			enc = newEncoder(args[0], args[1])
		}
		defer enc.Close()
		enc.acknowledge = ack
		enc.qpack.SetCapacity(tableSize)
		enc.Encode(logger)

	case "decode":
		var dec *decoder
		switch len(args) {
		case 0:
			dec = newDecoder("", "")
		case 1:
			dec = newDecoder(args[0], "")
		default:
			dec = newDecoder(args[0], args[1])
		}
		defer dec.Close()
		dec.Decode(logger)

	default:
		usage()
	}
}
