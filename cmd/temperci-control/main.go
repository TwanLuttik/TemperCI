// Command temperci-control is the TemperCI control plane.
//
// It receives GitHub App webhooks, mints JIT self-hosted runner configs,
// and assigns jobs to host agents. This binary is a version/help stub until
// phase 2 implements the real server.
package main

import (
	"flag"
	"fmt"
	"os"
)

// version is set at link time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s [flags]\n\n", os.Args[0])
		fmt.Fprintf(flag.CommandLine.Output(), "temperci-control is the TemperCI control plane (GitHub webhooks, JIT, scheduling).\n\n")
		fmt.Fprintf(flag.CommandLine.Output(), "Flags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	flag.Usage()
	os.Exit(2)
}
