# sshm

A terminal picker for the servers you actually use. Type a few letters, hit
Enter, you are in. No remembering IPs, no hunting for which folder the `.pem`
ended up in.

```
sshm                                                                    5 host()s
›  search servers, #tag to filter

● ▸prod-api            ubuntu@10.0.4.21          aws prod          30m ago
·  home-nas            192.168.1.10              home              3d ago
◌  bastion             ops@jump.example.com      prod
×  prod-db             postgres@10.0.4.30        aws db prod
·  staging-api         ubuntu@staging.example…   staging

────────────────────────────────────────────────────────────────────────────
Main API server
ssh -p 2222 -i ~/.ssh/keys/prod.pem -o IdentitiesOnly=yes ubuntu@10.0.4.21
reachable in 23ms · used 2 times, 30m ago

enter connect · ^n new · ^e edit · ^r recheck · ^g help · esc quit
```

## Why it is fast

Three decisions do most of the work:

**It opens in the terminal you are already in.** `sshm shell-init` binds a key
in your shell, so the picker takes over the current terminal and hands it back
when you are done. No new window, no tmux popup, no shell startup cost.

**It replaces itself with ssh.** When you press Enter, sshm restores the
terminal and `execve`s into `ssh`. There is no wrapper process holding your tty
for the next three days, no extra layer for Ctrl-C or window resizes to cross,
and ssh's exit status is yours. When the session ends you are back at your
prompt exactly as if you had typed the command.

**Nothing slow happens at startup.** The host list is one small file read.
Reachability checks run in the background, only for the rows on screen, and
never block the first frame. Key files are stat'ed only for the host you have
selected.

Search re-ranks the entire list on every keystroke: **~1ms for 1000 hosts, with
three allocations.** For a realistic list of 50–200 servers it is well under a
tenth of that.

## Install

```sh
git clone <this repo> && cd sshm
make setup      # fetch dependencies (once, needs network)
make install    # builds and installs to $(go env GOPATH)/bin
```

Make sure `$(go env GOPATH)/bin` is on your `PATH`.

## Quick start

```sh
sshm import                      # pull in everything from ~/.ssh/config
eval "$(sshm shell-init zsh)"    # add this line to ~/.zshrc
```

Then press **Ctrl+S** in any terminal.

`shell-init` also supports `bash`, `fish` and `tmux`, and takes `--key` to bind
something other than Ctrl+S:

```sh
sshm shell-init bash --key '^G'
```

> Ctrl+S is XOFF on most terminals, which freezes the screen. The generated
> snippet runs `stty -ixon` for you when you bind Ctrl+S or Ctrl+Q.

## Using it

Just type. The query fuzzy-matches against the alias, hostname, tags, user and
note, weighted in that order, so `papi` finds `prod-api` and `192.1` finds your
NAS. Matched characters are underlined so you can see why a row is in the list.

| Query      | Means                                     |
|------------|-------------------------------------------|
| `prod api` | fuzzy match on both words                 |
| `#aws`     | only hosts tagged `aws`                   |
| `!#staging`| hide hosts tagged `staging`               |
| `#aws #db api` | tagged `aws` **and** `db`, fuzzy `api` |

| Key | Action |
|-----|--------|
| `enter` | connect |
| `↑ ↓` or `^k ^j` | move |
| `esc` | clear the search, or quit when it is already empty |
| `^n` | add a host |
| `^e` | edit the selected host |
| `^x` | delete the selected host |
| `^y` | copy the ssh command to the clipboard |
| `^r` | re-check reachability |
| `^g` | help |

Commands are on Ctrl rather than plain letters because the search box is always
live — a picker whose list swallows `a` would be useless. They avoid Alt on
purpose: macOS terminals send Option as a compose key by default, so Alt
bindings silently do nothing there.

The list is ordered by *frecency* when you have not typed anything: a decaying
score that combines how often and how recently you connect, with a one-week
half-life. The three boxes you live in float to the top; the one you touched
once last quarter sinks. Once you start typing, match quality leads and history
only breaks ties.

The dot on the left is a TCP connect to the ssh port — `●` open, `×` refused or
timed out, `◌` checking, `·` not checked yet. It is a plain TCP probe, not an
SSH handshake, so it needs no credentials, touches no agent and cannot lock an
account. Only the visible rows are probed, so this stays cheap with hundreds of
hosts.

