# Browser keys

Every key of `laneway web`, by view, as bound by default. `?` lists the
keys you can press right now; the key bar at the bottom shows the main
ones, and a click presses one. Keys for the terminal:
[Terminal keys](terminal-keys.md).

Where a key differs from the terminal's for the same action, the
terminal's is in the last column.

Typing while no field has the focus doesn't run a string of shortcuts.
Three keys in quick succession, not all the same, read as typing (also
when the first ones opened a picker the rest landed in): laneway takes
back what the first keys did (a dialog, a panel, a view they opened),
pauses the single keys until you stop typing (or press `esc`), and offers
to type the text into the field you used last. Keys that change
something (move a card, copy, open in Jira) wait a fifth of a second for
this. Settings › Keyboard › Single-key shortcuts turns every key without
a modifier off, for speech input; the palette (`ctrl+k`) still runs them.

## Everywhere

| Key | Does | Terminal |
| --- | --- | --- |
| `?` | the keys you can press now | |
| `:` `ctrl+k` | the palette: `:` commands, `/` search issues, `#` JQL, `g ` jump to a key | |
| `/` | search issues | |
| `g g` | go to an issue by key or pasted link | `#` |
| `Q` | JQL, with completion; a hit opens beside the rest, shown as a view (`ctrl+enter`: the view alone) | |
| `n` | new issue | |
| `alt+p` | switch project: its last board, or the view's project where it has one; starred ones first, `ctrl+s` or ☆ stars one | `p` |
| `g` then a letter | go to a view, see below | |
| `M` | every view (More) | |
| `tab` | focus between the view and the panel | |
| `esc` | close the panel | |
| `@` | switch or add a Jira site | |
| `T`, `w` | timer, log work | |
| `u` | undo the last edit | |
| `S`, `alt+s` | start work; another agent in the worktree | |
| `ctrl+y` | copy a branch name | |
| `ctrl+\` | type into the issue's agent | |
| `ctrl+a` | ask `ui.llm` about the issue (on the board: mark all) | |
| `ctrl+e` | refine | |
| `g t` | next theme | |
| `g c` | focus the view bar | |

| View | Key | Terminal |
| --- | --- | --- |
| Home | `g h` | `~` |
| Board | `g b` | |
| Planning | `g p` | `P` |
| Reports | `g r` | `C` |
| Roadmap | `g m` | `R` |
| My work | `g w`, `O` | `O` |
| Worklogs | `W` | `W` |
| Inbox | `g i`, `I` | `I` |
| Standup | `g s`, `U` | `U` |
| Agents | `g a`, `ctrl+g` | `ctrl+g` |
| Merge requests | `g M` | `alt+m` |
| Rules | `g l` | |
| Settings | `g ,`, `,` | `,` |

## Board

| Key | Does | Terminal |
| --- | --- | --- |
| `j` `k` `h` `l`, arrows | move | |
| `Home` `End`, `PgUp` `PgDn` | first, last in the lane, a page | `g` `G` |
| `enter` | open the panel | |
| `f`, `F` | filter, filter builder | `/`, `F` |
| `m`, `A` | mine, assignee filter | `m`, `a` |
| `1`–`9`, `0` | quick filters, clear | |
| `B` | board | `b` |
| `v`, `[` `]` | pick a view, previous, next | `[` `]` |
| `t` | lanes or list | |
| `O` | lanes: swimlanes; list: sort | `s` |
| `C` | list columns | |
| `z` `Z` | fold the band (or the stacked lane's section), unfold all | |
| `{` `}` | previous, next band; in a grouped list, group | |
| `c` | one-line cards | |
| `alt+e` | empty lanes | |
| `alt+l` | next lane layout | |
| `alt+t` | time machine: `←` `→` a day, `esc` back | `ctrl+t` |
| `alt+o` | closed sprints | `ctrl+o` |
| `V` | releases | |
| `r` | refresh | |

### Cards

| Key | Does | Terminal |
| --- | --- | --- |
| `s` `p` `P` | status, priority, points | panel only |
| `e` `a` | summary, assignee | panel only |
| `H` `L` | move a lane | |
| `K` `J`, `alt+k` `alt+j` | rank, to the top, bottom | |
| `M` | to a sprint or the backlog | |
| `E` | quick edit | `e` |
| `x`, `ctrl+a` | mark, mark the lane or every row | `x`, `X` |
| `X` | edit the marked | `B` |
| `.` | repeat the last change | |
| `n` | new issue, in the shown sprint | |
| `*` | pin | |
| `o` | open in Jira | |
| `y` `Y` `ctrl+y` | copy key (with marks: a markdown table), link, branch | |

## Panel

The panel owns the keys while it has the focus (its edge lights up); digits
are its tabs.

| Key | Does | Terminal |
| --- | --- | --- |
| `1` `2` `3` `4` | Details, Comments, History (again: work log, all), Terminal | `[` `]` |
| `j` `k` | next, previous comment; else scroll | `}` `{` |
| `ctrl+d` `ctrl+u` | half a page | |
| `s` `p` `P` | status, priority, points | |
| `e` `E` | summary (or your selected comment), description | |
| `l` `a` | labels, assignee | |
| `c` | comment; `ctrl+enter` posts, `ctrl+o` sets who sees it | |
| `R` | reply to the selected or last comment | |
| `d` | delete your selected comment | `delete` twice |
| `G` | go to a link, subtask, child or web link | `L` |
| `L` | add a link to an issue | `A` |
| `A` | more actions | |
| `D` | development: `j` `k` rows, `enter` opens or unfolds a merge request, `d` its diff | |
| `N` | private notes, edited in place | `$EDITOR` |
| `[` `]` | previous, next issue of the list you came from | |
| `backspace` | back along the trail | |
| `i` | images | |
| `/`, `n` `N` | find in the issue | |
| `<` `>` | widen, narrow the panel | |
| `*` `o` `y` `Y` | pin, Jira, copy key, link | |
| `ctrl+\` | its agent's terminal; `t` takes over | |
| `r` | refresh | |

Fields have no key walk: click one to edit it.

## Planning

| Key | Does | Terminal |
| --- | --- | --- |
| `j` `k` | move | |
| `x` `space` | select; on a header, fold | `x` |
| `m` | to a sprint or the backlog | `M` |
| `J` `K` | rank | |
| `[` `]` | another sprint | |
| `\|` | split: a sprint beside the list; `>` moves there, `tab` `h` `l` switch side | |
| `z`, `enter` | fold the section, open | |
| `e` `X` | quick edit, edit the selected | `e` `B` |
| `P` `a` | points, assignee | |
| `N` | new sprint | |
| `Z` | start the sprint | `S` |
| `C` | complete it | |
| `E` | name, goal and end in one form | `R`, `E` |
| `f` `F` `A` | filter, builder, assignee | `/` `F` |
| `O` | sort | |
| `y` | copy the section as a markdown table | |
| `R` | reload | |

## Reports

| Key | Does |
| --- | --- |
| `1`–`7` | burndown, burnup, cumulative flow, velocity, cycle time, retro, releases |
| `h` `l`, `[` `]` | previous, next report |
| `s` | pick a sprint |
| `W` | cycle-time window |
| `d` | the column done counts from (`ui.report_done`) |
| `c` | a second line beside done, for the visit; again removes it |
| `j` `k`, `enter` | items, open (a release: its issues on the board) |
| `r` | release the version |
| `y` | copy the numbers as a markdown table |
| `R` | reload |

## Roadmap

| Key | Does | Terminal |
| --- | --- | --- |
| `h` `l`, `+` `-`, `.` | scroll, zoom, today | |
| `space`, `enter` | fold an epic's issues, open | |
| `H` `L`, `<` `>` | move the bar, its end | |
| `e` | grip its start, its end, let go | |
| `E` | quick edit | |
| `f`, `F` | the epic's issues on the board, filter | `f`, `/` |
| `n` | new epic | |
| `y` `Y` | copy a markdown table, link | |
| `R` | reload | |

## My work and worklogs

| Key | Does | Terminal |
| --- | --- | --- |
| `1` `2` `3` | issues, day, week | `O`, `W`, `W` |
| `W` | day or week | |
| `h` `l`, `0` | a day or week back, forward, today | `[` `]` |
| `e`, `d` | edit, delete an entry (the marked ones) | |
| `x`, `X` | mark an entry, every entry | |
| `m` | move an entry (the marked ones) to another issue | |
| `p` | propose missing worklogs | |
| `a` | log work | |
| `enter` | open; log a proposal; log on a week's cell | |
| `+` | add an issue's row to the week | `#` |
| `y` | copy as a markdown table | |
| `v`, `d` | issues: group by, hide done | |

## Inbox

| Key | Does |
| --- | --- |
| `1` `2` `3`, `tab` | Inbox, Mentions, All |
| `e`, `E` | done until news, every read thread done |
| `s` | snooze till the next workday |
| `u`, `a` | unread, read or unread |
| `c` `R` | comment, reply |
| `enter` `o` `y` | open, Jira, copy key |
| `r` | refresh |

## Standup

| Key | Does | Terminal |
| --- | --- | --- |
| `h` `l` | previous, next person | `←` `→` |
| `A` | who takes part | `a` |
| `s` | shuffle | |
| `space` | the timer | |
| `P` | park the card | |
| `z` | Off the board | |
| `[` `]` | a workday back, forward | |
| `enter` `y` `r` | open, copy, refresh | |

## Agents

| Key | Does |
| --- | --- |
| `enter` `ctrl+\` | type into the terminal; `ctrl+\` back |
| `z` | full size, `esc` back |
| `t` | take over input from another attach |
| `tab` | worktrees without an agent too |
| `v` `o` | the issue, Jira |
| `p`, `d` twice | prompt, stop |
| `N`, `S` | another agent in its worktree, start work |
| `f` | focus it in herdr |

In the terminal every key goes to the agent except `ctrl+\`,
`ctrl+shift+c` (copy) and `ctrl+shift+v` (paste).

## Merge requests

The list (`g M`): `enter` opens the page, `d` its changes, `i` the Jira
issue beside, `o` GitLab.

The page:

| Key | Does |
| --- | --- |
| `1` `2` | Overview, Changes |
| `j` `k`, `]` `[` | next, previous file |
| `n` `N` | next, previous thread |
| `e` | edit it; on Changes the whole file |
| `z` `Z` | fold the file, all |
| `v` | an earlier push |
| `S` | submit the review |
| `A` | approve |
| `M` | merge |
| `C` | agent review |
| `i` | the Jira issue beside |
| `o` `r` | GitLab, reload |

Notes take the mouse: a click on a line number notes it, `shift`+click a
range; suggest, edit, drop, reply and resolve are buttons. A click on the
title or a field (reviewers, assignees, labels, target branch) edits it.

## Rules and settings

Rules: `t` try a change, `N` browser notifications, `R` reload.

Settings: `/` filters, `enter` or `space` changes, `h` `l` the previous,
next value, `delete` resets. On a key row, `enter` captures a new key.

## Mouse and touch

- A click opens the panel; `ctrl`, `⌘` or `shift`+click marks, as does a
  card's checkbox. A double-click opens it in Jira.
- Drag a card to a lane (a lane of several statuses splits into drop zones),
  or along its lane to rank it. Drag planning rows across sections, roadmap
  bars and their ends.
- Right-click anything with an issue key: its menu. A long press on a phone
  opens it as a sheet.
- Drag the panel's left edge to resize; a double-click resets it.
- `shift`+wheel or a sideways swipe scrolls lanes and the roadmap.

## Rebinding

Settings › Keyboard: `enter` on a row captures a new key and writes it to
the config. A key with a terminal action of the same name
([list](terminal-keys.md#rebinding)) goes to `ui.keys`, so the terminal
follows; it takes one key, not a sequence. The rest go to `ui.web_keys`, by
the id `scope:default key`:

```yaml
ui:
  keys:
    project: ctrl+p
  web_keys:
    "board:C": alt+c
```

`none` unbinds a key (No key when capturing).
