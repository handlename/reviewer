# reviewer UI Design Principles (UI_DESIGN)

This document records **why the review screen looks and behaves the way it does**. It is the single source of truth for the design system, the layout policy and the interaction model of `references/template.html`.

It is normative. A change that contradicts a principle recorded here is not simply a change to the template — it is a change to this document, and the reasoning that follows the principle is what has to be answered first. Each principle therefore carries the reason it exists and, where one was seriously weighed, the alternative that was rejected.

Related documents, and the line between them:

| Document | Answers |
|---|---|
| **UI_DESIGN.md** (this) | Why the screen looks and behaves as it does |
| [`DESIGN.md`](DESIGN.md) §2-F | How the template is structured and initialized |
| [`DESIGN.md`](DESIGN.md) §3 | Which DOM constraints comment targeting must obey |
| [`GLOSSARY.md`](GLOSSARY.md) | What the vocabulary means |
| [`AGENTS.md`](AGENTS.md) | Which invariants a change must not break |

---

## 1. Constraints on the medium

The review screen is one self-contained `references/template.html`, embedded into the binary with `//go:embed`, written in vanilla HTML, CSS and JavaScript.

**No framework, no build step.** The binary must render a complete, styled page from a single embedded asset; a build pipeline would put a second toolchain between a change and the page it produces, and a framework would put a runtime there too. Everything in this document has to be achievable in plain CSS and plain DOM.

The page does load three things from CDNs — the IBM Plex webfonts, Prism, and Mermaid. They are progressive enhancements: without them the page still renders and every review interaction still works.

---

## 2. The design system

### 2.1 Monochrome, with one accent

Hierarchy comes from **opacity, weight and whitespace — never from a second hue**. The palette is a single stone scale expressed as HSL components (`--fg-hsl`, `--bg-hsl`) so that every level of text and every border can be derived from one colour by opacity alone: `--text-primary` → `--text-body` → `--text-secondary` → `--text-tertiary`, and `--border-color` → `--border-strong`.

There is exactly one accent (`--accent-hsl`), and it is spent **only on interactive affordances** — links, focus, comment state, the info callout's left rule.

**Why:** the reviewer is reading a specification. The body text is the thing they came for, and the two rails around it exist to be ignored until wanted. A palette of saturated hues competes with the content for the same attention. This replaced an earlier dark-navy-and-sky-blue glassmorphism treatment with seven saturated pill badges, and the change was explicitly behaviour-preserving: only the `<head>` and styles were rewritten.

**Rejected:** reintroducing a muted semantic tint for faster colour scanning of badges. It remains a standing offer, not a default.

### 2.2 The one exception: diff tints

A diff's added and removed lines are distinguished by **colour** — a desaturated green and red, tuned down to sit inside the stone palette (`--diff-add-bg`, `--diff-del-bg`, `--diff-add-fg`, `--diff-del-fg`).

**Why:** `+` and `-` are not a level in a hierarchy, they are a difference in meaning, and colour is the fastest channel for that distinction. This is the only place where a hue carries meaning, and it is deliberately the *only* one.

Everything downstream must agree with these four tokens rather than restating them — the file-list status marks and the suggestion diff in the comment panel both take their colour from here, so that green and red mean one thing across the whole page.

### 2.3 Typography

IBM Plex Sans for prose, IBM Plex Mono for code, badges and anything on a numeric column. A fixed modular scale over a 17px reading base with a 1.65 line height.

**Why:** a specification is a long-form reading document before it is an application. A fixed scale keeps that reading rhythm stable no matter what the document contains.

### 2.4 Surfaces and shape

Flat hairline surfaces. Borders at `--border-color` / `--border-strong`, a 3px `--radius`, an 8px `--u` spacing unit. No shadows used as elevation, no translucency used as depth.

**Why:** glassmorphism was removed for the same reason the hue palette was — it draws the eye to the chrome. It also actively broke things: keeping figure overflow visible so comment bubbles are not clipped was part of the same pass.

### 2.5 Badges

Square, monochrome. Priority is expressed by **fill and opacity** (`must` inverts to a solid block, `should` and `could` step down through the text scale, `wont` loses its border and gains a strikethrough); status is expressed by **border style** (`confirmed` solid, `inferred` dashed, `assumption` dotted).

