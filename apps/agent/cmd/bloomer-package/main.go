// bloomer-package builds a macOS disk image using Go and Apple's built-in tools.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"bloomer/agent/internal/release"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "DMG packaging failed:", err)
		os.Exit(1)
	}
}
func command(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
func run() error {
	rootFlag := flag.String("root", "../..", "repository root")
	version := flag.String("version", release.Version, "release version")
	flag.Parse()
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return fmt.Errorf("DMG packaging requires an Apple Silicon Mac")
	}
	if !release.ValidVersion(*version) {
		return fmt.Errorf("invalid release version")
	}
	root, err := filepath.Abs(*rootFlag)
	if err != nil {
		return err
	}
	agent := filepath.Join(root, "apps/agent")
	dist := filepath.Join(agent, "dist")
	if err = os.MkdirAll(dist, 0755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(dist, ".dmg-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	volume := filepath.Join(stage, "volume")
	if err = os.Mkdir(volume, 0755); err != nil {
		return err
	}
	app := filepath.Join(volume, "安装 Bloomer.app")
	if err = command("/usr/bin/osacompile", "-o", app, filepath.Join(agent, "packaging/macos/installer.applescript")); err != nil {
		return err
	}
	plist := filepath.Join(app, "Contents/Info.plist")
	for _, entry := range [][3]string{{"CFBundleIdentifier", "string", "dev.bloomer.installer"}, {"CFBundleShortVersionString", "string", *version}, {"CFBundleVersion", "string", *version}, {"CFBundleDisplayName", "string", "安装 Bloomer"}} {
		// osacompile may include any of these keys already. Set-or-add handles both cases.
		if exec.Command("/usr/libexec/PlistBuddy", "-c", "Set :"+entry[0]+" "+entry[2], plist).Run() != nil {
			if err = command("/usr/libexec/PlistBuddy", "-c", "Add :"+entry[0]+" "+entry[1]+" "+entry[2], plist); err != nil {
				return err
			}
		}
	}
	payload := filepath.Join(app, "Contents/Resources/package")
	if err = os.MkdirAll(payload, 0755); err != nil {
		return err
	}
	for _, name := range release.Files {
		var source string
		switch name {
		case "bloomer-agent", "bloomerctl":
			source = filepath.Join(agent, "bin", name)
		case "openapi.yaml":
			source = filepath.Join(root, "contracts", name)
		case "INSTALL.md":
			source = filepath.Join(root, "docs/install.md")
		default:
			source = filepath.Join(agent, "packaging/macos", name)
		}
		data, e := os.ReadFile(source)
		if e != nil {
			return e
		}
		mode := os.FileMode(0644)
		if name == "bloomer-agent" || name == "bloomerctl" {
			mode = 0755
		}
		path := filepath.Join(payload, name)
		if err = os.WriteFile(path, data, mode); err != nil {
			return err
		}
		if mode == 0755 {
			if err = command("/usr/bin/codesign", "--force", "--sign", "-", "--identifier", "dev.bloomer."+name, path); err != nil {
				return err
			}
		}
	}
	b, err := exec.Command(filepath.Join(payload, "bloomer-agent"), "-version").Output()
	if err != nil {
		return err
	}
	fields := strings.Fields(string(b))
	if len(fields) < 2 || fields[1] != *version {
		return fmt.Errorf("binary version does not match release version")
	}
	ctlVersion, err := exec.Command(filepath.Join(payload, "bloomerctl"), "version").Output()
	if err != nil {
		return err
	}
	ctlFields := strings.Fields(string(ctlVersion))
	if len(ctlFields) < 2 || ctlFields[1] != *version {
		return fmt.Errorf("management binary version does not match release version")
	}
	if err = release.Write(payload, *version, strings.TrimSpace(string(b))); err != nil {
		return err
	}
	if _, err = release.Verify(payload); err != nil {
		return err
	}
	if err = command("/usr/bin/codesign", "--force", "--sign", "-", app); err != nil {
		return err
	}
	if err = command("/usr/bin/codesign", "--verify", "--deep", "--strict", app); err != nil {
		return err
	}
	readme, err := os.ReadFile(filepath.Join(root, "docs/install.md"))
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(volume, "安装说明.md"), readme, 0644); err != nil {
		return err
	}
	name := "bloomer-agent-" + *version + "-darwin-arm64.dmg"
	temporary := filepath.Join(stage, name)
	if err = command("/usr/bin/hdiutil", "create", "-volname", "Bloomer "+*version, "-srcfolder", volume, "-fs", "HFS+", "-format", "UDZO", temporary); err != nil {
		return err
	}
	if err = command("/usr/bin/hdiutil", "verify", temporary); err != nil {
		return err
	}
	output := filepath.Join(dist, name)
	if err = os.Rename(temporary, output); err != nil {
		return err
	}
	digest, err := release.Hash(output)
	if err != nil {
		return err
	}
	if err = os.WriteFile(output+".sha256", []byte(digest+"  "+name+"\n"), 0644); err != nil {
		return err
	}
	fmt.Println(output)
	return nil
}
