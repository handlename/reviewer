# reviewer Design Overview (DESIGN)

This document describes the internal design, system architecture, and component data flows of the `reviewer` spec compiler and review server.

---

## 1. System Architecture

`reviewer` is packaged as a single Go binary that orchestrates three primary layers:

1. **CLI Layer (`github.com/alecthomas/kong`)**
   Parses command-line arguments and dispatches commands (`build`, `serve`).
2. **Markdown Processing Layer (`github.com/yuin/goldmark`)**
   Parses GFM input documents, extracts metadata (via `goldmark-meta`), and translates text structures into HTML.
3. **Review Server Layer (`net/http`)**
   Hosts an interactive web application UI (built using standard HTML/JS/CSS) and provides endpoints to record reviewer feedback.

### Architecture Overview Diagram

```mermaid
graph TD
    subgraph CLI (cli/command)
        root[root.go - Root CLI]
        cmd_build[build.go - Build Subcommand]
        cmd_serve[serve.go - Serve Subcommand]
    end

    subgraph Core (package reviewer)
        render[render.go - RenderSpec]
        server[server.go - StartReviewServer]
        tmpl[references/template.html - Embed UI Template]
    end

    subgraph Storage
        md_file[input.md - Spec File]
        html_file[output.html - Compiled HTML]
        json_file[input-feedback.json - Comments JSON]
    end

    root --> cmd_build
    root --> cmd_serve

    cmd_build -->|Reads| md_file
    cmd_build -->|Calls| render
    render -->|Embeds| tmpl
    cmd_build -->|Writes| html_file

    cmd_serve -->|Starts Server| server
    server -->|Re-renders on each request| render
    render -->|Embeds| tmpl
    server -->|Launches Browser & Opens| UI[Interactive Review UI in Browser]
    UI -->|GET / re-render · POST /api/feedback · POST /api/close| server
    UI -->|SSE /api/events| server
    server -->|Writes Feedback / prunes resolved| json_file
    server -->|reload on file change| UI
    agent[External Agent Claude Code] -->|Reads comments · writes reply+summary| json_file
    agent -->|GET /api/wait long-poll for submits| server
    agent -->|Edits| md_file
    md_file -->|fsnotify watch| server
    json_file -->|fsnotify watch| server
```

---

## 2. Core Components

### A. CLI Commands (`cli/command/`)
* **`root.go`**: Establishes global parameters and instantiates command context.
* **`build.go`**: Compiles the source Markdown into a standalone, styled HTML file. Output defaults to the same directory as the source Markdown file.
* **`serve.go`**: Compiles the spec, spins up the local HTTP web server, and triggers the operating system's default browser to load the review application.

### B. Spec Renderer (`reviewer/render.go`)
Converts raw GFM Markdown into interactive, presentation-ready HTML. In addition to standard GFM translation, it performs custom **HTML post-processing**.

* **Safe Post-Processing Design (Masking Logic)**:
  To prevent the post-processor's regular expressions from polluting sample code blocks (`<pre>` and `<code>` tags) written by the spec author, a strict "Mask & Restore" sequence is executed:
  1. Standard code blocks are extracted via `codeBlockRegex` and replaced with unique placeholders (`<!--CODE_BLOCK_PLACEHOLDER_N-->`).
  2. Modifications are applied to the remaining markup (e.g., embedding Mermaid figures, applying `spec-table` styles, injecting badges, and building callout containers).
  3. Once all modifications are done, the original code blocks are restored into their placeholder positions.
* **Static Regex Compilation**:
  To eliminate execution-time compilation overhead under load, all regex objects (`mermaidRegex`, `calloutRegex`, `codeBlockRegex`) are compiled at the global scope using `regexp.MustCompile`.

### C. Review Server (`reviewer/server.go`)

The server hosts a **persistent, in-page review loop** (hunk-inspired): the user comments and
submits, the agent updates the document and replies, and the open page auto-reloads. Submitting
does **not** shut the server down.

* **Dynamic Port Allocation**:
  If the default port (`5500`) is in use, the server dynamically catches this error and queries a free port using `net.Listen("tcp", "127.0.0.1:0")`.
* **On-the-fly rendering (`GET /`)**:
  The document is re-rendered from the source `.md` on every request, so the agent's edits appear
  on the next reload. (The server no longer serves a byte slice captured at startup.)
* **`/api/feedback`**:
  * `GET`: Returns the feedback document as `{ "comments": [...], "summary": "..." }`. A missing
    file yields `{"comments":[]}`.
  * `POST`: Unmarshals the incoming `Feedback`, **prunes comments the user marked `resolved`**,
    writes the file, **releases any `GET /api/wait` long-poll waiter** (the low-latency submit
    signal), also prints `FEEDBACK_RECEIVED` to stdout (back-compat), and **keeps the server running**
    so the agent can pick up the comments.
* **`GET /api/wait` (long-poll)**:
  Blocks until the next `POST /api/feedback`, then returns `200` with the current feedback JSON so the
  agent detects a submit with near-zero latency and no stdout scraping. With no activity it returns
  `204` after `waitTimeout` (25s) so the client re-polls (long-poll convention); the session ending
  (`/api/close` / context cancellation) or the client disconnecting also releases the waiter. Fan-out
  is handled by a `submitNotifier` (mirrors `sseHub`, but signal-only) so multiple concurrent waiters
  are all released by a single submit.
