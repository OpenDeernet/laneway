# Command line

```
laneway [-config FILE] [-site NAME] [-demo] [command]
```

Without a command laneway starts the terminal app. Global flags go before
the command; a command's flags go before its arguments.

| Flag | |
| --- | --- |
| `-config FILE` | another config file |
| `-site NAME` | a `sites:` entry instead of the last one picked |
| `-demo` | a generated project served in-process: nothing of yours is read or written, and what you change is gone when it ends. With `-config` only its `ui:` (and in the browser `rules:`) applies |
| `-version` | print the version |

## The apps

| Command | |
| --- | --- |
| `laneway` | the terminal app |
| `laneway web` (`serve`) | the browser app, see [In the browser](../guide/09-in-the-browser.md) |
| `laneway setup` | add a Jira site, or replace a site's email and token |
| `laneway login` | sign in to a laneway-server site through its own sign-in (`-device`: with a code on another device; over SSH it does that anyway) |
| `laneway logout` | end a laneway-server site's sign-in, on the server too |

`laneway web` flags: `-addr` (`127.0.0.1:8484`), `-remote` (allow an
address that is not loopback; needs `-cert` and `-key`), `-cert`, `-key`
(TLS certificate and key, PEM), `-no-open` (don't open the browser),
`-demo`.

## Scripts

For scripts and CI, without the board. They use `jira:` unless given
`-site`.

```sh
laneway list [-jql 'project = ABC ORDER BY rank'] [-format plain|csv|json]   # default: your open issues
laneway view [-format plain|json] ABC-12
laneway create -project ABC -summary 'Login fails' [-type Bug] [-description '…'] [-format json]   # type: Task
laneway move ABC-12 'In Progress'        # a status the issue can move to, any case
```

## Shell

| Command | |
| --- | --- |
| `laneway prompt [-format TEMPLATE]` | the git branch's issue, its status, the timer and the inbox count: `ABC-12 · In review · ⏱ 1h 20m · ✉ 3`. Reads the state file only, never Jira; a Go template over `.Key .Status .TimerKey .Timer .Inbox` |
| `laneway completion bash\|zsh\|fish` | a completion script: commands, flags, site names and, for `view` and `move`, the keys on the boards last loaded |
| `laneway hook install [-strict] [-force]` | git hooks in this repository, see below |
| `laneway index [clear]` | what the local index holds; `clear` drops it |

```sh
source <(laneway completion bash)       # in ~/.bashrc
laneway completion fish > ~/.config/fish/completions/laneway.fish
```

```toml
# starship.toml
[custom.laneway]
command = "laneway prompt"
when = true
```

```sh
# tmux.conf
set -g status-right '#(laneway prompt -format "{{.Key}} {{.Timer}}")'
```

### Git hooks

`laneway hook install` adds two hooks:

- `commit-msg` puts the branch's key before a message that names no issue
  (`issue/ABC-12-fix`: `fix it` → `ABC-12 fix it`) and refuses the commit
  when the branch names none either; merges, reverts, fixups, squashes and
  amends pass. With `-strict` it also refuses keys Jira doesn't know.
- `post-checkout`, on checking out an issue's branch while it's still to
  do, asks `move ABC-12 (To Do) to In Progress? [y/N]`.

A hook that isn't laneway's is kept unless `-force`; the hooks do nothing
where laneway isn't installed.

## Rules

| Command | |
| --- | --- |
| `laneway rules list` | the rules that loaded |
| `laneway rules test [flags]` | which rules a change fires, and what stopped the rest |
| `laneway rules watch` | poll the `watch:` rules without the board |

See [Rules](rules.md).

## Merge requests

`laneway mr note LINK [FILE:LINE] TEXT` adds a note to your pending review
of the merge request at LINK: on a line of the diff (`FILE:-LINE` a removed
one), or on the merge request as a whole. Only you see it until you submit
the review. An agent reviewing a merge request leaves its findings so.
`-config` goes after `note`.
