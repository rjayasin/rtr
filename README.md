# rtr

[![CI](https://github.com/rjayasin/rtr/actions/workflows/ci.yml/badge.svg)](https://github.com/rjayasin/rtr/actions/workflows/ci.yml)

```text
 remote /srv/files                                │ local: ~/Downloads                          
                                                  │                                             
   [ ]           backups/                         │             project-backup/                 
   [x]     5.7G  ubuntu-24.04.2-desktop-amd64.iso │     248.0K  report-final.pdf                
 ➤ [x]     4.2G  screen-recording.mp4             │       1.2M  screenshot.png                  
                                                                                                
                             ╭────────────────────────────────────╮
                             │ Download 2 items • 9.9G            │
                             │ ubuntu-24.04.2-desktop-amd64.iso   │
                             │ screen-recording.mp4               │
                             │                                    │
                             │ Save to:                           │
                             │ ~/Downloads                        │
                             │                                    │
                             │ enter start • esc cancel           │
                             ╰────────────────────────────────────╯
                                                                                                
 2 selected                                                                            rtr — nas
 ───────────────────────────────────────────────────────────────────────────────────────────────
 transfers (2 active)
   ↓ archlinux-2024.04.01-x86_64.iso  ████████░░░░░  62%   18MB/s ETA 0:42
   ↑ site-backup.tar.zst              ███░░░░░░░░░░  24%  9.1MB/s ETA 1:55
 ↑/↓ move • → open • ← up • x/space select • / search • l local • enter download • t/n sort:newest • . hidden • a all • c clear • r refresh • esc back • ~ compare • tab panes
```

A terminal UI for moving files over SSH. Bookmark hosts, browse them over SFTP,
and pull files down or push them back up with `rsync` (the command and its flags
are configurable). Transfers run in the background while you keep browsing.

## Install

`rsync` and `ssh` must be on your `PATH`.

Download a prebuilt binary for your OS/arch from the
[latest release](https://github.com/rjayasin/rtr/releases/latest), or build from
source (requires Go 1.25+):

```sh
go install github.com/rjayasin/rtr@latest
# or, from a clone:
make build && ./rtr
```

## Usage

```sh
rtr                      # launch the TUI
rtr config               # open the config file in $EDITOR (creating it if needed)
rtr --config-path        # print where the config lives
rtr update               # update to the latest release
rtr version              # print the version
```

### Keys

| Screen     | Keys |
|------------|------|
| Bookmarks  | `↑/↓` move<br>`enter` connect<br>`n` new<br>`e` edit<br>`d` delete<br>`tab` focus transfers<br>`q` quit |
| Browser    | `↑/↓` move<br>`→` open dir<br>`←` up<br>`x`/`space` select<br>`a` all<br>`c` clear<br>`/` search<br>`l` toggle local pane<br>`~` toggle compare<br>`t` sort by time (toggle newest/oldest)<br>`n` sort by name (toggle A→Z/Z→A)<br>`.` toggle hidden files<br>`enter` download<br>`tab` switch pane<br>`r` refresh<br>`esc` disconnect (or clear filter) |
| Search (`/`) | type to filter by name (case-insensitive, matches anywhere)<br>`enter` accept and return to the list<br>`esc` clear |
| Compare (`~`) | with the local pane open, dims files present in **both** panes and sinks them to the bottom of each pane (unique files stay on top); each group still follows the pane's sort order |
| Transfers (`tab`) | `↑/↓` select<br>`c` cancel highlighted<br>`x` clear finished<br>`tab`/`esc` back |

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
make          # compile and launch rtr
make test     # run the test suite
make vet      # run go vet
make fmt      # format all Go sources
```

## Why does this project exist
I wanted the ease of navigation you get from an SFTP browser with the speed of rsync. 

## License

MIT — see [LICENSE](LICENSE).
