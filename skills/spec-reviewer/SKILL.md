---
name: spec-reviewer
description: Interactive spec document reviewer using the reviewer CLI with a live, in-page human-agent review loop.
allowed-tools: view_file replace_file_content write_to_file run_command
---
# Spec Reviewer Skill

This skill guides the AI agent to conduct interactive reviews of specification documents
(Markdown files) using the local `reviewer` tool. The review happens entirely on the served
HTML page: the user comments and submits, the agent updates the document and replies, and the
page auto-reloads to show the changes and the agent's notes.

## When to Use
Use this skill when a user asks to review, refine, or write specification documents in the repository.

## How the live loop works

`reviewer serve` no longer exits when the user submits. It stays running across review rounds:

1. The user adds gutter comments on document blocks and clicks **Submit Review**.
2. The server writes `<input>-feedback.json`, prints `FEEDBACK_RECEIVED` to stdout, and **keeps running**.
3. The agent reads the comments, edits the target `.md`, and writes a reply per comment plus a
   page-level change summary back into `<input>-feedback.json`.
4. The server watches the `.md` and the feedback file, re-renders, and pushes a reload over SSE.
   The open browser refreshes automatically — showing the updated document, each comment's agent
   reply, and the change summary.
5. The user marks addressed comments **resolved** and may add new comments, then submits again.
   Resolved comments are pruned on the next submit; unresolved ones carry forward.
6. The session ends when the user clicks **End Review** (the server shuts down) or cancels the process.

## Workflow

### 1. Discover the Target Specification
Locate the specification file to review.
- Search in standard locations like `docs/superpowers/specs/` or look for recently modified `.md` files.
- If multiple spec files are found, ask the user to clarify which file they want to review.

### 2. Launch the Review Server (in the background)
Run the server so it keeps running while you work:
```bash
go run ./cmd/reviewer serve <path/to/spec.md>
```
- Run it as a **background process** so you can keep editing while it serves. Do NOT pass
  `--no-open` unless the user asks — the browser should open to the review page.
- Note the URL it logs (typically `http://localhost:5500`).
- Tell the user the page is open and they can start commenting.

### 3. Wait for a submit (event-driven — do NOT ask the user to tell you)
The user should never have to say "I submitted." Start a **persistent monitor** that emits one event
each time the server prints `FEEDBACK_RECEIVED`; the runtime delivers each event to you as it lands,
with no user turn required.

- Capture the `reviewer serve` process's stdout to a log file, then run a poll-loop monitor over it.
  Use a poll loop, NOT `tail -f log | grep -m1` — if the log goes quiet after a match the pipeline
  hangs and the signal is lost.

```bash
# One event per new submit; keeps running for the whole session.
# NOTE: do NOT write `grep -c ... || echo 0` — on zero matches grep prints "0" AND exits 1, so the
# `|| echo 0` appends a second line and the variable becomes "0\n0", breaking the integer compare.
count() { c=$(grep -c FEEDBACK_RECEIVED "$LOG" 2>/dev/null); echo "${c:-0}"; }
base=$(count)
while true; do
  cur=$(count)
  if [ "$cur" -gt "$base" ]; then echo "REVIEW_SUBMIT total=$cur"; base=$cur; fi
  grep -q "Shutting down review server" "$LOG" 2>/dev/null && { echo "SERVER_STOPPED"; break; }
  sleep 2
done
```

- Each `REVIEW_SUBMIT` event is your cue to run step 4. The server stays alive; do not restart it.
- The monitor is persistent — you do NOT re-arm it per round. It fires again on the next submit and
  emits `SERVER_STOPPED` when the user clicks **End Review**, at which point the review is over.

### 4. Read the comments and update the document

**Report your progress on the page.** So the user can watch what you are doing without leaving the
review page, write your current activity to `<input>-status.json` at each step. The server pushes it
live to the page (no reload):

```jsonc
{"state": "working", "message": "コメントを確認しています…"}   // when you start
{"state": "working", "message": "対象文書を更新しています…"}   // while editing the .md
{"state": "working", "message": "返信を記入しています…"}       // while writing replies
{"state": "idle",    "message": ""}                          // when the round is fully done
```

- Set `state:"working"` with a short `message` as you move through the round; each write appears as a
  new line in the page's "Agent working…" panel.
- Always finish by writing `{"state":"idle","message":""}` so the panel clears once results are shown.

Then do the work:

- Read `<input>-feedback.json`. Its shape is:
  ```json
  { "comments": [ { "text": "...", "anchor": "...", "context": "...", "author": "human", "status": "open" } ], "summary": "" }
  ```
- Address each `open` comment by editing the target `.md`.
- Write your response back into the **same** `<input>-feedback.json`:
  - For each comment you addressed, set its `reply` (what you changed and why) and `replyTimestamp`.
  - Set the top-level `summary` to a short description of this round's changes.
  - **Do NOT set `status` to `resolved`.** Resolution is the human's decision — they mark comments
    resolved on the page after reviewing your reply.
  - Preserve the other comment fields (`text`, `anchor`, `context`, `author`, `timestamp`).
- Saving the `.md` and the feedback file triggers the page to auto-reload; the user sees your
  edits, your per-comment replies, and the summary without refreshing.

### 5. Iterate
- Continue the cycle: the user reviews your replies, marks comments resolved, adds new comments,
  and submits again (another `FEEDBACK_RECEIVED`). Repeat step 4.
- The review ends when the user clicks **End Review** (the `serve` process exits) or asks you to stop.

### Review focus
While reviewing, analyze the Markdown for:
- **Clarity & Completeness**: placeholders (TBD, TODO), missing definitions, ambiguities.
- **Internal Consistency**: architecture aligns with requirements.
- **Readability**: logical, clean heading hierarchy.
- **Reviewer System Invariants**: avoid nested blocks (lists inside callouts, nested tables) that
  break rendering or comment-anchor logic (see `AGENTS.md`).
