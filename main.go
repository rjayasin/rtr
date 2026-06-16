// Command rtr is a terminal UI for browsing bookmarked remote SSH hosts and
// pulling files/directories down to the local machine via rsync, with live
// progress. See README.md for details.
//
// Usage:
//
//	rtr                  launch the TUI
//	rtr config           open the config file in $EDITOR
//	rtr --config-path    print the config file path
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/rjayasin/rtr/internal/config"
	"github.com/rjayasin/rtr/internal/ui"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "config" {
		runConfig(args[1:])
		return
	}
	runTUI(args)
}

func runTUI(args []string) {
	fs := flag.NewFlagSet("rtr", flag.ExitOnError)
	configPath := fs.String("config", "", "path to config file (default: $XDG_CONFIG_HOME/rtr/config.toml)")
	showPath := fs.Bool("config-path", false, "print the config file path and exit")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr,
			"Usage:\n"+
				"  rtr [flags]   launch the TUI\n"+
				"  rtr config    open the config file in $EDITOR\n\n"+
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
	if err := ui.Run(cfg); err != nil {
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
