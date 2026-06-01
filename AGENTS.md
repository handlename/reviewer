# reviewer AI Agent Development Guidelines (AGENTS)

> [!IMPORTANT]
> **To All AI Agents:**
> Before initiating any code changes, feature additions, or debugging tasks in this repository, you **MUST** read and prioritize these guidelines as your highest-priority context. This repository contains strict implicit rules and invariants designed to prevent critical bugs and performance regressions. Neglecting these rules will break core features or result in rejected changes.

---

## 1. Documentation Language Constraint

* **Rule**:
  * **All documentation (including Markdown files like `README.md`, `GLOSSARY.md`, `DESIGN.md`, and this `AGENTS.md` file) MUST be written in English.**
  * If you generate or modify documentation, do not write in Japanese or any other language unless explicitly requested by the user.

---

## 2. Regular Expressions & Performance Invariants

The specification parsing pipeline (`render.go`) heavily relies on regular expressions for markdown enhancement.

* **Rule**:
  * **Never compile regular expressions dynamically inside functions** (such as `RenderSpec` or `postProcessHTML`). Compiling regex at runtime introduces severe performance overhead and will bottleneck the server under high load.
  * All regex patterns **MUST be pre-compiled at the global scope** in `render.go` using `regexp.MustCompile` and referenced via global variables.

```go
// CORRECT: Compile once at the global level
var (
    myCustomRegex = regexp.MustCompile(`(?s)<my-pattern>(.*?)</my-pattern>`)
)
```

---

## 3. Code Block Protection during Post-Processing (Masking Logic)

A major source of regression is when the HTML post-processor's search-and-replace routines accidentally rewrite sample code segments (`pre` and `code` blocks) specified by a user in their Markdown file.

* **Rule**:
  * When executing replacements (such as priority badges, callout cards, custom styles) in `postProcessHTML`, **never match or modify code inside code blocks**.
  * `render.go` enforces a strict **"Mask & Restore"** pipeline. Any new post-processing rules **MUST** be placed between the masking phase (Step 2) and the restoration phase (Step 6) to keep code blocks isolated.

```go
// Basic structure of postProcessHTML in render.go
func postProcessHTML(htmlStr string) string {
    // 1. Process Mermaid blocks first (to avoid standard code masking)
    // 2. Temporarily mask standard code blocks
    var maskedBlocks []string
    htmlStr = codeBlockRegex.ReplaceAllStringFunc(htmlStr, func(match string) string {
        maskedBlocks = append(maskedBlocks, match)
        return fmt.Sprintf("<!--CODE_BLOCK_PLACEHOLDER_%d-->", len(maskedBlocks)-1)
    })

    // [ADD NEW HTML REPLACEMENT LOGIC HERE!]
    // Example: Badge replacement, adding table classes, etc.

    // 6. Restore the masked code blocks to their placeholders
    for i, block := range maskedBlocks {
        placeholder := fmt.Sprintf("<!--CODE_BLOCK_PLACEHOLDER_%d-->", i)
        htmlStr = strings.Replace(htmlStr, placeholder, block, 1)
    }
    return htmlStr
}
```

---

## 4. Double Comment Anchor Prevention (No Nested Anchors)

The client-side JavaScript in `template.html` walks the DOM tree of the spec page to inject `data-anchor` identifiers and a hover bubble (`💬`) onto block elements.

* **Rule**:
  * **Never attach multiple nested comment triggers to parent-child blocks.** For instance, callouts (`.callout`), tables (`spec-table`), and lists (`ul`/`ol`) are block-level items that can receive comments. Their inner child elements (`p`, `li`, `tr`) must **not** get their own nested comment triggers, as this breaks layout alignment and produces messy overlapping icons.
  * Do not modify or break the parent-traversal logic (`isNested` check) defined in `initializeCommentableElements()`. If you extend commentable target `selectors`, ensure this containment check continues to correctly filter out nested elements.

---

## 5. API Contracts & Serialization (JSON tags)

Communication between the browser interface (`template.html`) and the local HTTP handler (`server.go`) relies on JSON messaging.

* **Rule**:
  * When modifying or expanding the Go `Comment` struct, you **MUST** specify the corresponding `json` tags.
  * Ensure all JSON property names match **camelCase** serialization to keep front-end parsing fully compatible.

```go
type Comment struct {
    Text      string `json:"text"`
    Timestamp string `json:"timestamp"`
    Anchor    string `json:"anchor,omitempty"`  // DOM element ID data-anchor
    Context   string `json:"context,omitempty"` // Text preview of the block
}
```

---

## 6. Testing & Regression Verification

This project embraces high-test coverage and maintains rigorous regression tests.

* **Validation Checklist**:
  * Whenever making changes to parsing, rendering, or APIs, always run the entire test suite from the repository root:
    ```console
    $ go test ./... -v
    ```
  * Added features to post-processing must be accompanied by new test cases in `render_test.go`.
  * Changes to the HTTP server, APIs, or shutdown triggers must be covered by `server_test.go`.
  * Ensure your server-probing port logic preserves dynamic binding (`port 0`) behavior to prevent port collisions during integration testing.
