# 3. Editing and moving

**In this chapter:** move cards along the workflow, change fields, write
descriptions and comments, create issues, and change a dozen cards at once.
From here on laneway writes to Jira, so practise on an issue of your own.

## Move a card

On the board, `H` and `L` push the card a lane left or right. It moves on
screen at once; Jira catches up behind it. With the mouse, drag it.

- A lane holding several statuses asks which one (in the browser, a drag
  shows a drop zone per status).
- Moved the wrong card, or set the wrong priority? `u` takes back the last
  change: a move, a rank, an edit, a bulk edit or a deleted comment. `u`
  again takes back the one before.
- `K` and `J` rank a card up or down within its lane, `alt+k` and `alt+j`
  to its top or bottom. With the mouse, drag it up or down the lane.
- `M` sends it to another sprint or the backlog.
- `.` does the last move or edit again, on the card you're on.

> **Try it:** pick one of your cards in To do and press `L`. It moves to In
> progress. Then `u` to put it back.

### When the workflow wants more

Some moves come with conditions: Code review needs a reviewer, Waiting for
needs a comment. Instead of failing with Jira's message, laneway opens a
small form with exactly those fields, the required ones first and marked
`*`. Fill them in and the move goes through; if Jira still refuses, the
form stays up with its reason, so the fix is one edit away. `esc` cancels
and the card goes back.

=== "Terminal"

    `↑` `↓` pick a field, `enter` edits it, `del` clears it, `ctrl+s` makes
    the move.

=== "Browser"

    `tab` walks the fields, `ctrl+enter` makes the move.

The same form shows up when you create an issue in a project that insists
on a field, such as a Component.

## Change fields

**From the board:** a quick edit changes status, priority, assignee,
labels, points or sprint without opening the panel. Or right-click the
card: the same, as a menu at the pointer, with open (here or in a new tab),
copy and pin below.

=== "Terminal"

    `e` on a card is the quick edit.

=== "Browser"

    `E` on a card is the quick edit; `s` `p` `P` `a` `e` also edit status,
    priority, points, assignee and summary right from the board. On a phone,
    a long press opens the menu.

**From the panel:** one key per common field, the same in both.

| Key | Edits |
| --- | --- |
| `s` | status |
| `p` | priority |
| `P` | story points |
| `e` | summary |
| `l` | labels |
| `a` | assignee ("Assign to me" has the cursor) |
| `E` | the description |

Any other field, custom ones too, edits in its row: text, numbers and dates
right there, people and options in a list under it. Fields the edit screen
has beyond the usual fold under a More row. `*` on a field pins it, so it
always shows, or unpins it into More: the usual ones too, in the terminal
and the browser alike.

=== "Terminal"

    `tab` to a field, `enter` edits it.

=== "Browser"

    Click a field to edit it.

A field Jira's field configuration requires is marked `*`, as on the create
form, and won't empty: laneway refuses before sending, with what you typed
still in the editor.

> **Tip:** dates understand you: `2026-10-01`, `today`, `+3d`, `fri`. A
> date-time takes `fri 14:00`.

## Write

`E` opens the description as markdown in an editor, right where it was.
Jira's extras (panels, expands, action items, mentions, dates, statuses,
coloured text, tables) have a markdown spelling of their own; `/` at a
line's start lists them to insert, narrowed as you type (`/warn`, `/todo`,
`/green`). The [editor reference](../reference/editor.md) has all of them.

=== "Terminal"

    Bold, italic, code and links show styled as you type. `enter` is a
    newline, `ctrl+s` saves, `esc` cancels (it asks once if you changed
    something). `ctrl+e` hands the text to your own `$EDITOR`.

=== "Browser"

    Markdown draws as rich text, its markers showing only on the line with
    the caret. A toolbar formats, as do `ctrl+b` `ctrl+i` `ctrl+k`;
    `ctrl+p` previews, `ctrl+enter` saves, `esc` cancels. Paste a web page's
    text and it comes in as markdown; paste or drop a file and it attaches.

In the panel, a click on an action item's box checks it in Jira, and a
click on an expand's title opens or folds it.

What you write is kept as a draft as you type, shared by the terminal and
the browser: after a crash or a closed tab, opening the editor on that
issue brings it back.

A colleague's edit is never saved over unasked: when the description
changed in Jira since you opened it, laneway writes nothing and lets you
see theirs before you save yours over it.

## Talk

- `c` writes a comment after the thread; `R` replies to a comment, in
  Jira's thread.