## Non-interactive use

```sh
sshm connect prod-api                       # skip the UI entirely
sshm connect prod-api -- -L 8080:localhost:80   # anything after -- goes to ssh
sshm which prod-api                         # print the command, don't run it
sshm list --json                            # for scripting
sshm list --tag prod
sshm import --from ~/.ssh/work_config --dry-run
sshm path                                   # where config and history live
```

`connect` accepts a fuzzy query, but only runs when it resolves to exactly one
host. If more than one matches it lists them and stops — opening a session to
the wrong server is a much worse outcome than one extra keystroke.

## Configuration

Hosts live in `~/.config/sshm/hosts.toml` (or `$XDG_CONFIG_HOME/sshm/`). It is
plain TOML, written with `0600` permissions, meant to be edited by hand and
committed to a dotfiles repo:

```toml
version = 1

[[host]]
alias = "prod-api"
hostname = "10.0.4.21"
user = "ubuntu"
port = 2222
identity_file = "~/.ssh/keys/prod.pem"
tags = ["aws", "prod"]
description = "Main API server"
options = ["ServerAliveInterval=30"]

[[host]]
alias = "bastion"
hostname = "jump.example.com"
user = "ops"
tags = ["prod"]
```

Connection history is separate, in `~/.local/state/sshm/history.json`. It is
machine-local and disposable — do not commit it. Both paths can be overridden
with `SSHM_CONFIG` and `SSHM_HISTORY`, which is handy for keeping a work profile
apart from a personal one.

Every write goes through a temp file, `fsync`, and an atomic rename, so an
interrupted save can never leave you with a truncated host list.

sshm reads `~/.ssh/config` on import but **never writes to it**. That file is
often hand-maintained or generated by other tools, and it stays the source of
truth for plain `ssh`.

### What import brings over

`Host` blocks with a literal name, and their `HostName`, `User`, `Port`,
`IdentityFile` and `ProxyJump`. A handful of behavioural directives
(`ServerAliveInterval`, `ForwardAgent`, `ControlMaster`, …) come across as `-o`
flags. `Include` is followed, including globs.

Deliberately skipped: `Host *` and other wildcard patterns (they are defaults,
not servers), `Match` blocks (they are conditional, and importing them
unconditionally would produce wrong settings), and everything else — importing
`LocalForward` or `SendEnv` blindly produces surprising sessions. Anything that
would not make a usable connection is reported rather than imported.

## Development

```sh
make check      # vet, gofmt, race tests, and a cold-start measurement
make test
make bench
```

Layout:

| Package | Responsibility |
|---------|----------------|
| `internal/model` | the `Host` type, validation, ssh argv construction |
| `internal/store` | `hosts.toml` — load, mutate, atomic save |
| `internal/frecency` | usage history and the decaying score |
| `internal/fuzzy` | the subsequence matcher and its scoring |
| `internal/search` | query parsing, filtering, ranking |
| `internal/probe` | bounded-concurrency TCP reachability with a TTL cache |
| `internal/sshconf` | `~/.ssh/config` reader |
| `internal/launch` | `execve` into ssh |
| `internal/shellinit` | the shell key-binding snippets |
| `internal/ui` | the Bubble Tea front end |
| `cmd/sshm` | CLI |

Everything below `internal/ui` is free of UI dependencies and unit tested on its
own. Dependencies are three: `bubbletea`, `lipgloss` and `BurntSushi/toml`. The
search box and the add/edit form are hand-rolled rather than pulled from
`bubbles`, because the behaviour needed is small and fully specified.

## Not built yet

Ideas that were considered and left out of the first version, roughly in the
order they would earn their place:

- **Port-forward presets** — saved `-L`/`-R` tunnels per host, launched from the
  picker. Probably the most requested next feature for anyone doing local dev
  against remote services.
- **`scp`/`rsync` mode** — pick a host, copy a file, without retyping the
  target.
- **Groups / bulk actions** — run one command across every host with a tag.
- **Last-exit memory** — remember that a host refused the key last time and say
  so in the detail pane.
- **Key discovery** — scan `~/.ssh` and suggest which key belongs to which host
  on import.
- **Sync** — the config file is already git-friendly, so this may just be
  documentation rather than code.