**Why:** two independent, non-colour channels can carry two independent taxonomies without spending the accent, and without asking the reader to memorise seven hues.

---

## 3. Layout

### 3.1 Three columns, the middle one leads

A contents rail on the left, the document in the middle, the feedback panel on the right (served mode only). The rails recede — quieter surfaces, smaller type, secondary text — so the document column reads as the page.

### 3.2 A spec has a reading measure; a diff does not

The document column is capped at a `--measure` of 40rem (~70 characters) and the layout at 1600px, because prose has an optimal measure and exceeding it costs comprehension.

**In diff mode the cap is dropped entirely.** A diff is as wide as its longest line and has no measure to respect; on a large screen the cap was pure loss — horizontal scrolling inside hunks while the window still had room. The column instead takes whatever the two rails leave, floored at `--main-min-width`.

### 3.3 Widths live in custom properties, never in inline styles

`--feedback-panel-width` and `--main-min-width` are read by the stylesheet, by the drag handle and by the media queries alike. JavaScript writes the custom property; it never writes `el.style.width`.

**Why:** an inline style outranks a selector inside a media query. Assigning the width directly would defeat the `width: 100%` that the narrow-viewport layout applies, and the page would scroll sideways on a small screen. One property also means the stylesheet and the resize handle cannot disagree about the same number.

The same reasoning governs **state**: `is-served`, `sidebar-collapsed` and `diff-review` are classes on `<body>`, not inline `display` values, so the narrow-viewport rules can still override them.

### 3.4 The comment panel is the reviewer's to size

The panel's width is draggable, defaults to 520px, and is remembered in `localStorage`. Double-clicking the divider restores the default by **removing** the custom property rather than writing 520px back, so the default keeps living in the stylesheet and only has to change in one place.

**Why:** reviewing a diff needs horizontal room on both sides at once — the code column must stay readable while a comment carrying a suggestion needs enough width to write replacement code in. No single number serves both; how much each side deserves depends on the diff, the screen and the moment. It is remembered because the review loop reloads the page every round, and a width that reset each time would be worse than a fixed one.

**Rejected:** 640px as the default. It was compared against 520px on a real `git diff` in a browser and left long diff lines truncated at a 1500px viewport.

The drag geometry measures the panel's **own** right edge once at drag start. Computing width as `window.innerWidth - clientX` assumes the panel's edge is the window's edge, which it is not: the layout is centred with a cap, and a scrollbar takes its own bite out of `innerWidth`.

### 3.5 The contents rail folds away

`‹` collapses it, `›` restores it, and the state survives a reload.

**Why:** a diff is read across rather than down, so the file list is the first thing worth trading for width.

---

## 4. Comment states: four states, three non-colour axes

The system forbids a second hue, so the four states an element can be in are separated by **position, stroke style and depth** instead.

| State | Channel | Distinguishing axis |
|---|---|---|
| hover | background wash (`--hl-wash`) | — |
| has comments | short tick in the left gutter (`--hl-mark`, offset `--hl-gutter`) | position — outside the box |
| composing | dashed full-perimeter ring | stroke — dashed |
| selected | solid full-perimeter ring (`--hl-ring`) + halo (`--hl-halo`) | stroke — solid; depth — halo |

**Why a full-perimeter ring rather than a left rule:** `.callout` already spends the accent on its semantic `border-left`, so a state that paints a rule in the same place is a visual no-op on exactly the elements that carry the most important content.

**No state may touch `padding` or `border`.** Both shift the content sideways or downwards when the state turns on, which moves the very text under review.

**Rejected:** a selection-only hue (breaks the monochrome system); accent lightness tokens such as `--accent-strong` (same objection, and it multiplies tokens for one state); moving `--callout-info` off the accent (weakens the semantics of info callouts to solve a selection problem).

**Range states are drawn as a band** — one continuous left rule plus a wash across the whole selected range — rather than an outline per row, which reads as separate boxes stacked on each other instead of one selection.

