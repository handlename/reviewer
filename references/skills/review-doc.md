# Review Doc Skill

This skill drives an interactive review of a Markdown document or a unified diff. The human
comments in a browser — on document blocks for Markdown, on line ranges for a diff — and you
edit and reply. Everything you need is exposed as MCP tools by the `reviewer` server — there are
no files to watch and no commands to run.

## When to Use
Use this skill when a user asks to review, refine, or write specification documents in the
repository, and when they want to review **the changes you just made**: write the diff to a
temporary file and open that.

## Tools

| Tool | Purpose |
| --- | --- |
| `review_start` | Open a Markdown document or a unified diff for review. Returns the review URL. |
| `review_wait` | Block until the human submits. Returns their comments. |
| `review_reply` | Write a reply under each comment, plus a summary of the round. |
| `review_progress` | Report what you are doing, live, on the review page. |

If these tools are not available, the `reviewer` MCP server is not registered. Tell the user how
to add it rather than falling back to another tool:

```console
$ claude mcp add reviewer -- reviewer mcp
```

Installing the `reviewer` Claude Code plugin registers the server automatically. The `reviewer`
binary must be on `PATH` either way; if it is missing, stop and tell the user to install it with
`brew install handlename/tap/reviewer` or
`go install github.com/handlename/reviewer/cmd/reviewer@latest`. Do not install it yourself.

## Workflow

### 1. Find the review target
**Reviewing a document:** locate the file. Search the usual documentation locations or look for
recently modified `.md` files. If several candidates exist, ask the user which one.

**Reviewing your own changes:** write the diff to a temporary file and use that path, e.g.

```console
$ git diff > "$TMPDIR/review.diff"
```

Keep the same path for the whole review: each round you regenerate the diff into that file, and
the page reloads by itself. `reviewer` decides from the content whether it is looking at Markdown
or at a diff, so there is nothing to declare.

### 2. Open the review
Call `review_start` with the path. Tell the user the returned URL is open and they can start
commenting.

### 3. Wait for a submit
Call `review_wait`. It returns one of three outcomes:

- `submitted` — the human submitted. Their comments are in `comments`. Go to step 4.
- `timeout` — nobody submitted yet. This is normal, not a failure. Call `review_wait` again.
- `session_ended` — the human clicked **End Review**. The review is over; stop.

Never ask the user whether they have submitted. Waiting is what `review_wait` is for.

### 3.5. Reading a comment on a diff
A comment made on a diff carries two extra things:

- `anchor` — `<path>#<start>-<end>`. **The numbers are 1-based positions among that file's
  rendered diff lines** (added, removed and context lines all counted, `@@` headers not). They
  are **not** source line numbers, and they are not GitHub's `#L12-L15`.
- `anchorLines` — the exact text of those lines, markers stripped. This is what you locate the
  code with; search the file for it rather than trusting a line number.

A comment may contain a ` ```suggestion ` block: the replacement the human wants for the
anchored lines. Apply it yourself — reviewer does not touch your source — and say so in your
reply. Treat it as a proposal you understand, not a patch to paste blindly: if it is wrong or
incomplete, say why in the reply instead of applying it.

### 4. Address the comments
Call `review_progress` with `state: "working"` and a short message as you go, so the user can
watch without leaving the page. Then:

- Edit the document — or, when reviewing a diff, the source the diff was taken from — to address
  each comment, then regenerate the diff into the same file.
- Call `review_reply` with one entry per comment you addressed, using the `id` from
  `review_wait`, plus a `summary` of this round's changes.
- Call `review_progress` with `state: "idle"` and an empty message once the round is done.

The page updates on its own: your edits, your per-comment replies, and the summary all appear
without a reload.

Resolving a comment is the human's decision, made on the page. You cannot mark one resolved, and
should not ask to.

### 5. Iterate
Return to step 3. The human reviews your replies, marks comments resolved, may add new ones, and
submits again. Resolved comments disappear on the next submit; unresolved ones carry forward.

## Review focus
While reviewing a Markdown document, analyse it for:
- **Clarity & Completeness**: placeholders (TBD, TODO), missing definitions, ambiguities.
- **Internal Consistency**: architecture aligns with requirements.
- **Readability**: logical, clean heading hierarchy.
- **Reviewer System Invariants**: avoid nested blocks (lists inside callouts, nested tables) that
  break rendering or comment-anchor logic.
