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

    cmd_serve -->|Reads| md_file
    cmd_serve -->|Calls| render
    cmd_serve -->|Starts Server & Embeds| server
    server -->|Launches Browser & Opens| UI[Interactive Review UI in Browser]
    UI -->|GET/POST /api/feedback| server
    server -->|Writes Feedback| json_file
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
* **Dynamic Port Allocation**:
  If the default port (`5500`) is in use, the server dynamically catches this error and queries a free port using `net.Listen("tcp", "127.0.0.1:0")`.
* **API Route (`/api/feedback`)**:
  * `GET`: Reads the existing `<input-filename>-feedback.json` file and returns all comments in JSON format.
  * `POST`: Unmarshals the incoming comment collection, saves it with proper indentations, and signals the host process to shut down.
* **Automatic Shutdown Gracefully**:
  Once a "Submit Review" action POSTs comments successfully, the server closes active connections and halts the Go process.

### D. UI Template (`references/template.html`)
Embedded into the Go binary at compile-time using the standard `//go:embed` directive.

* **Responsive 3-Column Layout**:
  * **Sidebar (Left)**: Renders the spec's title, version, date, and navigation.
  * **Main Content (Middle)**: Renders the compiled body of the specification.
  * **Feedback Panel (Right)**: Shows the comment inbox, list of active critiques, and submission options (visible only when served via HTTP).
* **Interactive DOM Initialization**:
  Upon load, the frontend JS runs `initializeCommentableElements()`. This attaches `data-anchor` attributes and a hover action `💬` to all root block elements (excluding headers, code tags, or nested child blocks).

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

Comments are marshaled and stored in the following JSON schema format:

```json
[
  {
    "text": "The comment text",
    "timestamp": "2026-06-01T20:46:27.000Z",
    "anchor": "spec-element-12",
    "context": "Context snippet representing the targeted block element"
  }
]
```
* `anchor`: Refers to the `data-anchor` property of the target DOM node.
* `context`: Truncated preview text of the target element (max 57 chars + `...`) used by the reviewer to identify the point of interest.
