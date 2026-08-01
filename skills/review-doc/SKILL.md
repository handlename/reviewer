---
name: review-doc
description: Review specification documents and Markdown notes with the reviewer MCP server, which renders them as structured HTML and hosts a live, in-page human-agent review loop. The human leaves gutter comments on document blocks and submits; the agent edits the document and replies inline; the page reloads automatically. Use when a user asks to review, refine, or iterate on a Markdown specification together. Triggers include "review this spec", "仕様書をレビューしたい", "let's review this document together".
compatibility: Requires the reviewer CLI (https://github.com/handlename/reviewer) on PATH, registered as an MCP server, plus a web browser.
license: MIT
---
# Review Doc Skill

This skill drives an interactive review of a Markdown document. The human comments on document
blocks in a browser; you edit the document and reply. Everything you need is exposed as MCP
tools by the `reviewer` server — there are no files to watch and no commands to run.

## When to Use
Use this skill when a user asks to review, refine, or write specification documents in the repository.

## Tools

| Tool | Purpose |
| --- | --- |
| `review_start` | Open a document for review. Returns the review URL. |
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

### 1. Find the document
Locate the file to review. Search the usual documentation locations or look for recently modified
`.md` files. If several candidates exist, ask the user which one.

### 2. Open the review
Call `review_start` with the document path. Tell the user the returned URL is open and they can
start commenting.

### 3. Wait for a submit
Call `review_wait`. It returns one of three outcomes:

- `submitted` — the human submitted. Their comments are in `comments`. Go to step 4.
- `timeout` — nobody submitted yet. This is normal, not a failure. Call `review_wait` again.
- `session_ended` — the human clicked **End Review**. The review is over; stop.

Never ask the user whether they have submitted. Waiting is what `review_wait` is for.

### 4. Address the comments
Call `review_progress` with `state: "working"` and a short message as you go, so the user can
watch without leaving the page. Then:

- Edit the document to address each comment.
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
While reviewing, analyse the Markdown for:
- **Clarity & Completeness**: placeholders (TBD, TODO), missing definitions, ambiguities.
- **Internal Consistency**: architecture aligns with requirements.
- **Readability**: logical, clean heading hierarchy.
- **Reviewer System Invariants**: avoid nested blocks (lists inside callouts, nested tables) that
  break rendering or comment-anchor logic.
