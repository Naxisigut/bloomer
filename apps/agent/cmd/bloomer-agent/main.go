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

var version = "1.0.0"
var commit = "unknown"

func main() { os.Exit(run()) }
func run() int {
	flags := flag.NewFlagSet("bloomer-agent", flag.ContinueOnError)
	configPath := flags.String("config", "", "JSON configuration (omitted: user-directory defaults)")
	check := flags.Bool("check", false, "validate configuration and official credential without network requests")
	backup := flags.String("backup", "", "write a consistent database backup; service must be stopped")
	restore := flags.String("restore", "", "restore a consistent database backup; service must be stopped")
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
	operations := 0
	for _, enabled := range []bool{*check, *backup != "", *restore != ""} {
		if enabled {
			operations++
		}
	}
	if operations > 1 {
		fmt.Fprintln(os.Stderr, "choose only one maintenance operation")
		return 2
	}
	if *check {
		if err := service.Preflight(c); err != nil {
			fmt.Fprintln(os.Stderr, "preflight:", err)
			return 1
		}
		fmt.Println("configuration and credentials valid")
		return 0
	}
	if *backup != "" || *restore != "" {
		if err := service.Maintain(ctx, c, *backup, *restore); err != nil {
			fmt.Fprintln(os.Stderr, "maintenance:", err)
			return 1
		}
		fmt.Println("database maintenance completed")
		return 0
	}

	if err = service.Run(ctx, c, version+"+"+commit); err != nil {
		fmt.Fprintln(os.Stderr, "agent:", err)
		return 1
	}
	return 0
}
