# reviewer AI Agent Development Guidelines (AGENTS)

> [!IMPORTANT]
> **To All AI Agents:**
> Before initiating any code changes, feature additions, or debugging tasks in this repository, you **MUST** read and prioritize these guidelines as your highest-priority context. Naming is one of them: §11 requires every new name to come from `GLOSSARY.md`. This repository contains strict implicit rules and invariants designed to prevent critical bugs and performance regressions. Neglecting these rules will break core features or result in rejected changes.

---

## 1. Documentation Language Constraint

* **Rule**:
  * **All documentation (including Markdown files like `README.md`, `GLOSSARY.md`, `DESIGN.md`, `UI_DESIGN.md`, and this `AGENTS.md` file) MUST be written in English.**
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

### The diff path does not post-process — it escapes

`Render` dispatches on the content: Markdown goes through `postProcessHTML`, a unified diff does
not. Badge and callout rewriting would corrupt code, and there is no Markdown to enhance.

* **Rule**:
  * The template is `text/template`, and nothing upstream of the diff renderer produces HTML, so
    **every diff-derived string MUST be escaped with `html.EscapeString` before it is embedded** —
    line content, hunk headers, and file paths, which also land in attribute values.
  * Hunk headers are not optional: a diff touching
    `func (s *ReviewSession) Done() <-chan struct{}` puts `<-chan` straight into one, and a path
    containing `"` breaks out of `data-file="…"`.
  * **Never route diff output through `postProcessHTML`** to "reuse" its table or badge handling.
    A `[Must]` inside a code comment must stay text.

---

## 4. Double Comment Anchor Prevention (No Nested Anchors)

The client-side JavaScript in `template.html` walks the DOM tree of the spec page to inject `data-anchor` identifiers and a hover bubble (`💬`) onto block elements.

* **Rule**:
  * **Never attach multiple nested comment triggers to parent-child blocks.** For instance, callouts (`.callout`), tables (`spec-table`), and lists (`ul`/`ol`) are block-level items that can receive comments. Their inner child elements (`p`, `li`, `tr`) must **not** get their own nested comment triggers, as this breaks layout alignment and produces messy overlapping icons.
  * Do not modify or break the parent-traversal logic (`isNested` check) defined in `initializeCommentableElements()`. If you extend commentable target `selectors`, ensure this containment check continues to correctly filter out nested elements.

* **Rule (diff review)**:
  * A diff comment targets a range of lines, but **`data-anchor` MUST be set on the first line of
    the range only**; the remaining lines carry `data-anchor-member`. Six functions resolve an
    anchor with `document.querySelector('[data-anchor="…"]')`, which expects exactly one element.
    Duplicating the attribute does not throw — those functions silently do nothing, which is far
    harder to notice than a crash. Collect the rest of the range from the member lines when the
    whole of it has to light up.
  * Selections must not cross a hunk boundary, and the re-anchoring search must not either.

---

## 5. API Contracts & Serialization (JSON tags)

Communication between the browser interface (`template.html`) and the local HTTP handler (`server.go`) relies on JSON messaging.

* **Rule**:
  * When modifying or expanding the Go `Comment` struct, you **MUST** specify the corresponding `json` tags.
  * Ensure all JSON property names match **camelCase** serialization to keep front-end parsing fully compatible.

```go
type Comment struct {
    Text        string   `json:"text"`
    Timestamp   string   `json:"timestamp"`
    Anchor      string   `json:"anchor,omitempty"`      // spec-element-N | <path>#<start>-<end> | <path>#file
    AnchorLines []string `json:"anchorLines,omitempty"` // diff only: the lines the comment was written against
    Outdated    bool     `json:"outdated,omitempty"`    // diff only: those lines are gone
    Context     string   `json:"context,omitempty"`     // Text preview of the target
}
```

