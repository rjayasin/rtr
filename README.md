# rtr

A terminal UI download client. Bookmark remote SSH hosts, browse their
directories over SFTP, pick files or folders, and pull them down with `rsync`.
Downloads run in the background — start as many as you like and keep browsing
while their progress bars stack at the bottom of the window.

```
rtr — nas
/volume1/media

  [ ]    812M  archive.tar.gz
▸ [ ]          movies/
  [ ]     12K  notes.txt
  [x]   1.4G   ubuntu.iso

1 selected
transfers (1 active)
ubuntu.iso             ███████████░░░░░░░░░░░  47% 3.2MB/s ETA 0:00:08
archive.tar.gz         ✓ → /Users/you/Downloads
↑/↓ move • → open • ← up • space select • d download • s sort:name • … • x clear done
```

Pressing `d` opens a destination popover *over* the file list; hit `enter` and
the download starts in the background while you keep browsing.

## Why

`rsync` over SSH is the right tool for resumable, integrity-checked transfers,
but driving it by hand means remembering hosts, typing remote paths, and staring
at a frozen terminal. `rtr` wraps that workflow in a keyboard-driven TUI: your
hosts are bookmarks, remote directories are browsable, and the rsync command it
spawns is configurable and monitored.

> **Scope:** `rtr` does **remote → local** transfers — it pulls a remote source
> down to the machine you run it on. (rsync cannot move data directly between two
> remote hosts in a single invocation.)

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

`rtr config` opens the file in `$EDITOR` (falling back to `$VISUAL`, then `vi`);
multi-word values like `EDITOR="code -w"` are honored.

### Keys

| Screen     | Keys |
|------------|------|
| Bookmarks  | `↑/↓` move · `enter` connect · `n` new · `e` edit · `d` delete · `q` quit |
| Form       | `tab`/`↑↓` change field · `enter` save · `esc` cancel |
| Browser    | `↑/↓` move · `→`/`enter` open dir · `←` up · `space` select · `a` all · `c` clear · `s` toggle sort (name ↔ time) · `d` download · `x` clear finished transfers · `r` refresh · `esc` back |
| Download popover | `enter` start (in background) · `esc` cancel |

If no items are checked, `d` downloads the entry under the cursor. Each `enter`
queues another background transfer; they run in parallel and show in the bottom
panel. `x` removes finished ones.

## Configuration

Config lives at `$XDG_CONFIG_HOME/rtr/config.toml` (default
`~/.config/rtr/config.toml`) and is created on first run. Bookmarks added through
the UI are written back to it.

```toml
default_local_dir = "/Users/you/Downloads"

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

### The rsync command

`rtr` builds the command programmatically (no shell, so paths with spaces are
safe). For each transfer it runs:

```
<binary> <flags…> --info=progress2 --no-inc-recursive <extra_args…> \
    [-e "ssh -p <port> -i <identity>"] user@host:'<remote path>' … <local dir>
```

- `--info=progress2` gives a single machine-readable overall percentage, which
  drives the progress bar.
- `--no-inc-recursive` makes rsync size the whole transfer up front so that
  percentage is meaningful from the start.
- The SSH transport (`-e …`) is only added when the bookmark needs a non-default
  port, an identity file, or a jump host; otherwise rsync uses your `ssh` and
  honors `~/.ssh/config`.

## Authentication & host keys

- Auth prefers `ssh-agent` (`SSH_AUTH_SOCK`), then the bookmark's identity file,
  then the usual `~/.ssh/id_{ed25519,ecdsa,rsa}`.
- Host keys are checked against `~/.ssh/known_hosts`. An **unknown** host is
  trusted on first use and recorded (like OpenSSH's
  `StrictHostKeyChecking=accept-new`); a **changed** key is rejected, since that
  is the dangerous case.

## Development

```sh
go test ./...     # unit tests: progress parsing, rsync arg building, config, UI flows
go vet ./...
gofmt -l .
```

Layout:

```
main.go                    entrypoint & flags
internal/config            TOML config + bookmarks
internal/sshx              SSH dial (agent/key/jump/known_hosts) + SFTP browsing
internal/transfer          rsync arg building, spawn, --info=progress2 parsing
internal/ui                Bubble Tea: bookmarks, browser (with dest popover + background transfers panel)
```

Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea),
[pkg/sftp](https://github.com/pkg/sftp), and `golang.org/x/crypto/ssh`.

## License

MIT — see [LICENSE](LICENSE).