* **`POST /api/close`**:
  The page's **End Review** button hits this endpoint to end the session (graceful shutdown).
* **`GET /api/status`**:
  Returns the agent's current activity (`{ "state": "working|idle", "message": "..." }`) from the
  `-status.json` sidecar, so the page can restore the "working" indicator after a mid-round reload. A
  missing file yields `idle`.
* **`GET /api/events` (SSE)**:
  A Server-Sent Events stream carrying **typed JSON events** so the page can react differently:
  * `{"kind":"reload"}` — the document or feedback changed; the page calls `location.reload()`.
  * `{"kind":"status","state":...,"message":...}` — the agent's activity changed; the page updates the
    live activity panel **in place**, without reloading.
* **File watching (`fsnotify`)**:
  The server watches the directory containing the `.md` and its `-feedback.json` / `-status.json`
  sidecars. Document/feedback changes debounce (~150ms) into a `reload`; status changes debounce
  (~60ms, snappier) into a `status` event carrying the file's contents. This is how the agent's file
  edits and progress reach the browser with no explicit control call.
* **Graceful shutdown**:
  A single `done` signal (closed by `/api/close` or context cancellation) unblocks SSE handlers and
  blocked `/api/wait` waiters, and triggers `http.Server.Shutdown`.

### D. UI Template (`references/template.html`)
Embedded into the Go binary at compile-time using the standard `//go:embed` directive.

* **Responsive 3-Column Layout**:
  * **Sidebar (Left)**: Renders the spec's title, version, date, and navigation.
  * **Main Content (Middle)**: Renders the compiled body of the specification.
  * **Feedback Panel (Right)**: Shows the comment inbox, list of active critiques, and submission options (visible only when served via HTTP). Unsubmitted comments can be edited inline or deleted before submission.
* **Comment Display Order**:
  The feedback panel renders comments in the **appearance order of their target block** in the
  document, so the panel reads top-down alongside the spec. `commentsInAppearanceOrder()` builds an
  `anchor → position` map from the live DOM (`.main-content [data-anchor]`) and stable-sorts a
  `{comment, originalIndex}` view — so comments on the same block keep creation order, and the
  `idx`-based handlers (`editingIdx`, `deleteComment`, `saveComment`) continue to address the
  untouched `comments` array. Comments with no anchor, or whose anchored element disappeared after
  an agent edit, sink to the end in creation order. Only the rendering is reordered: the `comments`
  array and the feedback file written from it stay in creation order.
* **Interactive DOM Initialization**:
  Upon load, the frontend JS runs `initializeCommentableElements()`. This attaches `data-anchor` attributes and a hover action `💬` to all root block elements (excluding headers, code tags, or nested child blocks).
* **Inline Comment Editing State Management**:
  To support inline editing, the frontend JS tracks the active edit state using a global variable `editingIdx` (initialized to `-1` when no comment is being edited):
  * **Switching Mode**: Clicking the edit button (✎) or using keyboard shortcuts (`Enter` / `Space`) sets `editingIdx` to the corresponding comment index and triggers a re-render.
  * **UI Transformation**: During rendering, if the comment index matches `editingIdx`, it renders a focused `<textarea>` and action buttons (Save and Cancel) instead of plain text, supporting keyboard controls (`Ctrl/Cmd + Enter` to save, `Escape` to cancel).
  * **State Updates**: Saving updates both the comment text and its timestamp, then resets `editingIdx` to `-1`. Deleting a comment adjusts `editingIdx` to handle indices shifting safely.

---

## 3. Comment Targeting & DOM Traversal Constraints

To avoid rendering redundant comment bubbles on child nodes when their parent containers are already commentable, the frontend JavaScript enforces the following traversal guards:

1. **Code block exclusion**:
   Elements matching `.closest('pre')` or `.closest('code')` are skipped.
2. **Parent containment check**:
   If an element's parent container is already a block-level target (e.g., a paragraph `p` inside a callout, or a list item `li` inside a table cell), the element is excluded from receiving an anchor ID.

```javascript
// Preventing duplicate commenting anchors (from template.html)
let isNested = false;
let parent = el.parentElement;
while (parent && parent !== container) {
    if (parent.matches(selectors)) {
        isNested = true; // Skip this node because the parent is already commentable
        break;
    }
    parent = parent.parentElement;
}
```

---

## 4. Feedback Schema Specification (JSON)

The feedback document is the shared state for the loop, read and written by both the browser and
the agent. It is stored as a `Feedback` object:

```json
{
  "comments": [
    {
      "text": "The human comment text",
      "timestamp": "2026-06-01T20:46:27.000Z",
      "anchor": "spec-element-12",
      "context": "Context snippet representing the targeted block element",
      "author": "human",
      "status": "open",
      "reply": "The agent's reply describing how the comment was addressed",
      "replyTimestamp": "2026-06-01T20:50:00.000Z"
    }
  ],
  "summary": "Agent's page-level summary of the latest pass"
}
```

* `anchor`: Refers to the `data-anchor` property of the target DOM node.
* `context`: Truncated preview text of the target element (max 57 chars + `...`).
* `author`: `human` or `agent`. Top-level comments are human-authored.
* `status`: `open` or `resolved`. Only the **human** sets `resolved` (via a page control); resolved
  comments are pruned on the next submit. The agent must not self-resolve.
* `reply` / `replyTimestamp`: the agent's response threaded under the human comment.
* `summary`: the agent's change summary for the latest round, rendered at the top of the panel.