* **Rule (a comment is a thread)**:
  * `Comment` carries the **head** of the thread; `Messages` carries the turns after it. Anything
    that has to hold for every turn — the pending-question rule, rendering, counting — is written
    against `Comment.thread()`, the flat projection of the two, never against `Messages` alone.
    Special-casing the head is how the two authors' rules drift apart.
  * The same rule exists twice on purpose: in Go (`Comment.PendingQuestion`) and in
    `references/template.html` (`pendingQuestion`). The page cannot call Go, and the server cannot
    see the DOM. **Change one and you must change the other** — the Go table in `server_test.go` is
    the specification for both.

* **Rule (fields added for diff review)**:
  * New fields **MUST** be `omitempty`. The sidecar round-trips `Comment` through
    `encoding/json`, and a Markdown comment must serialise exactly as it did before — old sidecars
    are read back after an upgrade.
  * `Comment` **is** the MCP response schema (`waitOutput.Comments`), so anything added here is
    something every agent sees. Document its meaning in the `review_start` description and in
    `references/skills/review-doc.md` at the same time.

---

## 6. Testing & Regression Verification

This project embraces high-test coverage and maintains rigorous regression tests.

* **Validation Checklist**:
  * Whenever making changes to parsing, rendering, or APIs, always run the entire test suite from the repository root:
    ```console
    $ go test ./... -v
    ```
  * Added features to post-processing must be accompanied by new test cases in `render_test.go`.
  * Changes to diff detection, parsing or rendering belong in `diff_test.go`; changes to how
    comments survive a round belong in `reanchor_test.go`.
  * `RenderDiff`'s output is pinned by golden files under `testdata/`. Regenerate them with
    `go test . -run TestRenderDiffBody -update` (the flag is defined in the root package only, so
    `./...` will fail on it) and **read the resulting diff before committing it** — the point of a
    golden file is that a surprising change is visible.
  * Changes to the HTTP server, APIs, or shutdown triggers must be covered by `server_test.go` or `session_test.go`, whichever owns the code.
  * Changes to the MCP tool surface must be covered by `mcpserver_test.go`.
  * Ensure your server-probing port logic preserves dynamic binding (`port 0`) behavior to prevent port collisions during integration testing.

---

## 7. Stdout Is the MCP Transport

`reviewer mcp` serves JSON-RPC over stdin/stdout.

* **Rule**:
  * **Never write to stdout from `package reviewer` or from the CLI layer.** Any stray `fmt.Print*`
    to stdout is parsed as a protocol message and breaks every agent session. This is why the
    `FEEDBACK_RECEIVED` line was removed from `POST /api/feedback` and why `handleError` writes to
    `os.Stderr`.
  * Diagnostics go to stderr. `InitLogger` already configures zerolog with
    `zerolog.ConsoleWriter{Out: os.Stderr}`; use `log` rather than `fmt`.

```go
// WRONG: corrupts the JSON-RPC stream
fmt.Println("something happened")

// CORRECT
log.Info().Msg("something happened")
```

---

## 8. Tool Errors Versus Tool Outcomes

The MCP SDK turns a Go `error` returned from a tool handler into a result flagged `IsError`.

* **Rule**:
  * **Return an error only for a genuine agent mistake** — waiting before any review was started,
    replying to an unknown comment id, an invalid progress state.
  * **Anything that is merely "what happened" must be a value.** `review_wait` reports `submitted`,
    `timeout` and `session_ended` through its `outcome` field. Returning an error for an idle
    expiry would present a routine wait as a broken call and would likely stop the agent's loop.
  * When adding an outcome, extend the `WaitOutcome` constants rather than reaching for an error.

---

## 9. The Review Screen Follows UI_DESIGN.md

`references/template.html` is governed by a written design system. [UI_DESIGN.md](UI_DESIGN.md) is
**normative**: it records each principle together with the reasoning and the alternatives that were
rejected. A change that contradicts a principle there is a change to that document first, and the
reasoning under the principle is what has to be answered.

