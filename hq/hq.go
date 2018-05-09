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
}

type commonFlags struct {
	TableSize uint64
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

func (a *commandLine) parseServer(args []string) {
	a.usage = "server <address:port> <cert> <key>"
	if len(args) < 3 {
		a.exit("missing arguments")
	}
	a.args = &serverArguments{args[0], args[1], args[2]}
}

func (a *commandLine) parseClient(args []string) {
	a.usage = "client <URL>"
	if len(args) < 1 {
		a.exit("missing arguments")
	}
	a.args = &clientArguments{args}
}

func (a *commandLine) Parse() {
	a.fs.Init(os.Args[0], flag.ExitOnError)

	a.usage = "server|client <args...>"
	a.fs.Usage = func() {
		a.print("Usage: %s [flags] %s", a.fs.Name(), a.usage)
		a.fs.PrintDefaults()
	}

	a.fs.Uint64Var(&a.TableSize, "t", 1<<12, "QPACK table size")
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

func main() {
	args := new(commandLine)
	args.Parse()

	switch a := args.args.(type) {
	case *clientArguments:
		runClient(&args.commonFlags, a)
	case *serverArguments:
		runServer(&args.commonFlags, a)
	default:
		panic("unknown command")
	}
}

func die(msg string, err error) {
	fmt.Fprintln(os.Stderr, "error "+msg+": "+err.Error())
	os.Exit(1)
}

func runClient(common *commonFlags, args *clientArguments) {
	var client minhq.Client
	client.Config.DecoderTableCapacity = hc.TableCapacity(common.TableSize)

	for _, url := range args.URLs {
		request, err := client.Fetch("GET", url)
		if err != nil {
			die("creating fetch", err)
		}
		go func() {
			defer request.Close()
			_, err := io.Copy(request, os.Stdin)
			if err != nil {
				die("sending request body", err)
			}
		}()

		response := <-request.Response
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

func runServer(common *commonFlags, args *serverArguments) {
	config := minhq.Config{DecoderTableCapacity: hc.TableCapacity(common.TableSize)}
	server, err := minhq.Listen(args.Address, args.CertFile, args.KeyFile, &config)
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
		resp, err := req.Respond(200, hc.HeaderField{Name: "User-Agent", Value: "hq"})
		if err != nil {
			req.C.Close()
			continue
		}

		multi := io.MultiWriter(os.Stdout, resp)
		fmt.Fprintf(multi, req.String())
		fmt.Println("[[[")
		_, err = io.Copy(multi, req)
		if err != nil {
			req.C.Close()
			continue
		}
		fmt.Println("]]]")
		err = resp.Close()
		if err != nil {
			req.C.Close()
			continue
		}
	}
}
