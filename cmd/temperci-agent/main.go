// Command temperci-agent is the TemperCI host agent.
//
// It maintains a warm microVM pool, binds VMs to assigned jobs, starts the
// official actions/runner with JIT config, and tears down guests plus host
// scratch after every job. This binary is a version/help stub until later
// phases implement pool and VMM logic.
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
		fmt.Fprintf(flag.CommandLine.Output(), "temperci-agent is the TemperCI host agent (warm pool, bind, teardown).\n\n")
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