* **Rule**:
  * **Read UI_DESIGN.md before changing how the review screen looks or behaves.** Do not infer the
    design system from the stylesheet — several rules exist to prevent regressions that the CSS
    alone does not explain.
  * **Never introduce a second hue.** Hierarchy comes from opacity, weight and whitespace, and
    there is exactly one accent. The diff's add/delete tints are the single sanctioned exception
    (UI_DESIGN.md §2.1–2.2). The four comment states are told apart by position, stroke style and
    depth for this reason, and **no comment state may touch `padding` or `border`** (§4).
  * **Never write a width or a layout state into an inline style.** Widths live in
    `--feedback-panel-width` / `--document-min-width`, and mode state rides `<body>` classes
    (`is-served`, `rail-collapsed`, `diff-review`). An inline style outranks the
    narrow-viewport media queries (§3.3).
  * **There is no browser test harness.** `go test ./...` cannot see this class of regression.
    Verify in a real browser, in **both** light and dark, against `examples/sample.md` and
    `examples/sample.diff` (§9). How that verification is done — and that **you** do it — is
    section 10.

---

## 10. Verifying a Review-Screen Change in a Browser

The review screen is the one part of this repository the test suite cannot see. Every defect found
in `references/template.html` so far was of a kind `go test ./...` passes over without a word: a
control that never rendered at all, two controls colliding, four pixels of vertical misalignment.
Each was obvious within seconds of opening the page.

* **Rule**:
  * **Whoever changed the page verifies the page.** Do not finish a change to
    `references/template.html` by writing that the check by eye is left to review — that hands a
    human the one check the change actually needed. Report what you observed, not what you expect
    to be true.
  * **Verify every state the change can render in, not just the one you were working on.** A thread
    with no messages and a thread with several; a comment with a target and one without; a fresh
    comment and a resolved one; light **and** dark. A control that is gated on a condition is
    exactly the control that is missing in the state nobody opened.
  * **Measure anything positional; do not eyeball it.** Read `getBoundingClientRect()` and compare
    the numbers before and after. "Looks aligned" is how a four-pixel offset survives, and a
    measurement is also what makes the fix reviewable.
  * **Operate the real controls rather than inspecting the DOM only.** Click the button, send the
    keystroke, and check what changed. A `confirm()` can be exercised in both directions by
    replacing `window.confirm` in the page for the duration of the check, which is the only way to
    verify a two-branch dialog without a human at the keyboard.
  * **Never navigate or reload a page a human is using without asking.** Their unsent comments and
    open editors live only in that tab; a reload discards them. `hasUnsentEdits()` exists to stop
    the server doing this — do not do by hand what the code refuses to do.

### A recipe that works (macOS)

Drive a headless Chrome over the DevTools Protocol. Keep the browser running between steps so page
state survives, and talk to it from Node, whose `WebSocket` is built in — no dependency to install.

```console
$ (nohup "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" --headless=new \
    --remote-debugging-port=9222 --user-data-dir="$TMPDIR/cdp-profile" about:blank & )
$ curl -s http://127.0.0.1:9222/json   # the page target's webSocketDebuggerUrl
```

`Runtime.evaluate` runs assertions and clicks inside the page; `Page.captureScreenshot` takes the
picture. Four traps, each of which cost a round of confusion:

* **`--screenshot` on its own hangs.** The page holds an SSE stream open on `/api/events`, so a
  capture that waits for the load to settle waits forever. Capture over CDP instead.
* **`setsid` does not exist on macOS.** Background with `(nohup … & )`.
* **`Emulation.setEmulatedMedia` is scoped to the CDP connection**, so a light/dark override
  disappears when that connection closes. Set the scheme and take the shot in the **same**
  connection, or the picture comes back in the default scheme.
* **A `clip` rect is in page coordinates**, while `getBoundingClientRect()` is viewport-relative. A
  full-viewport capture with the element scrolled into view is less trouble than getting a closeup
  right.

