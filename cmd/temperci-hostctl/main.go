// Command temperci-hostctl restarts TemperCI systemd units (allowlisted only).
//
// Usage:
//
//	temperci-hostctl restart control|agent|all
//	temperci-hostctl status control|agent|all
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "usage: %s restart|status control|agent|all\n", os.Args[0])
		os.Exit(2)
	}
	action := os.Args[1]
	target := os.Args[2]
	units, err := unitsFor(target)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	switch action {
	case "restart":
		args := append([]string{"restart"}, units...)
		run("systemctl", args...)
	case "status":
		args := append([]string{"is-active"}, units...)
		run("systemctl", args...)
	default:
		fmt.Fprintf(os.Stderr, "unknown action %q\n", action)
		os.Exit(2)
	}
}

func unitsFor(target string) ([]string, error) {
	switch target {
	case "control":
		return []string{"temperci-control.service"}, nil
	case "agent":
		return []string{"temperci-agent.service"}, nil
	case "all":
		return []string{"temperci-control.service", "temperci-agent.service"}, nil
	default:
		return nil, fmt.Errorf("target must be control, agent, or all")
	}
}

func run(name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s %s: %v\n", name, strings.Join(args, " "), err)
		os.Exit(1)
	}
}
