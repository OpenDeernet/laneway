# Markdown editor

Descriptions, rich-text fields and your own comments edit as markdown in
the terminal, and in the browser as markdown or visually (below). laneway
turns markdown into Jira's document format on save and back again on the
next edit.

## Plain markdown

Paragraphs, headings, lists, code, quotes, rules, and **bold**, *italic*,
`code`, ~~strike~~ and links. Text that would read as markdown (a `*`, a
`#` at a line's start, a `:smile:` typed as words) is escaped with `\`.

## Jira's extras

| Jira | Markdown |
| --- | --- |
| Mention | `@Ada Lovelace` |
| Emoji | `:smile:` |
| Date | `<date>2026-09-30</date>` |
| Status | `<status color="green">DONE</status>` |
| Smart link | `<https://…>` |
| Link card | `<!-- card: https://… -->` |
| Panel (info, note, success, warning, error) | its text between `<!-- panel:info -->` and `<!-- /panel -->` |
| Expand | its text between `<!-- expand: Title -->` and `<!-- /expand -->` |
| Action item | `- [ ] ` (`- [x] ` done) |
| Decision | `<> ` |
| Underline, sub, superscript | `<u>`, `<sub>`, `<sup>` |
| Text colour, background | `<span style="color:#ff5630">`, `<span style="background-color:#fff0b3">` |

## Tables

`| a | b |` rows. A cell's colour leads it as `<!-- bg:#deebff -->`, a
header cell outside the first row as `<!-- th -->`. `<br>` breaks a line in
a cell, `<br><br>` starts a paragraph. A numbered or sized table edits under
a `<!-- table:1 numbered, column widths -->` line that keeps its layout, and
its widths while the column count stays.

## What markdown can't hold

- Inline content without a syntax stands as `⟦2 …⟧`: edit around it, delete
  it to drop it.
- A block, such as an image or a table with merged cells, stands as a
  `<!-- keep:1 table … -->` line. Move the line and the block moves, delete
  it and the block goes; anything else and it comes back untouched.
- A synced block or columns edit as their text between `<!-- block:2 … -->`
  and `<!-- /block -->`.

## While typing

- `/` at a line's start lists all of the above to insert, narrowed as you
  type (`/warn`, `/todo`, `/green`).
- `@` and a few letters list people; the mention notifies them.
- `:` and two letters list emoji, the best match first (`:smle` finds
  `:smile:`), the ones you use most above the rest.

=== "Terminal"

    The text is styled as you type (bold, italic, strike, code, links).
    `ctrl+s` saves, `enter` is a newline, `esc` cancels (asking once when
    there are changes). `ctrl+e` hands the text to `$VISUAL` or `$EDITOR`
    (else `vi`); saving a changed file there writes it back. In a list,
    `↑` `↓` (or `ctrl+p` `ctrl+n`) pick and `tab` or `enter` inserts. A
    failed save keeps your text in a file and says where.

=== "Browser"

    Two modes, switched in the toolbar (the choice is kept per browser):

    - **Visual** edits Jira's document itself, as it shows, and saves it
      as it is: nothing goes through markdown. Markdown typed still
      formats (`# `, `- `, `1. `, `[] `, `> `, ```` ``` ````, `---`,
      `**bold**`). `/` inserts (tables, panels, expands, decisions, dates,
      status), `@` mentions, `:` picks an emoji. Pictures pasted or
      dropped upload where they land; drag an edge to size one, the bar
      over it places it left, centred, right or wrapped, sets its alt text
      and caption. A bar over a table adds and removes rows and columns,
      merges cells and colours them. Code blocks pick their language and
      are coloured. What it can't edit (a macro) is kept as it was.
    - **Markdown** draws markdown as rich text, its markers shown only on
      the caret's line (*Source* shows them all); `ctrl+p` previews,
      `alt+↑` `↓` move lines, `enter` continues a list, quote or table,
      `tab` nests a list item or steps through cells. Pasted HTML comes in
      as markdown.

    Switching converts what you wrote (a document markdown can't hold
    stays Visual). `ctrl+enter` saves, `esc` cancels (asking once when
    there are changes); `ctrl+b` `ctrl+i` `ctrl+k` (link) and `ctrl+e`
    (code) format in both.

What you write is kept as a draft in the state file a moment after each
change, shared by the terminal and the browser: open the editor on the
issue again, in either, and it comes back. Saving, posting or cancelling
drops it. The browser also sends it as the page goes, so a reload or a
closed tab keeps the last keystrokes.

A rich-text field the field configuration requires doesn't save empty;
the editor stays open.

A description or rich-text field saves only when Jira still has what the
editor opened on (for a draft, what it was written on). When someone
changed it meanwhile, nothing is written:

=== "Terminal"

    The editor opens again on your text. `ctrl+r` swaps in Jira's and back;
    `ctrl+s` saves the one showing over it.

=== "Browser"

    A dialog shows what saving yours would change in Jira's: keep editing,
    take theirs, or save yours over it.