**Table rows are the exception that proves the rule.** The four states must look the same on a table row as on a paragraph, even though a `<tr>` cannot carry the boxes some of them generate. What that costs, and how the declarations are split between the row and its cells, is a DOM constraint rather than a design decision — see [`DESIGN.md` §3](DESIGN.md#3-comment-targeting--dom-traversal-constraints).

**Rejected:** moving comment granularity from the row to the cell, which removes the problem at its root but changes a product decision and breaks anchor compatibility with comments already in a sidecar; and placing the badge inline in the last cell, which abandons the shared "badge in the right gutter" vocabulary and hides the badge behind long cell content.

---

## 5. Interaction

### 5.1 The whole block is the target

In a Markdown review, clicking anywhere on a commentable block targets it. Targeting fires only when mouse-up leaves a collapsed selection, so dragging still selects text; clicks on links and on the `💬 N` indicator are excluded.

**Why:** the original affordance was a hover-only bubble in the right gutter — small, outside the text column, and invisible until hovered. The hover background wash remains as the affordance.

### 5.2 A diff selects line ranges

In a diff review the unit is the line, not the block. Drag or shift-click selects a range; a file header is a target in its own right, for the comments that are about the change to a file rather than to any line of it. The anchor forms, and the constraints that shape them — the anchor on the first line only, no selection across a hunk boundary — are in [`DESIGN.md` §3](DESIGN.md#3-comment-targeting--dom-traversal-constraints).

What matters here is that both are **selections a reviewer makes with the pointer**, so the states in §4 apply to a range as a band and to a file header as a single element.

Focus moves into the composer on **`mouseup`**, not `mousedown`. The browser's own default handling of `mousedown` moves focus after the handler runs, and `preventDefault` there would cost click-drag text selection in the diff.

### 5.3 The connector line carries the mapping

A single SVG overlay draws one line between a selected comment card and its target. Hover previews it dashed; click selects it solid and scrolls the document so the two line up horizontally. When the target scrolls off-screen the endpoint clamps to the viewport edge and fades, so the line reads as slipping away toward the element rather than stopping abruptly. Clicking an element with no comment yet draws the line to the composer.

**Why:** the mapping used to rely on a duplicated `💬 On: …` text snippet inside every comment — easy to miss, and clutter. Because the line now carries it, that label was removed.

**Chosen model (Mode A), from an interactive prototype:** clicking a commented element **selects** its comment; clicking an uncommented element **targets** a new one.

### 5.4 The panel reads top-down alongside the document

Comments render in the appearance order of their target block, derived from the live DOM rather than by parsing anchor strings, so an anchor that no longer resolves is handled naturally instead of resolving to a stale position. Comments with no target, or whose target disappeared after an agent edit, sink to the end in creation order.

**Ordering is applied to the rendering only.** The `comments` array and the feedback file written from it keep creation order, so nothing changes for the agent.

**Rejected:** reordering the array itself (would change the persisted JSON and the order the agent reads); layering by `status` before appearance order (breaks document order into groups).

### 5.5 The composer grows to fit its content

A suggestion fence is as many lines as the range it quotes. The composer grows up to `50vh`, then scrolls rather than pushing Submit and End Review off the screen. Inline comment editing in the panel does the same, sized to the comment it opens with.

### 5.6 Suggestions are opt-in, and applying one is not our job

The ` ```suggestion ` fence is inserted only when the button is pressed, never automatically.

**Why:** most comments are questions. A fence that appeared by itself would be in the way, and it would teach the agent to expect a patch that is not there.

In the panel a suggestion renders as a **diff against the lines it replaces**, using the same tints as the diff body, with shared lines at either end kept as context — so editing one line inside a six-line quote shows as one change rather than six removals and six additions. It wraps long lines instead of scrolling them, because the panel is narrow and a suggestion is short.

reviewer never edits source. Applying a suggestion is the agent's job. Marking a comment resolved is the human's alone, which is why that control exists only here, on the page.

### 5.7 A comment is a thread, and both sides speak in it

A comment card renders the whole exchange: the human's own remark as the head, then every message after it, attributed and timestamped, in the order it was said. The two authors are separated by **depth** — the agent's message is a filled block, the human's answer is unfilled — never by a second hue (§2.1).

A **Reply** control sits under a thread that has started. It is a text-weight button until it is pressed, and only then a composer, so a thread at rest stays as quiet as it was before it could be replied to. Ctrl/Cmd+Enter sends and Escape cancels, matching the composer and inline editing (§8).

**A thread nobody has answered yet renders exactly as it did before threading** — no Reply control, no resolve toggle. Both appear with the first message, which is also the point at which there is something to resolve.

**A reply posts nothing by itself.** It is held with the rest of the comments and travels on the next Submit, keeping review's batch rhythm: one submit, one round.

**Rejected:** a dedicated "answer this question" control (a second posting path that buys nothing over the reply the human is already writing); per-message ids and reply-to targeting (a heavier schema and UI for the rare thread carrying two unanswered questions).

---

## 6. Rendering a diff

### 6.1 One line-number column

A single column shows each line's number on its own side — the old file's for a deletion, the new file's otherwise — with a deletion's number dimmed to mark the change of coordinate system.

**Why:** a deletion has no number on the new side, and leaving the cell blank hides which line a comment is about.

### 6.2 The hunk is the horizontal scroll unit

**Why:** scrolling per line lets rows slide independently and destroys the column alignment a diff is read by. Scrolling the whole file card would carry its sticky path header away with it.

The `💬` badge sits sticky at the right edge of the scrollport rather than hanging in the right gutter, because the hunk scrolls horizontally and would clip it.

### 6.3 Syntax highlighting: per file, per line, on demand

The grammar is chosen **per file** from its path — the only signal a diff carries, and one page routinely mixes Go, Markdown and YAML. Highlighting runs **per line, as lines scroll into view**.

**Why per line:** the DOM is one row per line because that is what line-range anchoring needs, and a diff is a list of fragments rather than a document, so there is no whole-file parse to share. **Why on demand:** a thousand-line diff should not pay for all of it before first paint.

**Accepted cost:** a construct spanning lines — a block comment, a multi-line string — is tokenized without its context and can be coloured as if its lines stood alone. GitHub has the same property for the same reason. Accepting it is what keeps line-range anchoring possible.

The extension → grammar map is explicit rather than "pass the extension to Prism and hope": adding a language is one line, and an unmapped one degrades to plain text, never to a broken page.

### 6.4 File status is a mark, not a word

In the file list the status is a fixed-width glyph in front of the name — `+` added, `−` deleted, `⇄` renamed, `·` modified — so the names line up in one column and the list can be scanned rather than read. The words are not thrown away: they stay in full on the file header in the document, and follow the mark as its `title`.

Marks are glyphs, not an icon font or inline SVG: they inherit the monospace column and the text colour, and the page loads nothing new for them.

`−` is U+2212 MINUS SIGN, not a hyphen, so it pairs with `+` at the same weight and width.

---

## 7. Theme

Light and dark are both first-class, driven by `prefers-color-scheme` alone — there is no in-page toggle. The dark theme redefines only the tokens, never the rules.

Everything the page loads must follow: Mermaid is initialized with `dark` or `neutral` rather than a fixed white canvas, and Prism's two themes are swapped by a `media` attribute on their stylesheets.

Because there is no toggle, anything that captures the page (a screenshot, a print) has to select the scheme on the browser side.

---

## 8. Accessibility and motion

Every control reachable by pointer is reachable by keyboard: the edit and delete affordances carry `tabindex` and handle Enter and Space, and the composer saves on Ctrl/Cmd+Enter and cancels on Escape. `:focus-visible` is styled with the accent rather than suppressed, and `prefers-reduced-motion` is honoured — including by the connector line.

User-supplied text is inserted with `textContent`, never `innerHTML`.

---

## 9. Verification

There is no browser test harness. Several of the constraints above exist precisely because they are invisible to the Go test suite — the anonymous-table-cell rule, the drag geometry, two handlers firing on one gesture.

Changes to the review screen are therefore verified **by eye, in a real browser, in both light and dark themes**, against `examples/sample.md` and `examples/sample.diff`:

```console
$ go run ./cmd/reviewer serve examples/sample.md -p 8080
$ go run ./cmd/reviewer serve examples/sample.diff -p 8080
```

Whether that is enough is a fair standing question; adding a harness has so far been judged disproportionate to the size of the fixes involved.
