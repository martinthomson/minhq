package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/martinthomson/minhq"
	"github.com/martinthomson/minhq/hc"
)

type serverArguments struct {
	Address  string
	CertFile string
	KeyFile  string
}

type clientArguments struct {
	URLs []string
	File string
}

type commonFlags struct {
	TableSize          uint64
	ConcurrentDecoders uint64
}

type commandLine struct {
	usage string
	fs    flag.FlagSet

	commonFlags
	args interface{}
}

func (a *commandLine) print(format string, params ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", params...)
}

func (a *commandLine) exit(msg string) {
	a.print("error: " + msg)
	a.fs.Usage()
	os.Exit(2)
}

func (a *commandLine) parseServer(params []string) {
	a.usage = "server <address:port> <cert> <key>"
	if len(params) < 3 {
		a.exit("missing arguments")
	}
	a.args = &serverArguments{params[0], params[1], params[2]}
}

func (a *commandLine) parseClient(params []string) {
	a.usage = "client <URL>"
	if len(params) < 1 {
		a.exit("missing arguments")
	}

	var args clientArguments
	fs := flag.NewFlagSet(a.fs.Name()+" client", flag.ExitOnError)
	fs.Usage = func() {
		a.print("Usage: %s [...] client [flags] <url> [url [...]]")
		fs.PrintDefaults()
	}
	fs.StringVar(&args.File, "d", "", "read request body from file")
	fs.Parse(params)
	args.URLs = fs.Args()
	a.args = &args
}

func (a *commandLine) Parse() {
	a.fs.Init(os.Args[0], flag.ExitOnError)

	a.fs.Usage = func() {
		a.print("Usage: %s [common options] <command> [args...]", a.fs.Name())
		a.print("Commands:")
		a.print("    client - Make a request")
		a.print("    server - Run a server")
		a.print("Common Options:")
		a.fs.PrintDefaults()
	}

	a.fs.Uint64Var(&a.TableSize, "t", 1<<12, "QPACK table size")
	a.fs.Uint64Var(&a.ConcurrentDecoders, "b", 100, "QPACK max blocked streams")
	a.fs.Parse(os.Args[1:])

	if a.fs.NArg() < 1 {
		a.exit("missing argument")
	}
	positional := a.fs.Args()
	switch positional[0] {
	case "server", "s":
		a.parseServer(positional[1:])
	case "client", "c":
		a.parseClient(positional[1:])
	default:
		a.exit("unknown option: " + positional[0])
	}
}

func boundsCheck(v uint64, limit uint64, message string) {
	if v > limit {
		panic(fmt.Sprintf("%s can't be more than %d", message, limit))
	}
}

func main() {
	args := new(commandLine)
	args.Parse()

	boundsCheck(args.commonFlags.TableSize, uint64(^uint(0)), "table size (-t)")
	boundsCheck(args.commonFlags.ConcurrentDecoders, 1<<16-1, "max blocked (-b)")
	config := &minhq.Config{
		DecoderTableCapacity: hc.TableCapacity(args.commonFlags.TableSize),
		ConcurrentDecoders:   uint16(args.commonFlags.ConcurrentDecoders),
	}

	switch a := args.args.(type) {
	case *clientArguments:
		runClient(config, a)
	case *serverArguments:
		runServer(config, a)
	default:
		panic("unknown command")
	}
}

func die(msg string, err error) {
	fmt.Fprintln(os.Stderr, "error "+msg+": "+err.Error())
	os.Exit(1)
}

func runClient(config *minhq.Config, args *clientArguments) {
	client := minhq.Client{Config: *config}

	for _, url := range args.URLs {
		request, err := client.Fetch("GET", url)
		if err != nil {
			die("creating fetch", err)
		}
		if args.File != "" {
			go func() {
				defer request.Close()
				inputFile, err := os.Open(args.File)
				if err != nil {
					die("opening input file: "+args.File, err)
				}
				_, err = io.Copy(request, inputFile)
				if err != nil {
					die("sending request body", err)
				}
			}()
		} else {
			request.Close()
		}

		response := request.Response()
		fmt.Println(response)
		fmt.Println("[[[")
		_, err = io.Copy(os.Stdout, response)
		if err != nil {
			die("reading response body", err)
		}
		fmt.Println("]]]")
	}
	client.Close()
}

func runServer(config *minhq.Config, args *serverArguments) {
	server, err := minhq.Listen(args.Address, args.CertFile, args.KeyFile, config)
	if err != nil {
		die("starting server", err)
	}
	go func() {
		for <-server.Connections != nil {
		}
	}()

	for {
		req := <-server.Requests

		// This handles just one request at a time.
		resp, err := req.Respond(200, hc.HeaderField{Name: "Server", Value: "hq"})
		if err != nil {
			req.C.Close()
			continue
		}

		multi := io.MultiWriter(os.Stdout, resp)
		fmt.Fprintf(multi, req.String())
		fmt.Fprintln(multi)
		fmt.Fprintln(multi, "[[[")
		_, err = io.Copy(multi, req)
		if err != nil {
			req.C.Close()
			continue
		}
		fmt.Fprintln(multi, "]]]")
		err = resp.Close()
		if err != nil {
			req.C.Close()
			continue
		}
	}
}
