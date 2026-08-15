# reviewer Glossary (GLOSSARY)

This document defines the core domain terms used within the `reviewer` codebase and documentation. It serves as a unified reference to ensure human developers and AI agents share a common understanding of the system's terminology.

---

## 1. Core Concepts

### Review Target
* **Description**: The file handed to `reviewer serve` / `review_start`. It is either a **Spec** or a **Diff**; nothing declares which, and nothing needs to.
* **Role**: One input path serves both kinds of review, so an agent that wants its own change reviewed writes a diff to a temporary file and passes it down the path it already uses.

### Kind
* **Description**: What a review target turns out to be once its content is read: `KindMarkdown` or `KindDiff`.
* **Behavior**: Decided from the **content**, never from the file name or a flag. Fenced code blocks are skipped while deciding, so a spec that quotes a diff is still a spec.
* **Relevant Modules**: `DetectKind` in `diff.go`.

### Spec (Specification)
* **Description**: A Markdown document written in GitHub Flavored Markdown (GFM) that serves as the target file processed by `reviewer`.
* **Role**: It describes system requirements, design, and flows, and is compiled into an interactive, beautifully styled HTML document.

### Diff
* **Description**: A unified diff (`git diff` output, or plain `diff -u`) as a review target. Comments attach to line ranges and to whole files rather than to document blocks.
* **Excluded**: Combined diffs (`@@@ … @@@`, produced by merges) are rejected: a line there has no single before/after, so the anchor coordinate system cannot describe it.

### Render
* **Description**: The compilation pipeline that turns a review target into the review page: `Render` decides the **Kind** and dispatches to the Markdown or the diff renderer, then both merge their body into the embedded layout template.
* **Relevant Modules**: `Render`, `RenderSpec` in `render.go`; `RenderDiff` in `diff.go`.
* **Note**: The Markdown path runs HTML post-processing (badges, callouts, Mermaid); the diff path deliberately does not, and escapes every diff-derived string itself instead.

### Review Server
* **Description**: A lightweight local HTTP server launched via the `reviewer serve` command that hosts a persistent, in-page human-agent review loop.
* **Role**: It hosts the compiled spec (re-rendered on demand), launches the default web browser, and exposes endpoints for the loop: `/api/feedback` (GET/POST comments), `/api/wait` (long-poll for submits), `/api/close` (End Review), and `/api/events` (SSE live-reload). It stays running across review rounds — submitting no longer shuts it down.
* **Relevant Modules**: `StartReviewServer` function in `server.go`.

---

## 2. Syntax & Decoration

### Badge
* **Description**: Custom markup tags denoting priority, requirements status, or certain assumptions within a specification.
* **Supported Badges**:
  * `[Must]` / `<strong>Must</strong>` -> Mandatory requirements (Red)
  * `[Should]` / `<strong>Should</strong>` -> Recommended features (Orange)
  * `[Could]` / `<strong>Could</strong>` -> Desirable, optional items (Green)
  * `[Wont]` / `<strong>Wont</strong>` -> Dropped or deferred items (Gray)
  * `[Confirmed]` / `<strong>Confirmed</strong>` -> Confirmed behavior (Light Green)
  * `[Inferred]` / `<strong>Inferred</strong>` -> Inferred system behaviors (Blue)
  * `[Assumption]` / `<strong>Assumption</strong>` -> Baseline assumptions/hypotheses (Purple)
* **Processing**: Translated during post-processing into custom `<span>` tags with specific CSS classes (e.g., `badge-must`).

### Callout
* **Description**: Highlighted cards style borrowed from GitHub-flavored alert blockquotes.
* **Supported Types**: `NOTE`, `TIP`, `IMPORTANT`, `WARNING`, `CAUTION`
* **Processing**: Transformed during HTML post-processing into themed container elements (`<div class="callout callout-xxxx">`) with corresponding borders, titles, and backgrounds.

