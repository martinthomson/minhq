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
		"    ... encode [-a] [-t cap] [-r cap] [in [out]]\n\n" +
		"    -a    Treat every block as immediately acknowledged\n" +
		"    -t    Set the capacity of the table\n" +
		"    -r    Set the referenceable capacity (must be after -t)\n\n" +
		"Decode from a QIF file:\n" +
		"    ... decode [in [out]]\n" +
		"    -t    Set the capacity of the table\n\n"
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

func encode(logger *log.Logger, args []string) {
	ack := false
	capacity := hc.TableCapacity(4096)
	referenceable := hc.TableCapacity(4096)
	maxBlocked := 0
	for len(args) > 1 && args[0][0:1] == "-" {
		if args[0] == "-a" {
			ack = true
			args = args[1:]
		}
		if len(args) >= 2 && args[0] == "-b" {
			sz, err := strconv.Atoi(args[1])
			check(err)
			maxBlocked = sz
			args = args[2:]
		}
		if len(args) >= 2 && args[0] == "-t" {
			sz, err := strconv.Atoi(args[1])
			check(err)
			capacity = hc.TableCapacity(sz)
			referenceable = capacity
			args = args[2:]
		}
		if len(args) >= 2 && args[0] == "-r" {
			sz, err := strconv.Atoi(args[1])
			check(err)
			referenceable = hc.TableCapacity(sz)
			args = args[2:]
		}
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
	enc.qpack.SetCapacity(capacity)
	enc.qpack.SetReferenceableLimit(referenceable)
	enc.qpack.SetMaxBlockedStreams(maxBlocked)
	enc.Encode(logger)
}

func decode(logger *log.Logger, args []string) {
	var dec *decoder
	capacity := hc.TableCapacity(4096)
	for len(args) > 1 && args[0][0:1] == "-" {
		if len(args) >= 2 && args[0] == "-t" {
			sz, err := strconv.Atoi(args[1])
			check(err)
			capacity = hc.TableCapacity(sz)
			args = args[2:]
		}
	}
	switch len(args) {
	case 0:
		dec = newDecoder("", "")
	case 1:
		dec = newDecoder(args[0], "")
	default:
		dec = newDecoder(args[0], args[1])
	}
	defer dec.Close()
	dec.qpack.Table.SetCapacity(capacity)
	dec.Decode(logger)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}

	logger := log.New(&logbuf, "", log.Lmicroseconds|log.Lshortfile)

	switch os.Args[1] {
	case "encode":
		encode(logger, os.Args[2:])

	case "decode":
		decode(logger, os.Args[2:])

	default:
		usage()
	}
}