- `@` and a few letters list people; `tab` inserts a mention that notifies
  them.
- `:` and two letters list emoji, the ones you use most first. In the
  description editor too.
- `ctrl+o` limits a comment to one project role or group you're in, or
  makes it an internal note in a Service Desk project.
- A post that fails keeps its text (in the terminal `c` brings it back).
  One Jira answered too late may have landed: posting it again looks for
  it among the newest comments first, so it isn't posted twice.
- Under each comment: *reply*, and on your own *edit* and *delete*, to
  click.

=== "Terminal"

    `ctrl+s` posts, `enter` starts a new line. `}` and `{` select a
    comment: `R` replies to it, `enter` edits yours, `delete` twice deletes
    it (`u` brings it back).

=== "Browser"

    `ctrl+enter` posts. On the comments tab, `j` and `k` select a comment:
    `R` replies to it, `e` edits yours, `d` deletes it.

> **Try it:** on an issue a colleague works on, press `c`, type `@` and the
> first letters of their name, `tab`, then your message. `esc` instead of
> posting if you'd rather not send it; laneway asks before dropping what
> you wrote.

## More actions

`A` in the panel holds the rest: a subtask (or a child for an epic), a link
to another issue or a web page, clone, change its type, move it to another
project, set the original estimate, time in each status, the dependency
tree, delete it, watch it or add and remove watchers, vote, flag, upload a
file, delete an attachment.
With the project in `jira.repos` it also opens a draft pull request from
the issue's branch (chapter 6).

=== "Terminal"

    `A` also attaches the image on your clipboard or a screenshot of a
    region (grim and slurp, gnome-screenshot, spectacle or screencapture),
    and downloads an attachment to `ui.download_dir`.

=== "Browser"

    `L` links to another issue directly. Drop or paste files on the panel
    to attach them; a click on an attachment opens or downloads it.

## Create an issue

`n` opens one form: the type, the summary, where the cursor waits, and the
description. The issue lands in the board's project, and in the sprint you
are looking at; the panel opens on it. Fields the type requires, like a
Component, show up as soon as the type is set. `+ more fields` shows the
others the type allows, like the assignee, labels or a due date. A refused
create keeps the form, Jira's reasons under the fields they are about.

Making several? Paste a list into the summary: one issue a line, list
markers dropped.

=== "Terminal"

    `←` `→` change the type (it starts on the one you last made). `enter`
    on the summary creates it, `ctrl+s` from anywhere. `alt+enter` creates
    it and keeps the form for the next, its type and fields as they were.

=== "Browser"

    `ctrl+enter` creates it. Tick *Create another* to keep the form for the
    next. Drop files on it to attach them. A form closed unsent, or lost to
    a reload or a closed tab, is back the next time `n` opens it.

A subtask, an epic's child, a roadmap epic and a clone open the same form,
the parent or the clone's copy filled in.

> **Tip:** give a type a starting description with `ui.templates` in the
> config, such as steps, expected and actual for every new Bug.

## Many cards at once

Mark the cards, then edit them together: status (each along its own
workflow), priority, assignee, labels, points or sprint. Before
more than one card changes, laneway asks once more. Cards that fail stay
marked, with the reason. `u` puts back what each card had, also one marked
in a view you've since left. `esc` clears the marks; marks survive
switching views.

=== "Terminal"

    1. `x` marks the card under the cursor; `X` marks the whole lane (or
       every row in the list, after `/` narrowed it).
    2. `B` edits all marked cards; `enter` again confirms. For labels,
       `ui -old` adds ui and removes old.

=== "Browser"

    1. `x` (or `ctrl`+click, or a card's checkbox) marks a card; `ctrl+a`
       marks the whole lane, or every row in the list. A bar counts the
       marked cards.
    2. `X` edits them all, the flag too, after Apply (or `y`), four at
       a time, with a Stop button; an Undo follows.

> **Try it:** search `label:` to show the cards without a label. Mark them
> all, edit, labels, type one, `enter` twice.

## Recap

- `H` `L` move, `u` undoes, `K` `J` rank, `M` to a sprint, `.` again.
- A quick edit on the board, or one key per field in the panel.
- `E` description, `c` comment, `R` reply, `@` mention, `A` for more.
- `n` creates; mark cards and edit them at once.

Previous: [The board and the panel](02-board-and-panel.md) · Next:
[Planning and reporting](04-planning-and-reporting.md)
