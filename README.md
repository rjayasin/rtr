# rtr

[![CI](https://github.com/rjayasin/rtr/actions/workflows/ci.yml/badge.svg)](https://github.com/rjayasin/rtr/actions/workflows/ci.yml)

```text
 remote  /volume1/media

    [ ]           Movies/
 ➤  [x]     4.7G  Interstellar.2014.mkv
    [x]     1.2G  Arrival.2016.mkv
    [ ]   248.6M  trailer.mp4
    [ ]    12.4K  notes.txt

 2 selected                                            rtr — nas
 ──────────────────────────────────────────────────────────────
 transfers (2 active)
   ↓ Interstellar.2014.mkv  ██████████░░░░░░  62%   18MB/s ETA 0:42
   ↑ backup.tar.gz          ████░░░░░░░░░░░░  24%  9.1MB/s ETA 1:55
 ↑/↓ move • → open • enter download • l local • tab transfers
```

A terminal UI for moving files over SSH. Bookmark hosts, browse them over SFTP,
and pull files down or push them back up with `rsync` (the command and its flags
are configurable). Transfers run in the background while you keep browsing.

## Install

`rsync` and `ssh` must be on your `PATH`.

Download a prebuilt binary for your OS/arch from the
[latest release](https://github.com/rjayasin/rtr/releases/latest), or build from
source (requires Go 1.23+):

```sh
go install github.com/rjayasin/rtr@latest
# or, from a clone:
make build && ./rtr
```

## Usage

```sh
rtr                      # launch the TUI
rtr config               # open the config file in $EDITOR (creating it if needed)
rtr update               # update to the latest release
rtr version              # print the version
rtr --config-path        # print where the config lives
```

### Keys

| Screen     | Keys |
|------------|------|
| Bookmarks  | `↑/↓` move · `enter` connect · `n` new · `e` edit · `d` delete · `tab` focus transfers · `q` quit |
| Form       | `tab`/`↑↓` change field · `enter` save · `esc` cancel |
| Browser    | `↑/↓` move · `→` open dir · `←` up · `x`/`space` select · `a` all · `c` clear · `/` search · `l` local pane · `t` sort by time (toggle newest/oldest) · `n` sort by name (toggle A→Z/Z→A) · `enter` download · `tab` switch pane · `r` refresh · `esc` disconnect (or clear filter) |
| Search (`/`) | type to filter by name (case-insensitive, matches anywhere) · `enter` accept and return to the list · `esc` clear |
| Local pane (`l`) | a split view of the directory rtr was launched from · `↑/↓` move · `→` open dir · `←` up · `enter` upload to the remote dir · `/` search · `t` sort by time · `n` sort by name · `~` compare · `r` refresh · `tab` switch to remote · `l`/`esc` close |
| Compare (`~`) | with the local pane open, dims files present in **both** panes and sinks them to the bottom of each pane (unique files stay on top); each group still follows the pane's sort order |
| Disconnect prompt | `←/→` select Yes/No · `enter` confirm · `y` disconnect · `n`/`esc` stay connected |
| Transfers (`tab`) | `↑/↓` select · `c` cancel highlighted · `x` clear finished · `tab`/`esc` back |
| Transfer popover | `enter` start (in background) · `esc` cancel — opened by `enter` on the remote pane (download) or the local pane (upload) |

In-progress transfers (downloads and uploads) are recorded in `transfers.json`
(beside the config) and resumed on the next launch if you quit or rtr is
interrupted.

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

## Updating

If you installed a release binary, rtr can update itself in place:

```sh
rtr update    # fetch the latest release and replace the running binary
```

rtr also checks for a newer release at startup and shows a notice on the
bookmarks screen when one is available. Set `RTR_NO_UPDATE_CHECK=1` to disable
that check. (Source builds report version `dev` and are not auto-nagged; run
`rtr update` to move onto a published release.)

## Development

```sh
go test ./...
go vet ./...
gofmt -l .
```

## Why does this project exist
I wanted the ease of navigation you get from an SFTP browser with the speed of rsync. 

## License

MIT — see [LICENSE](LICENSE).
