# rtr

[![CI](https://github.com/rjayasin/rtr/actions/workflows/ci.yml/badge.svg)](https://github.com/rjayasin/rtr/actions/workflows/ci.yml)

A terminal UI download client. Bookmark remote SSH hosts, browse their
directories over SFTP, pick files or folders, and pull them down with `rsync`.
Downloads run in the background while you keep browsing, with progress bars
stacked at the bottom of the window.

`rtr` does **remote → local** transfers — it pulls a remote source down to the
machine you run it on.

## Install

Requires Go 1.23+, plus `rsync` and `ssh` on your `PATH`.

```sh
go install github.com/rjayasin/rtr@latest
# or, from a clone:
go build -o rtr . && ./rtr
```

## Usage

```sh
rtr                      # launch the TUI
rtr config               # open the config file in $EDITOR (creating it if needed)
rtr --config ./my.toml   # use a specific config file
rtr --config-path        # print where the config lives
```

### Keys

| Screen     | Keys |
|------------|------|
| Bookmarks  | `↑/↓` move · `enter` connect · `n` new · `e` edit · `d` delete · `tab` focus transfers · `q` quit |
| Form       | `tab`/`↑↓` change field · `enter` save · `esc` cancel |
| Browser    | `↑/↓` move · `→` open dir · `←` up · `x`/`space` select · `a` all · `c` clear · `/` search · `t` sort by time (toggle newest/oldest) · `n` sort by name (toggle A→Z/Z→A) · `enter` download · `tab` focus transfers · `r` refresh · `esc` disconnect (or clear filter) |
| Search (`/`) | type to filter by name (case-insensitive, matches anywhere) · `enter` accept and return to the list · `esc` clear |
| Disconnect prompt | `←/→` select Yes/No · `enter` confirm · `y` disconnect · `n`/`esc` stay connected |
| Transfers (`tab`) | `↑/↓` select · `c` cancel highlighted · `x` clear finished · `tab`/`esc` back |
| Download popover | `enter` start (in background) · `esc` cancel |

In-progress downloads are recorded in `transfers.json` (beside the config) and
resumed on the next launch if you quit or rtr is interrupted.

## Configuration

Config lives at `$XDG_CONFIG_HOME/rtr/config.toml` (default
`~/.config/rtr/config.toml`) and is created on first run. Bookmarks added
through the UI are written back to it.

```toml
[rsync]
  binary = "rsync"
  flags  = ["-a", "-z", "--partial", "--human-readable"]
  extra_args = ["--exclude", ".DS_Store"]   # appended verbatim

[[bookmarks]]
  name        = "nas"
  user        = "me"
  host        = "nas.local"
  port        = 2222
  remote_path = "/volume1/media"            # starting directory when browsing
  identity    = "~/.ssh/id_ed25519"         # optional
  jump_host   = "me@bastion:22"             # optional ProxyJump

[[bookmarks]]
  name      = "box"
  ssh_alias = "box"   # inherit HostName/User/Port/IdentityFile from ~/.ssh/config
```

Auth prefers `ssh-agent`, then the bookmark's identity file, then the usual
`~/.ssh/id_{ed25519,ecdsa,rsa}`. Host keys are checked against
`~/.ssh/known_hosts`: unknown hosts are trusted on first use, changed keys are
rejected.

## Development

```sh
go test ./...
go vet ./...
gofmt -l .
```

Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea),
[pkg/sftp](https://github.com/pkg/sftp), and `golang.org/x/crypto/ssh`.

## License

MIT — see [LICENSE](LICENSE).
