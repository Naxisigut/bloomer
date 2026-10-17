package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"bloomer/agent/internal/management"
	"bloomer/agent/internal/release"
)

var version = release.Version
var commit = "unknown"

func main() { os.Exit(run()) }
func run() int {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: bloomerctl install|upgrade|start|stop|status|health|restore|verify|version [options]")
		return 2
	}
	operation := os.Args[1]
	if operation == "version" {
		fmt.Printf("bloomerctl %s (%s)\n", version, commit)
		return 0
	}
	flags := flag.NewFlagSet("bloomerctl "+operation, flag.ContinueOnError)
	exe, err := os.Executable()
	if err != nil {
		return fail(err)
	}
	directory := flags.String("package-dir", filepath.Dir(exe), "directory containing release manifest and files")
	port := flags.Int("https-port", 8443, "private Tailscale HTTPS port")
	plan := flags.Bool("plan", false, "validate and show installation plan without mutations")
	backup := flags.String("backup", "", "absolute database backup path for restore")
	if err = flags.Parse(os.Args[2:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected positional arguments")
		return 2
	}
	if *plan && operation != "install" && operation != "upgrade" {
		return fail(fmt.Errorf("--plan only applies to install and upgrade"))
	}
	if operation == "verify" {
		m, err := release.Verify(*directory)
		if err != nil {
			return fail(err)
		}
		return output(m)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fail(err)
	}
	m := management.New(home)
	action := func() (management.Result, error) {
		switch operation {
		case "install", "upgrade":
			return m.Install(*directory, *port, *plan)
		case "start":
			_, err := m.Start()
			return management.Result{"state": "running"}, err
		case "stop":
			err := m.Stop(true)
			return management.Result{"state": "stopped"}, err
		case "status":
			return m.Status()
		case "health":
			return m.Health()
		case "restore":
			return m.Restore(*backup)
		default:
			return nil, fmt.Errorf("unknown management command: %s", operation)
		}
	}
	// Status and installation plans must not create directories or lock files.
	var result management.Result
	if operation == "status" || operation == "health" || *plan {
		result, err = action()
	} else {
		switch operation {
		case "install", "upgrade", "start", "stop", "restore":
			result, err = m.WithLock(action)
		default:
			return fail(fmt.Errorf("unknown management command: %s", operation))
		}
	}
	if err != nil {
		return fail(err)
	}
	return output(result)
}
func output(value any) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(value); err != nil {
		return fail(err)
	}
	return 0
}
func fail(err error) int { fmt.Fprintln(os.Stderr, "Bloomer management failed:", err); return 1 }
