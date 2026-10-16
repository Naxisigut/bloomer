package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"bloomer/agent/internal/config"
	"bloomer/agent/internal/service"
)

var version = "0.2.0-dev"
var commit = "unknown"

func main() { os.Exit(run()) }
func run() int {
	flags := flag.NewFlagSet("bloomer-agent", flag.ContinueOnError)
	configPath := flags.String("config", "", "JSON configuration (omitted: user-directory defaults)")
	showVersion := flags.Bool("version", false, "print version and exit")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected positional arguments")
		return 2
	}
	if *showVersion {
		fmt.Printf("bloomer-agent %s (%s)\n", version, commit)
		return 0
	}
	c, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "configuration:", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err = service.Run(ctx, c, version+"+"+commit); err != nil {
		fmt.Fprintln(os.Stderr, "agent:", err)
		return 1
	}
	return 0
}
