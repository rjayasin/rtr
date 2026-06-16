// Command rtr is a terminal UI for browsing bookmarked remote SSH hosts and
// pulling files/directories down to the local machine via rsync, with live
// progress. See README.md for details.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/rjayasin/rtr/internal/config"
	"github.com/rjayasin/rtr/internal/ui"
)

func main() {
	configPath := flag.String("config", "", "path to config file (default: $XDG_CONFIG_HOME/rtr/config.toml)")
	showPath := flag.Bool("config-path", false, "print the config file path and exit")
	flag.Parse()

	if *showPath {
		p := *configPath
		if p == "" {
			var err error
			if p, err = config.DefaultPath(); err != nil {
				fail(err)
			}
		}
		fmt.Println(p)
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

func fail(err error) {
	fmt.Fprintln(os.Stderr, "rtr:", err)
	os.Exit(1)
}