### Mermaid Block
* **Description**: Text blocks containing diagram specifications written in Mermaid syntax (`<pre><code class="language-mermaid">`).
* **Processing**: Converted into `<figure class="diagram-container"><div class="mermaid">` blocks during rendering, which are dynamically evaluated and rendered as SVG charts by the client-side `mermaid.js` library.

---

## 3. Review Features

### Element-level Commenting
* **Description**: The feature that allows reviewers to attach feedback directly to individual block-level elements (such as paragraphs `p`, list items `li`, table rows `tr`, and callouts) instead of a single page-wide comment. This is the **Spec** side of commenting; a **Diff** targets line ranges and files instead (see section 4).
* **Behavior**: Hovering over a commentable block reveals a `💬` button in the right margin, which opens a targeted input text area in the feedback panel.
* **Related Attribute**: `data-anchor="spec-element-X"`.

### Anchor
* **Description**: The string that says what a comment is about. One field, three forms:
  * `spec-element-12` — a Markdown block, numbered in DOM traversal order.
  * `<path>#<start>-<end>` — a range of a diff file's rendered lines.
  * `<path>#file` — a diff file as a whole.
* **Behavior**: Parsing splits on the **last** `#`, so a path that itself contains one still round-trips. A form that a parser does not recognise is passed through untouched, which is how one code path serves both kinds of review.

### Connector Line
* **Description**: The curve drawn between a selected comment and the thing it is about.
* **Behavior**: Selecting a comment (clicking its card, or its `💬` indicator) scrolls the target into view, highlights it, and draws the line; it follows scrolling and resizing, and fades toward the screen edge when one end is off-screen.

### Feedback
* **Description**: The shared review state — a `{ comments, summary }` document read and written by both the browser and the session. It is reviewer's internal store; the agent reaches it only through the MCP tools.
* **Behavior**: Human comments can be edited inline or deleted before submitting. Clicking "Submit Review" POSTs the data to the server, which prunes resolved comments, assigns ids, and writes the **Sidecar** while **staying alive**. The submit releases any `/api/wait` long-poll waiter and any `review_wait` call. Nothing is written to stdout: that is the MCP transport.

### Sidecar
* **Description**: The files holding one document's review state: `$TMPDIR/reviewer/<stem>-<hash>-feedback.json` and its `-status.json` sibling.
* **Behavior**: Named for the document's stem plus the first four bytes of the SHA-256 of its absolute path, because basenames collide across directories. Written `0600` in a `0700` directory. They live under the temp directory rather than beside the document, so reviewing a file inside a repository leaves no untracked files behind — and are therefore subject to that directory's cleanup: the state survives a reload and a restart, not indefinitely.
* **Concurrency**: All access is serialised by `sidecarMu` on the session. The lock is taken at handler and method entry, never inside the shared read helper, which `Reply` calls while already holding it.

### Agent Reply
* **Description**: The agent's response threaded beneath a human comment (`reply` / `replyTimestamp` fields), describing how the comment was addressed.
* **Behavior**: Written by the agent into the feedback file; rendered under the human comment on the next reload. The agent never marks a comment resolved.

### Change Summary
* **Description**: The agent's page-level `summary` of the latest round's document changes, rendered at the top of the feedback panel.

### Resolve
* **Description**: A human-only action that marks an addressed comment `resolved` on the page after reviewing the agent's reply.
* **Behavior**: Resolved comments recede for the current cycle and are pruned on the next submit; only open comments carry forward.

### Live Reload
* **Description**: Automatic browser refresh driven by Server-Sent Events (`/api/events`).
* **Behavior**: The server watches **the review target only** (`fsnotify`, 150 ms debounce) and pushes a typed `reload` event when it changes, so the agent's edits appear without a manual refresh. The sidecar is not watched: the session is its only writer and announces its own changes. A reload is deferred (shown as a prompt) while the user has unsent edits.

