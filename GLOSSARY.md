# reviewer Glossary (GLOSSARY)

This document defines the core domain terms used within the `reviewer` codebase and documentation. It serves as a unified reference to ensure human developers and AI agents share a common understanding of the system's terminology.

---

## 1. Core Concepts

### Spec (Specification)
* **Description**: A Markdown document written in GitHub Flavored Markdown (GFM) that serves as the target file processed by `reviewer`.
* **Role**: It describes system requirements, design, and flows, and is compiled into an interactive, beautifully styled HTML document.

### Render
* **Description**: The compilation pipeline that parses the Markdown spec, executes HTML post-processing (custom syntax replacements), and merges the content with the embedded layout template.
* **Relevant Modules**: `RenderSpec` function in `render.go`.

### Review Server
* **Description**: A lightweight local HTTP server launched via the `reviewer serve` command that hosts a persistent, in-page human-agent review loop.
* **Role**: It hosts the compiled spec (re-rendered on demand), launches the default web browser, and exposes endpoints for the loop: `/api/feedback` (GET/POST comments), `/api/close` (End Review), and `/api/events` (SSE live-reload). It stays running across review rounds — submitting no longer shuts it down.
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
* **Description**: The feature that allows reviewers to attach feedback directly to individual block-level elements (such as paragraphs `p`, list items `li`, table rows `tr`, and callouts) instead of a single page-wide comment.
* **Behavior**: Hovering over a commentable block reveals a `💬` button in the right margin, which opens a targeted input text area in the feedback panel.
* **Related Attribute**: `data-anchor="spec-element-X"`.

### Scroll-sync
* **Description**: An interactive navigation feature that coordinates the main content scroll position with the feedback sidebar.
* **Behavior**:
  * Clicking the inline comment indicator (`💬 X` count) next to a block element scrolls the feedback sidebar directly to the corresponding comment.
  * Clicking the `On: [preview]` context badge inside a sidebar comment scrolls the main spec view smoothly to center the targeted element.

### Feedback
* **Description**: The shared review state — a `{ comments, summary }` document read and written by both the browser and the agent.
* **Behavior**: Human comments can be edited inline or deleted before submitting. Clicking "Submit Review" POSTs the data to the server, which prunes resolved comments and writes `<input-filename>-feedback.json` while **staying alive** (it prints `FEEDBACK_RECEIVED` to signal the agent).
* **Persistence**: `<input-filename>-feedback.json` holds the current round's state, not accumulated cross-session history.

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
* **Behavior**: The server watches the `.md` and feedback file (`fsnotify`) and pushes a typed `reload` event when either changes, so the agent's edits and replies appear without a manual refresh. A reload is deferred (shown as a prompt) while the user has unsent edits.

### Agent Activity (Status)
* **Description**: The agent's live progress, surfaced on the review page so the user can watch what the agent is doing between submitting and the reply landing — without leaving the page or inspecting the agent session.
* **Behavior**: The agent writes its current activity to the `<input>-status.json` sidecar (`{ state, message }`). The server watches it and pushes a typed `status` event over SSE; the page updates the "Agent working…" panel **in place** (no reload). The agent writes `state:"idle"` when the round completes, which clears the panel. `GET /api/status` restores the indicator after a mid-round reload.
