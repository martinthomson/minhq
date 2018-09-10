package main

import (
	"fmt"
	"os"
	"runtime"
)

func usage() {
	msg := fmt.Sprintf("Usage: %s <cmd> [args]\n\n", os.Args[0])
	msg += "Encode from a QIF file:\n"
	msg += "    ... encode [-a] [in [out]]\n\n"
	msg += "    -a    Treat every block as immediately acknowledged\n\n"
	msg += "Decode from a QIF file:\n"
	msg += "    ... decode [in [out]]\n\n"
	os.Stderr.WriteString(msg)
	os.Exit(2)
}

func check(err error) {
	if err != nil {
		fmt.Printf("error: Invalid input %v\n", err)
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

	args := os.Args[2:]
	switch os.Args[1] {
	case "encode":
		ack := false
		if len(args) >= 1 && args[0] == "-a" {
			ack = true
			args = args[1:]
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
		enc.Encode()

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
		dec.Decode()

	default:
		usage()
	}
}
