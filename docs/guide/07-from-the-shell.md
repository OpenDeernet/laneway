# 7. From the shell

**In this chapter:** use laneway without the board: in scripts, in your
prompt, in the background. And what happens when the connection drops.

## Scripts

Four commands, no board:

```sh
laneway list                                   # your open issues
laneway list -jql 'project = ABC ORDER BY rank' -format csv
laneway view ABC-12                            # one issue; -format json
laneway create -project ABC -type Bug -summary 'Login fails'   # type: Task by default
laneway move ABC-12 'In Progress'              # any status it can move to
laneway move ABC-12                            # where it can move to
```

`-format json` suits `jq`; `-site club` picks another site. Flags go before
the key.

> **Try it:** `laneway list -format json | jq -r '.[].key'`.

On a laneway-server site, `laneway token` lists, makes and revokes your
API tokens, and `laneway apps` the apps you signed in with:

```sh
laneway token create -days 30 'CI pipeline'   # the secret, printed once
laneway token list
laneway apps revoke 7
```

See [Tokens](../reference/cli.md#tokens).

## Completion

```sh
source <(laneway completion bash)          # in ~/.bashrc; or zsh
laneway completion fish > ~/.config/fish/completions/laneway.fish
```

It completes commands, flags, site names and, for `view` and `move`, the
keys on the boards laneway last loaded.

## Your prompt

`laneway prompt` prints the branch's issue, its status, the timer and the
inbox count: `ABC-12 · In review · ⏱ 1h 20m · ✉ 3`. It reads laneway's
state file only, never Jira, so it is fast enough for every prompt.

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

## In the background

`laneway web` can start when you log in: tick it on the setup page or
under Settings › App (chapter 9). Your rules run inside it, so they keep
watching while the browser tab is closed.

`laneway rules watch` runs your `watch:` rules without any app, printing
what fires: leave it in a tmux pane. Chapter 8 shows how to write them.

## When the connection drops

laneway keeps working.

- **Writing.** A change that never reached Jira waits in a queue and goes
  out by itself once Jira answers, oldest first. If the issue changed in
  Jira meanwhile, it waits for you to send it anyway or drop it. Anything
  that may have reached Jira fails as before, so nothing is sent twice.
- **Typing.** A comment or description you're writing is kept as a draft;
  after a crash, opening the editor on the issue brings it back.

=== "Terminal"

    The queue shows as `⇡3` in the header; the palette's *queue* row sends
    or drops a waiting change.

    Every issue laneway reads is also kept in a local index. Offline, the
    palette, the panel and simple `Q` searches answer from it and say so.
    It keeps the project's people as well, so `@` mentions and the
    assignee picker work offline too. `laneway index` shows what it holds,
    `laneway index clear` empties it.

=== "Browser"

    The queue shows as a chip with a count in the bar on top; a click sends
    or drops a waiting change. The `laneway web` server must still be
    running: reads come from it, and there is no local index.

## Recap

- `list`, `view`, `create`, `move` for scripts; `completion` for your
  shell.
- `laneway prompt` for the prompt or tmux; `laneway web` at login, or
  `rules watch`, in the background.
- Offline: writes wait in a queue; in the terminal, reads come from the
  index.

Previous: [Work on an issue](06-work-on-an-issue.md) · Next:
[Make it yours](08-make-it-yours.md)
