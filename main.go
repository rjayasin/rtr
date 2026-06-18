// Command rtr is a terminal UI for browsing bookmarked remote SSH hosts and
// pulling files/directories down to the local machine via rsync, with live
// progress. See README.md for details.
//
// Usage:
//
//	rtr                  launch the TUI
//	rtr config           open the config file in $EDITOR
//	rtr update           update rtr to the latest release
//	rtr version          print the version
//	rtr --config-path    print the config file path
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/rjayasin/rtr/internal/config"
	"github.com/rjayasin/rtr/internal/ui"
	"github.com/rjayasin/rtr/internal/update"
)

// version is the build version, overridden via -ldflags "-X main.version=...".
// It stays "dev" for plain `go build`/`go run` source builds.
var version = "dev"

func main() {
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "config":
			runConfig(args[1:])
			return
		case "update":
			runUpdate()
			return
		case "version", "--version", "-v":
			fmt.Printf("rtr %s\n", version)
			return
		}
	}
	runTUI(args)
}

// runUpdate implements `rtr update`: fetch the latest release and replace the
// running binary in place if it is newer, printing progress as it goes.
func runUpdate() {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	fmt.Printf("rtr %s — checking for updates…\n", version)
	v, updated, err := update.Apply(ctx, version, func(msg string) {
		fmt.Printf("  • %s\n", msg)
	})
	if err != nil {
		fail(err)
	}
	if !updated {
		fmt.Printf("Already up to date (%s).\n", v)
		return
	}
	fmt.Printf("✓ Updated %s → %s. Restart rtr to use the new version.\n", version, v)
}

func runTUI(args []string) {
	fs := flag.NewFlagSet("rtr", flag.ExitOnError)
	configPath := fs.String("config", "", "path to config file (default: $XDG_CONFIG_HOME/rtr/config.toml)")
	showPath := fs.Bool("config-path", false, "print the config file path and exit")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr,
			"Usage:\n"+
				"  rtr [flags]   launch the TUI\n"+
				"  rtr config    open the config file in $EDITOR\n"+
				"  rtr update    update rtr to the latest release\n"+
				"  rtr version   print the version\n\n"+
				"Flags:\n")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)

	if *showPath {
		fmt.Println(resolvePath(*configPath))
		return
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		fail(err)
	}
	if err := ui.Run(cfg, version); err != nil {
		fail(err)
	}
}

// runConfig implements `rtr config`: ensure the config file exists (creating it
// with defaults if needed), then open it in the user's editor.
func runConfig(args []string) {
	fs := flag.NewFlagSet("rtr config", flag.ExitOnError)
	configPath := fs.String("config", "", "path to config file (default: $XDG_CONFIG_HOME/rtr/config.toml)")
	_ = fs.Parse(args)

	cfg, err := config.Load(*configPath)
	if err != nil {
		fail(err)
	}
	if err := openInEditor(cfg.Path()); err != nil {
		fail(err)
	}
}

func resolvePath(p string) string {
	if p != "" {
		return p
	}
	dp, err := config.DefaultPath()
	if err != nil {
		fail(err)
	}
	return dp
}

// editorCommand returns the editor argv, honoring $EDITOR, then $VISUAL, then vi.
// Multi-word values (e.g. "code -w") are split into command and arguments.
func editorCommand() []string {
	for _, v := range []string{os.Getenv("EDITOR"), os.Getenv("VISUAL"), "vi"} {
		if fields := strings.Fields(v); len(fields) > 0 {
			return fields
		}
	}
	return []string{"vi"}
}

func openInEditor(path string) error {
	argv := editorCommand()
	args := append(append([]string{}, argv[1:]...), path)
	cmd := exec.Command(argv[0], args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "rtr:", err)
	os.Exit(1)
}