### Submit Long-poll (`/api/wait`)
* **Description**: The agent-facing counterpart to Live Reload: a long-poll endpoint the agent uses to detect a human submit with near-zero latency, replacing log-string polling.
* **Behavior**: `GET /api/wait` blocks until the next `POST /api/feedback`, then returns `200` with the current feedback JSON; an idle wait returns `204` after ~25s so the agent re-polls. A `submitNotifier` (a signal-only sibling of the SSE hub) fans one submit out to every concurrent waiter, and the session ending releases blocked waiters.
* **Direction**: Whereas SSE (`/api/events`) pushes agent→browser changes, `/api/wait` carries the browser→agent submit signal.

### Agent Activity (Status)
* **Description**: The agent's live progress, surfaced on the review page so the user can watch what the agent is doing between submitting and the reply landing — without leaving the page or inspecting the agent session.
* **Behavior**: The agent writes its current activity to the `<input>-status.json` sidecar (`{ state, message }`). The server watches it and pushes a typed `status` event over SSE; the page updates the "Agent working…" panel **in place** (no reload). The agent writes `state:"idle"` when the round completes, which clears the panel. `GET /api/status` restores the indicator after a mid-round reload.

---

## 4. Diff Review

### File / Hunk / Line
* **Description**: The parsed shape of a diff. A `File` has hunks, a `Hunk` has its `@@` header and lines, a `Line` has a kind (context / add / delete / meta), its content with the leading marker stripped, and its number on each side (0 where it does not exist).
* **Relevant Modules**: `ParseUnifiedDiff` in `diff.go`.

### Display Path
* **Description**: The single path a file is known by on the page and inside anchors: its new path, falling back to the old one when the new side is `/dev/null`.
* **Why**: Using `/dev/null` as-is would put every deleted file in one anchor namespace, so two deletions in one diff would collide. A renamed file is therefore addressed by its **new** name.

### Rendered Line Index
* **Description**: The coordinate a diff anchor counts in: the 1-based position of a line among its file's rendered diff lines — added, removed and context lines all counted, `@@` headers not.
* **Why not source line numbers**: A removed line has no number on the new side, so "do not delete this line" — and any range spanning a removal and its replacement, which is what a suggestion is — could not be expressed. `path#12-15` looks like GitHub's `path#L12-L15` but is **not** a source line number.

### Anchor Lines
* **Description**: The exact text of the lines a comment was written against, markers stripped, persisted on the comment as `anchorLines`.
* **Role**: Line numbers move every time the agent regenerates the diff; the text is what finds the lines again. It is also what an agent should search for to locate the code.

### Re-anchoring
* **Description**: Recomputing where a diff comment belongs against the current diff.
* **Rules**: (1) if the recorded range still holds exactly that content, keep it and do not search; (2) otherwise search the file — exactly one match moves the comment, zero or several leave it **Outdated**. Matching compares content only, never line kind, and never crosses a hunk boundary. A whole-file comment follows the file itself.
* **Behavior**: Computed **for display only**, when the page asks for the comments, and never written back. The browser holds the result and posts it at the next submit, so persistence keeps riding the existing write path.
* **Why rule 1**: Searching by content alone would send most single-line comments outdated on the first reload with the diff unchanged, because lines like `}` or `return nil` occur several times in one file.
* **Relevant Modules**: `reanchor.go`.

### Outdated
* **Description**: A diff comment whose lines are no longer in the diff (or whose file has left it).
* **Behavior**: It stays in the panel, marked, with the lines it was written against quoted — that quotation is the only remaining record of what it referred to. It keeps its anchor, so it re-attaches if the lines come back, and it remains a valid target for `review_reply`. It is excluded from the diff body: no highlight, no `💬`, sorted to the end of the panel.

### Suggestion
* **Description**: A ` ```suggestion ` fenced block inside a comment: the replacement the human wants for the anchored lines.
* **Behavior**: Inserted into the composer only when the "quote lines" button is clicked, never automatically. The panel renders it as a diff against `anchorLines`, with lines shared at either end kept as context. Applying it is the agent's job — reviewer never touches the source.

### File Status
* **Description**: What happened to a file in this diff: added, deleted, renamed, or modified.
* **Behavior**: Shown as a mark in front of the name in the file list (`+`, `−`, `⇄`, `·`) and in words on the file header, where there is room to say "renamed from …" in full.
