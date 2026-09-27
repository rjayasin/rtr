# rtr

[![CI](https://github.com/rjayasin/rtr/actions/workflows/ci.yml/badge.svg)](https://github.com/rjayasin/rtr/actions/workflows/ci.yml)

![rtr browsing a NAS with the local pane open and transfers running](docs/screenshot.png)

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
rtr --config-path        # print where the config lives
rtr update               # update to the latest release
rtr version              # print the version
```

### Keys

| Screen     | Keys |
|------------|------|
| Bookmarks  | `↑/↓` move<br>`enter` connect<br>`n` new<br>`e` edit<br>`d` delete<br>`tab` focus transfers<br>`?` toggle keyboard tips (hidden by default)<br>`q` quit |
| Browser    | `↑/↓` move<br>`→` open dir<br>`←` up<br>`x`/`space` select (checkboxes appear once something is selected)<br>`a` all<br>`c` clear<br>`/` search<br>`l` toggle local pane<br>`~` toggle compare<br>`t` sort by time (toggle newest/oldest)<br>`n` sort by name (toggle A→Z/Z→A)<br>`.` toggle hidden files<br>`enter` download<br>`tab`/`shift+tab` switch pane (forward/back)<br>`?` toggle keyboard tips (hidden by default)<br>`r` refresh<br>`esc` disconnect (or clear filter/selection first) |
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
make screenshot  # re-render docs/screenshot.png (fabricated data; needs Chrome)
```

### Releases

Pushing to `main` automatically cuts a release. The version bump is inferred
from [Conventional Commit](https://www.conventionalcommits.org) messages since
the last tag: `feat:` bumps the minor, `fix:` bumps the patch, and a `!` after
the type (e.g. `feat!:`) or a `BREAKING CHANGE:` footer bumps the major.
Commits without a recognized type still ship as a patch release.

## Why does this project exist
I wanted the ease of navigation you get from an SFTP browser with the speed of rsync. 

## License

MIT — see [LICENSE](LICENSE).