To exercise the MCP tools against your own build across several steps, feed `reviewer mcp` from a
FIFO that a second process holds open, and read its answers from the log it writes:

```console
$ mkfifo in
$ (nohup ./reviewer mcp --no-open < in > out.log 2> err.log & )
$ (nohup tail -f /dev/null > in & )        # keeps the FIFO open across steps
$ printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize", ...}' > in
```

That drives the real tool surface — `review_start`, `review_wait`, `review_reply` — rather than a
stand-in, so the page under test is the page an agent would actually get.

---

## 11. Names Come from GLOSSARY.md

`GLOSSARY.md` is this codebase's vocabulary, and its §5 (Screen Anatomy) is the vocabulary of the
review page. It is not a summary written after the fact — it is where a name is decided.

* **Rule**:
  * **Before naming anything — a function, a variable, a CSS class, an element id, a `data-`
    attribute, a `<body>` class, a localStorage key, a comment, a commit message — look the thing
    up in `GLOSSARY.md` and use the term you find there.** If a term exists, its spelling is not
    yours to vary: no synonym, no abbreviation, no second word for the same thing.
  * **One term, one identifier stem.** The **Implementation** line of each entry lists the
    identifiers that term owns. `Connector Line` is `connector`, never `connection`; `Comment Card`
    is `comment-card`, never `feedback-item`; `Contents Rail` is `contentsRail` / `.contents-rail`,
    and its parts take the shorter `rail-` prefix (`.rail-toc-item`, `#hideRailBtn`).
    The two figures at the top of GLOSSARY §5 label every part of the review page with its term and
    its identifiers — start there when you are unsure what a thing is called.
  * **A thing with no term yet needs a term first.** Add the entry to `GLOSSARY.md` in the same
    change — Description, and Implementation naming the identifiers — and then write the code. Do
    not leave the code naming something the glossary cannot name.
  * **Renaming a term renames its identifiers.** If a canonical name changes, the identifiers under
    its Implementation line change with it in the same change, and the old name moves to Aliases so
    that a search for it still lands on the term.
  * **The vocabulary is the same in conversation as it is in code.** When you talk about this
    project in a development session — to the user in chat, in a scratch note, in a plan, in a PR
    or issue body, in a `review_reply` — call each thing by the term `GLOSSARY.md` gives it. This
    holds whatever language the session is conducted in: a term is a name, and a name is not
    translated. The sentence around it takes the session's language; the term does not, and it is
    not transliterated, glossed, or swapped for where the thing happens to sit on the screen. The
    exception below travels with the rule: a wire-format name is spoken the way it is spelled.

    ```text
    Bad:  右のフィードバックパネルにあるコメントカードの解決トグルを押すと…
    Good: Feedback Panel の Comment Card にある Resolve Toggle を押すと…
    ```

    Conversation is where the identifiers come from: a change discussed as「右のパネル」lands in
    the code as `rightPanel`, so the term has to be right in the sentence before it can be right
    in the name.

* **The one exception — wire formats.** A name that has already left the process keeps the spelling
  it was published with: the **Sidecar**'s JSON keys, the **Anchor** string forms
  (`spec-element-N`, `<path>#<start>-<end>`, `<path>#file`), the MCP tool names, the `/api/…` paths,
  and the `-feedback.json` / `-status.json` filenames. A rename there breaks a sidecar written
  yesterday, or an agent built against those names. Where such a name differs from its term, the
  term's entry records the difference; the code does not "fix" it.

* **Why**: the review page is worked on by people and by agents, in prose and in code, across a
  document, a stylesheet and 3,500 lines of template. When one thing carries four names — the left
  column was a `sidebar`, a `contents rail`, a `file list` and a TOC at once — every reader has to
  re-derive which is which, and names drift away from what they name: `scrollToSidebarComment`
  scrolled the feedback panel, not the rail. Naming from one list is what keeps a search for a term
  finding everything about it.
