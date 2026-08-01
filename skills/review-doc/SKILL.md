---
name: review-doc
description: Review specification documents and Markdown notes with the reviewer CLI, which renders them as structured HTML and hosts a live, in-page human-agent review loop. The human leaves gutter comments on document blocks and submits; the agent edits the document and replies inline; the page reloads automatically. Use when a user asks to review, refine, or iterate on a Markdown specification together. Triggers include "review this spec", "仕様書をレビューしたい", "let's review this document together".
compatibility: Requires the reviewer CLI (https://github.com/handlename/reviewer) on PATH, plus curl and a web browser.
license: MIT
---
# Review Doc Skill

This skill guides the AI agent to conduct interactive reviews of specification documents
(Markdown files) using the `reviewer` tool. The review happens entirely on the served
HTML page: the user comments and submits, the agent updates the document and replies, and the
page auto-reloads to show the changes and the agent's notes.

## When to Use
Use this skill when a user asks to review, refine, or write specification documents in the repository.

## How the live loop works

`reviewer serve` does not exit when the user submits. It stays running across review rounds:

1. The user adds gutter comments on document blocks and clicks **Submit Review**.
2. The server writes `<input>-feedback.json`, releases any `GET /api/wait` long-poll waiter (and still
   prints `FEEDBACK_RECEIVED` to stdout for back-compat), and **keeps running**.
3. The agent reads the comments, edits the target `.md`, and writes a reply per comment plus a
   page-level change summary back into `<input>-feedback.json`.
4. The server watches the `.md` and the feedback file, re-renders, and pushes a reload over SSE.
   The open browser refreshes automatically — showing the updated document, each comment's agent
   reply, and the change summary.
5. The user marks addressed comments **resolved** and may add new comments, then submits again.
   Resolved comments are pruned on the next submit; unresolved ones carry forward.
6. The session ends when the user clicks **End Review** (the server shuts down) or cancels the process.

## Workflow

### 0. Verify the reviewer CLI is available
This skill drives the `reviewer` command. Check that it is installed **before** doing anything else:

```bash
reviewer --version
```

If the command is not found, **stop and tell the user how to install it**. Do NOT install it yourself,
and do not fall back to another tool:

```console
$ brew install handlename/tap/reviewer
```

or

```console
$ go install github.com/handlename/reviewer/cmd/reviewer@latest
```

Resume the workflow only after the user confirms the installation.

### 1. Discover the Target Specification
Locate the specification file to review.
- Search in standard documentation locations, or look for recently modified `.md` files.
- If multiple spec files are found, ask the user to clarify which file they want to review.

### 2. Launch the Review Server (in the background)
Run the server so it keeps running while you work:
```bash
reviewer serve <path/to/spec.md>
```
- Run it as a **background process** so you can keep editing while it serves. Do NOT pass
  `--no-open` unless the user asks — the browser should open to the review page.
- Note the URL it logs (typically `http://localhost:5500`) and keep it as `$URL` — the step 3 monitor
  long-polls `$URL/api/wait`.
- Tell the user the page is open and they can start commenting.

### 3. Wait for a submit (event-driven — do NOT ask the user to tell you)
The user should never have to say "I submitted." Start a **persistent monitor** that long-polls the
server's `GET /api/wait` endpoint; the runtime delivers each event to you as it lands, with no user
turn required.

- `/api/wait` **blocks until the next submit**, then returns `200` (near-zero latency, no log parsing).
  With no activity it returns `204` after ~25s so the loop simply re-polls (long-poll convention).
- `$URL` is the server's base URL from step 2 (e.g. `http://127.0.0.1:5500`). Keep the server's
  timeout (25s) below curl's `--max-time` (30s) so the server, not curl, closes the idle connection.

```bash
# One REVIEW_SUBMIT event per submit; keeps running for the whole session.
while true; do
  code=$(curl -s -o /tmp/reviewer-wait.json -w '%{http_code}' --max-time 30 "$URL/api/wait")
  case "$code" in
    200) echo "REVIEW_SUBMIT" ;;         # a submit landed -> run step 4
    204) : ;;                            # idle timeout -> just re-poll
    *)   echo "SERVER_STOPPED"; break ;; # 000 (connection refused) etc. -> server gone, review over
  esac
done
```

- Each `REVIEW_SUBMIT` event is your cue to run step 4. The server stays alive; do not restart it.
- The monitor is persistent — you do NOT re-arm it per round. It fires again on the next submit and
  emits `SERVER_STOPPED` once the server is gone (the user clicked **End Review**), at which point the
  review is over.
- `FEEDBACK_RECEIVED` is still printed to stdout for back-compat/debugging, but `/api/wait` is the
  primary, low-latency detection path — prefer it over log scraping.

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
  break rendering or comment-anchor logic.
