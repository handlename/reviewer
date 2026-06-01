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
* **Description**: A lightweight local HTTP server launched via the `reviewer serve` command.
* **Role**: It hosts the compiled spec, automatically launches the default web browser, and provides API endpoints (`/api/feedback`) to gather real-time comments and reviews from developers or reviewers.
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
* **Description**: The collection of review comments entered by the reviewer.
* **Persistence**: Clicking "Submit Review" inside the browser panel POSTs the data to the server, which writes it to `<input-filename>-feedback.json` and automatically shuts down.
