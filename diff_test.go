package reviewer

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Golden files record what the diff renderer emits. Regenerate them with
//
//	go test ./... -run TestRenderDiffBody -update
//
// and read the resulting diff before committing it.
var update = flag.Bool("update", false, "rewrite the golden files under testdata/")

func TestDetectKind(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    Kind
	}{
		{
			name: "git diff",
			content: `diff --git a/main.go b/main.go
index 1111111..2222222 100644
--- a/main.go
+++ b/main.go
@@ -1,3 +1,3 @@
 package main
-func old() {}
+func new() {}
`,
			want: KindDiff,
		},
		{
			name: "plain diff -u without a git header",
			content: `--- a/main.go	2026-01-01 00:00:00
+++ b/main.go	2026-01-02 00:00:00
@@ -1 +1 @@
-old
+new
`,
			want: KindDiff,
		},
		{
			name: "hunk header alone",
			content: `@@ -10,4 +10,4 @@ func RenderSpec() {
 ctx
-old
+new
`,
			want: KindDiff,
		},
		{
			name: "markdown",
			content: `---
title: Spec
---

# Heading

Some prose with a --- rule below.

---
`,
			want: KindMarkdown,
		},
		{
			// The trap: a spec that quotes a diff is still a spec. Reading it as a diff would
			// strip its formatting and orphan every existing comment anchor.
			name:    "markdown containing a diff in a fenced code block",
			content: "# Spec\n\nSee the change:\n\n```diff\ndiff --git a/main.go b/main.go\n--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n-old\n+new\n```\n\nDone.\n",
			want:    KindMarkdown,
		},
		{
			name:    "markdown with a tilde fence",
			content: "# Spec\n\n~~~\n@@ -1 +1 @@\n~~~\n",
			want:    KindMarkdown,
		},
		{
			name:    "empty",
			content: "",
			want:    KindMarkdown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectKind([]byte(tt.content)); got != tt.want {
				t.Errorf("DetectKind() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseUnifiedDiffSingleFile(t *testing.T) {
	files, err := ParseUnifiedDiff([]byte(`diff --git a/main.go b/main.go
index 1111111..2222222 100644
--- a/main.go
+++ b/main.go
@@ -10,4 +10,4 @@ func main() {
 ctx
-old line
+new line
 ctx2
`))
	if err != nil {
		t.Fatalf("ParseUnifiedDiff() error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1", len(files))
	}
	f := files[0]
	if f.OldPath != "main.go" || f.NewPath != "main.go" {
		t.Errorf("paths = %q/%q, want main.go/main.go", f.OldPath, f.NewPath)
	}
	if len(f.Hunks) != 1 {
		t.Fatalf("got %d hunks, want 1", len(f.Hunks))
	}
	if got, want := f.Hunks[0].Header, "@@ -10,4 +10,4 @@ func main() {"; got != want {
		t.Errorf("hunk header = %q, want %q", got, want)
	}

	want := []Line{
		{Kind: LineContext, Content: "ctx", OldNo: 10, NewNo: 10},
		{Kind: LineDelete, Content: "old line", OldNo: 11},
		{Kind: LineAdd, Content: "new line", NewNo: 11},
		{Kind: LineContext, Content: "ctx2", OldNo: 12, NewNo: 12},
	}
	assertLines(t, f.Lines(), want)
}

func TestParseUnifiedDiffFileVariants(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		wantFiles   int
		wantDisplay []string
	}{
		{
			name: "multiple files",
			content: `diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1 +1 @@
-a
+A
diff --git a/b.go b/b.go
--- a/b.go
+++ b/b.go
@@ -1 +1 @@
-b
+B
`,
			wantFiles:   2,
			wantDisplay: []string{"a.go", "b.go"},
		},
		{
			name: "added file",
			content: `diff --git a/new.go b/new.go
new file mode 100644
index 0000000..1111111
--- /dev/null
+++ b/new.go
@@ -0,0 +1,2 @@
+package main
+func main() {}
`,
			wantFiles:   1,
			wantDisplay: []string{"new.go"},
		},
		{
			// Two deletions in one diff both have /dev/null on the new side. Falling back to the
			// old path is what keeps their anchors apart.
			name: "two deleted files keep distinct display paths",
			content: `diff --git a/gone.go b/gone.go
deleted file mode 100644
--- a/gone.go
+++ /dev/null
@@ -1 +0,0 @@
-package main
diff --git a/also-gone.go b/also-gone.go
deleted file mode 100644
--- a/also-gone.go
+++ /dev/null
@@ -1 +0,0 @@
-package main
`,
			wantFiles:   2,
			wantDisplay: []string{"gone.go", "also-gone.go"},
		},
		{
			name: "rename with edits is found under its new path",
			content: `diff --git a/old/name.go b/new/name.go
similarity index 90%
rename from old/name.go
rename to new/name.go
--- a/old/name.go
+++ b/new/name.go
@@ -1 +1 @@
-package old
+package new
`,
			wantFiles:   1,
			wantDisplay: []string{"new/name.go"},
		},
		{
			name: "mode change only, no hunks",
			content: `diff --git a/script.sh b/script.sh
old mode 100644
new mode 100755
`,
			wantFiles:   1,
			wantDisplay: []string{"script.sh"},
		},
		{
			name:        "empty input",
			content:     "",
			wantFiles:   0,
			wantDisplay: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files, err := ParseUnifiedDiff([]byte(tt.content))
			if err != nil {
				t.Fatalf("ParseUnifiedDiff() error = %v", err)
			}
			if len(files) != tt.wantFiles {
				t.Fatalf("got %d files, want %d", len(files), tt.wantFiles)
			}
			for i, want := range tt.wantDisplay {
				if got := files[i].DisplayPath(); got != want {
					t.Errorf("file %d display path = %q, want %q", i, got, want)
				}
			}
		})
	}
}

// Status is what the sidebar draws its mark from, so the four cases are pinned down here
// rather than left to the renderer's golden files alone.
func TestFileStatus(t *testing.T) {
	tests := []struct {
		name string
		file File
		want FileStatus
	}{
		{name: "added", file: File{OldPath: devNull, NewPath: "new.go"}, want: FileAdded},
		{name: "deleted", file: File{OldPath: "gone.go", NewPath: devNull}, want: FileDeleted},
		{name: "renamed", file: File{OldPath: "old/name.go", NewPath: "new/name.go"}, want: FileRenamed},
		{name: "modified", file: File{OldPath: "main.go", NewPath: "main.go"}, want: FileModified},
		// A header-only entry (a mode change) names the same file on both sides.
		{name: "mode change only", file: File{OldPath: "script.sh", NewPath: "script.sh"}, want: FileModified},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.file.Status(); got != tt.want {
				t.Errorf("Status() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseUnifiedDiffNoNewlineMarker(t *testing.T) {
	files, err := ParseUnifiedDiff([]byte(`diff --git a/a.txt b/a.txt
--- a/a.txt
+++ b/a.txt
@@ -1 +1 @@
-old
\ No newline at end of file
+new
`))
	if err != nil {
		t.Fatalf("ParseUnifiedDiff() error = %v", err)
	}
	assertLines(t, files[0].Lines(), []Line{
		{Kind: LineDelete, Content: "old", OldNo: 1},
		{Kind: LineMeta, Content: "No newline at end of file"},
		{Kind: LineAdd, Content: "new", NewNo: 1},
	})
}

// A combined diff has one column per parent, so a line has no single before/after and the
// anchor coordinate system cannot describe it. Rejecting it beats guessing.
func TestParseUnifiedDiffRejectsCombinedDiff(t *testing.T) {
	_, err := ParseUnifiedDiff([]byte(`diff --cc merged.go
index 1111111,2222222..3333333
--- a/merged.go
+++ b/merged.go
@@@ -1,2 -1,2 +1,2 @@@
- one
 -two
++three
`))
	if err == nil {
		t.Fatal("want an error for a combined diff, got nil")
	}
	if !strings.Contains(err.Error(), "combined") {
		t.Errorf("error = %v, want it to mention combined diffs", err)
	}
}

func TestDiffAnchorRoundTrip(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		start, end int
		anchor     string
	}{
		{name: "single line", path: "render.go", start: 3, end: 3, anchor: "render.go#3-3"},
		{name: "range", path: "cli/command/serve.go", start: 12, end: 15, anchor: "cli/command/serve.go#12-15"},
		// The split is on the last '#', so a path that contains one still reads back whole.
		{name: "path containing a hash", path: "notes/a#1-2.md", start: 3, end: 4, anchor: "notes/a#1-2.md#3-4"},
		{name: "path containing a colon", path: "weird:name.go", start: 1, end: 2, anchor: "weird:name.go#1-2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatDiffAnchor(tt.path, tt.start, tt.end)
			if got != tt.anchor {
				t.Fatalf("FormatDiffAnchor() = %q, want %q", got, tt.anchor)
			}
			path, start, end, ok := ParseDiffAnchor(got)
			if !ok {
				t.Fatalf("ParseDiffAnchor(%q) reported not-an-anchor", got)
			}
			if path != tt.path || start != tt.start || end != tt.end {
				t.Errorf("ParseDiffAnchor() = %q,%d,%d; want %q,%d,%d", path, start, end, tt.path, tt.start, tt.end)
			}
		})
	}
}

// Anything that is not a diff anchor has to be recognisable as such, because the Markdown
// review shares every code path that handles anchors.
func TestParseDiffAnchorRejectsNonDiffAnchors(t *testing.T) {
	for _, anchor := range []string{
		"spec-element-7",
		"",
		"main.go#",
		"main.go#12",
		"main.go#L12-L15",
		"main.go#0-3",  // 1-based; 0 is not a line
		"main.go#5-3",  // end before start
		"main.go#1-2x", // trailing junk
		"main.go# 1-2", // space is not a digit
		"main.go#1--2", // not two numbers
	} {
		if _, _, _, ok := ParseDiffAnchor(anchor); ok {
			t.Errorf("ParseDiffAnchor(%q) accepted a non-anchor", anchor)
		}
	}
}

func TestDiffFileAnchorRoundTrip(t *testing.T) {
	for _, path := range []string{"render.go", "cli/command/serve.go", "notes/a#1-2.md"} {
		anchor := FormatDiffFileAnchor(path)
		got, ok := ParseDiffFileAnchor(anchor)
		if !ok {
			t.Fatalf("ParseDiffFileAnchor(%q) reported not-an-anchor", anchor)
		}
		if got != path {
			t.Errorf("ParseDiffFileAnchor(%q) = %q, want %q", anchor, got, path)
		}
		// The two anchor forms must not be mistaken for each other, in either direction.
		if _, _, _, ok := ParseDiffAnchor(anchor); ok {
			t.Errorf("ParseDiffAnchor accepted the whole-file anchor %q", anchor)
		}
		if _, ok := ParseDiffFileAnchor(FormatDiffAnchor(path, 1, 2)); ok {
			t.Errorf("ParseDiffFileAnchor accepted the line-range anchor for %q", path)
		}
	}

	for _, anchor := range []string{"spec-element-7", "", "main.go#files", "main.go#FILE", "file"} {
		if _, ok := ParseDiffFileAnchor(anchor); ok {
			t.Errorf("ParseDiffFileAnchor(%q) accepted a non-anchor", anchor)
		}
	}
}

func TestRenderDiffBody(t *testing.T) {
	names, err := filepath.Glob(filepath.Join("testdata", "*.diff"))
	if err != nil {
		t.Fatal(err)
	}
	if len(names) == 0 {
		t.Fatal("no sample diffs under testdata/")
	}

	for _, name := range names {
		t.Run(filepath.Base(name), func(t *testing.T) {
			content, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			files, err := ParseUnifiedDiff(content)
			if err != nil {
				t.Fatalf("ParseUnifiedDiff() error = %v", err)
			}
			got := renderDiffBody(files)

			golden := strings.TrimSuffix(name, ".diff") + ".golden.html"
			if *update {
				if err := os.WriteFile(golden, []byte(got), 0644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("missing golden file (run with -update): %v", err)
			}
			if got != string(want) {
				t.Errorf("rendered body does not match %s\n--- got ---\n%s", golden, got)
			}
		})
	}
}

// Escaping is not optional on this path: nothing upstream of RenderDiff produces HTML, so every
// diff-derived string reaches the page raw unless it is escaped here.
func TestRenderDiffEscapesEveryDiffDerivedString(t *testing.T) {
	files, err := ParseUnifiedDiff([]byte(`diff --git a/x">.go b/x">.go
--- a/x">.go
+++ b/x">.go
@@ -55,10 +80,11 @@ func (s *ReviewSession) Done() <-chan struct{} {
-	fmt.Println("<script>alert(1)</script>")
+	fmt.Println("safe & sound")
`))
	if err != nil {
		t.Fatalf("ParseUnifiedDiff() error = %v", err)
	}

	body := renderDiffBody(files)
	for _, unwanted := range []string{
		"<script>alert(1)</script>", // line content in a text node
		`<-chan`,                    // hunk header carrying a channel type
		`data-file="x">.go"`,        // a quote in a path would break out of the attribute
	} {
		if strings.Contains(body, unwanted) {
			t.Errorf("unescaped %q survived into the rendered body", unwanted)
		}
	}
	for _, wanted := range []string{
		"&lt;script&gt;alert(1)&lt;/script&gt;",
		"&lt;-chan",
		"safe &amp; sound",
		`data-file="x&#34;&gt;.go"`,
	} {
		if !strings.Contains(body, wanted) {
			t.Errorf("expected escaped %q in the rendered body", wanted)
		}
	}
}

// Render is the single entry point every call site uses; it has to pick the renderer itself.
func TestRenderDispatchesOnContent(t *testing.T) {
	diffHTML, err := Render([]byte(`diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1 +1 @@
-old
+new
`), "")
	if err != nil {
		t.Fatalf("Render(diff) error = %v", err)
	}
	if !strings.Contains(string(diffHTML), `class="diff-line`) {
		t.Error("a diff did not render as a diff")
	}
	if strings.Contains(string(diffHTML), "initializeCommentableElements();") {
		t.Error("diff mode must not run the Markdown block-comment initializer")
	}

	mdHTML, err := Render([]byte("# Heading\n\nProse.\n"), "")
	if err != nil {
		t.Fatalf("Render(markdown) error = %v", err)
	}
	if !strings.Contains(string(mdHTML), "<h1>Heading</h1>") {
		t.Error("markdown did not render as markdown")
	}
	if !strings.Contains(string(mdHTML), "initializeCommentableElements();") {
		t.Error("markdown mode lost the block-comment initializer")
	}
}

func TestDiffStats(t *testing.T) {
	files, err := ParseUnifiedDiff([]byte(`diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1,2 +1,3 @@
 ctx
-old
+new
+extra
`))
	if err != nil {
		t.Fatalf("ParseUnifiedDiff() error = %v", err)
	}
	if got, want := diffStats(files), "1 file · +2 −1"; got != want {
		t.Errorf("diffStats() = %q, want %q", got, want)
	}
}

func assertLines(t *testing.T, got, want []Line) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestWhitespaceOnlyMask(t *testing.T) {
	tests := []struct {
		name  string
		lines []Line
		want  []bool
	}{
		{
			name: "indent-only change folds both sides",
			lines: []Line{
				{Kind: LineDelete, Content: "\t\tfoo(bar)"},
				{Kind: LineAdd, Content: "        foo(bar)"},
			},
			want: []bool{true, true},
		},
		{
			name: "inner spacing change folds both sides",
			lines: []Line{
				{Kind: LineDelete, Content: "foo( a , b )"},
				{Kind: LineAdd, Content: "foo(a, b)"},
			},
			want: []bool{true, true},
		},
		{
			name: "trailing whitespace removal folds both sides",
			lines: []Line{
				{Kind: LineDelete, Content: "foo()   "},
				{Kind: LineAdd, Content: "foo()"},
			},
			want: []bool{true, true},
		},
		{
			name: "a real edit is never folded",
			lines: []Line{
				{Kind: LineDelete, Content: "foo()"},
				{Kind: LineAdd, Content: "bar()"},
			},
			want: []bool{false, false},
		},
		{
			// The safe-side approximation: unequal block lengths mean the 1:1 pairing is not
			// trustworthy, so nothing in the block folds even though two rows would match.
			name: "unequal block lengths fold nothing",
			lines: []Line{
				{Kind: LineDelete, Content: "\tfoo()"},
				{Kind: LineDelete, Content: "\tbar()"},
				{Kind: LineAdd, Content: "    foo()"},
				{Kind: LineAdd, Content: "    bar()"},
				{Kind: LineAdd, Content: "    baz()"},
			},
			want: []bool{false, false, false, false, false},
		},
		{
			name: "an addition with no deletion to pair with never folds",
			lines: []Line{
				{Kind: LineContext, Content: "func foo() {"},
				{Kind: LineAdd, Content: ""},
				{Kind: LineContext, Content: "\treturn"},
			},
			want: []bool{false, false, false},
		},
		{
			name: "a deletion with no addition to pair with never folds",
			lines: []Line{
				{Kind: LineDelete, Content: "\tfoo()"},
				{Kind: LineContext, Content: "\tbar()"},
			},
			want: []bool{false, false},
		},
		{
			name: "a mixed block folds only when every pair is whitespace-only",
			lines: []Line{
				{Kind: LineDelete, Content: "\tfoo()"},
				{Kind: LineDelete, Content: "\tbar()"},
				{Kind: LineAdd, Content: "    foo()"},
				{Kind: LineAdd, Content: "    baz()"},
			},
			want: []bool{false, false, false, false},
		},
		{
			name: "two separate blocks are judged independently",
			lines: []Line{
				{Kind: LineDelete, Content: "\tfoo()"},
				{Kind: LineAdd, Content: "    foo()"},
				{Kind: LineContext, Content: "\tsep()"},
				{Kind: LineDelete, Content: "old()"},
				{Kind: LineAdd, Content: "new()"},
			},
			want: []bool{true, true, false, false, false},
		},
		{
			// "\ No newline at end of file" carries meaning about the line it follows, so it
			// breaks the run rather than being paired over.
			name: "a meta line breaks the block",
			lines: []Line{
				{Kind: LineDelete, Content: "\tfoo()"},
				{Kind: LineMeta, Content: `\ No newline at end of file`},
				{Kind: LineAdd, Content: "    foo()"},
			},
			want: []bool{false, false, false},
		},
		{
			name:  "no lines",
			lines: nil,
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := whitespaceOnlyMask(tt.lines)
			if len(got) != len(tt.want) {
				t.Fatalf("whitespaceOnlyMask() length = %d, want %d", len(got), len(tt.want))
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("whitespaceOnlyMask()[%d] = %v, want %v (content %q)", i, got[i], tt.want[i], tt.lines[i].Content)
				}
			}
		})
	}
}

func TestRenderDiffBodyMarksWhitespaceOnlyLines(t *testing.T) {
	files, err := ParseUnifiedDiff([]byte(`diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,4 +1,4 @@
 package main
-	foo(bar)
-	old()
+        foo(bar)
+        new()
`))
	if err != nil {
		t.Fatalf("ParseUnifiedDiff() error = %v", err)
	}

	body := renderDiffBody(files)

	// Nothing folds: the block pairs 1:1 but old()/new() is a real edit, so the whole block
	// stays visible.
	if strings.Contains(body, "data-ws-only") {
		t.Errorf("renderDiffBody() marked a block containing a real edit as whitespace-only:\n%s", body)
	}
}

func TestRenderDiffBodyKeepsLineIndicesStableWhenFolding(t *testing.T) {
	files, err := ParseUnifiedDiff([]byte(`diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,3 +1,3 @@
 package main
-	foo(bar)
+        foo(bar)
`))
	if err != nil {
		t.Fatalf("ParseUnifiedDiff() error = %v", err)
	}

	body := renderDiffBody(files)

	for _, want := range []string{
		`data-line-index="1"`,
		`data-line-index="2"`,
		`data-line-index="3"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("renderDiffBody() dropped %s; folding must not renumber lines:\n%s", want, body)
		}
	}
	if strings.Count(body, "data-ws-only") != 2 {
		t.Errorf("renderDiffBody() marked %d lines whitespace-only, want 2 (the -/+ pair):\n%s",
			strings.Count(body, "data-ws-only"), body)
	}
}

// hunkFromShape builds a hunk whose lines have the kinds the shape names, one rune per line:
// "." context, "+" addition, "-" deletion, "\" meta. Each line's content is its index, so no
// two lines are equal and a block's edges are unambiguous in a failure message.
func hunkFromShape(shape string) Hunk {
	h := Hunk{Header: "@@ -1 +1 @@", OldStart: 1, NewStart: 1}
	for i, r := range shape {
		kind := LineContext
		switch r {
		case '+':
			kind = LineAdd
		case '-':
			kind = LineDelete
		case '\\':
			kind = LineMeta
		}
		h.Lines = append(h.Lines, Line{Kind: kind, Content: strconv.Itoa(i)})
	}
	return h
}

func ctxLines(n int) string { return strings.Repeat(".", n) }

func assertBlocks(t *testing.T, got, want []Block) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d blocks %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("block %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestChangeBlocks(t *testing.T) {
	tests := []struct {
		name  string
		shape string
		want  []Block
	}{
		{
			name:  "a hunk with no change has no block",
			shape: ctxLines(50),
			want:  nil,
		},
		{
			name:  "the block is clamped at both ends of a short hunk",
			shape: "..+..",
			want:  []Block{{0, 4}},
		},
		{
			name:  "a lone change keeps blockContext lines on each side",
			shape: ctxLines(10) + "+" + ctxLines(10),
			want:  []Block{{7, 13}},
		},
		{
			// Five unchanged lines between the changes: git -U3 keeps these in one hunk.
			name:  "changes five lines apart are one block",
			shape: ctxLines(10) + "+" + ctxLines(5) + "+" + ctxLines(10),
			want:  []Block{{7, 19}},
		},
		{
			// Six is the widest gap git -U3 still merges across.
			name:  "changes six lines apart are one block",
			shape: ctxLines(10) + "+" + ctxLines(6) + "+" + ctxLines(10),
			want:  []Block{{7, 20}},
		},
		{
			// Seven is where git -U3 splits the hunk, so the blocks split too.
			name:  "changes seven lines apart are two blocks",
			shape: ctxLines(10) + "+" + ctxLines(7) + "+" + ctxLines(10),
			want:  []Block{{7, 13}, {15, 21}},
		},
		{
			name:  "a deletion anchors a block just as an addition does",
			shape: ctxLines(10) + "-" + ctxLines(10),
			want:  []Block{{7, 13}},
		},
		{
			// The meta line sits one past the block's edge and is meaningless alone, so the
			// block stretches to keep it with the line it annotates.
			name:  "a meta line just past the edge joins the block",
			shape: "+..." + `\` + ctxLines(50),
			want:  []Block{{0, 4}},
		},
		{
			// Far from any change the meta line inherits the context around it and stays out.
			name:  "a meta line far from a change stays outside",
			shape: "+" + ctxLines(50) + `\`,
			want:  []Block{{0, 3}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertBlocks(t, hunkFromShape(tt.shape).ChangeBlocks(), tt.want)
		})
	}
}

func TestCollapsedRuns(t *testing.T) {
	tests := []struct {
		name  string
		shape string
		want  []Block
	}{
		{
			name:  "a hunk with no change folds nothing",
			shape: ctxLines(500),
			want:  nil,
		},
		{
			name:  "a short hunk folds nothing",
			shape: ctxLines(10) + "+" + ctxLines(10),
			want:  nil,
		},
		{
			name:  "context on both sides of a change folds away",
			shape: ctxLines(100) + "+" + ctxLines(100),
			want:  []Block{{0, 96}, {104, 200}},
		},
		{
			name:  "the run between two distant changes folds away",
			shape: "+" + ctxLines(100) + "+",
			want:  []Block{{4, 97}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertBlocks(t, hunkFromShape(tt.shape).CollapsedRuns(), tt.want)
		})
	}
}

// Folding turns on two things, so both are pinned.
//
// The licence comes first: a hunk that holds no run longer than wideGap folds nothing at all,
// however many runs it has that are longer than minCollapsedRun. That is what leaves an ordinary
// diff untouched.
func TestNothingFoldsWithoutAWideGap(t *testing.T) {
	// Two changes far enough apart to leave a 30-line gap — well past minCollapsedRun, well
	// short of wideGap.
	shape := "+" + ctxLines(36) + "+" + ctxLines(10)
	h := hunkFromShape(shape)

	gaps := h.uncoveredRuns()
	if len(gaps) == 0 {
		t.Fatal("fixture leaves no gap at all")
	}
	for _, g := range gaps {
		if g.Len() > wideGap {
			t.Fatalf("fixture has a gap of %d lines, past wideGap=%d; it is meant to stay under", g.Len(), wideGap)
		}
		if g.Len() <= minCollapsedRun {
			continue
		}
		// There is at least one gap the threshold alone would have folded.
		if got := h.CollapsedRuns(); len(got) != 0 {
			t.Errorf("folded %v without a run past wideGap=%d", got, wideGap)
		}
		return
	}
	t.Fatalf("fixture has no gap past minCollapsedRun=%d, so it cannot test the licence", minCollapsedRun)
}

// Once a hunk has that licence, minCollapsedRun decides which of its gaps are worth hiding. The
// boundary is pinned from both sides: a run of exactly minCollapsedRun stays.
func TestCollapsedRunsThreshold(t *testing.T) {
	for _, runLen := range []int{minCollapsedRun - 1, minCollapsedRun, minCollapsedRun + 1} {
		// A gap of runLen lines, then a change, then a gap wide enough to unlock the fold.
		shape := "+" + ctxLines(runLen+2*blockContext) + "+" + ctxLines(wideGap+2*blockContext)
		got := hunkFromShape(shape).CollapsedRuns()

		wantFold := runLen > minCollapsedRun
		foldedTheShortGap := false
		for _, r := range got {
			if r.Len() == runLen {
				foldedTheShortGap = true
			}
		}
		if foldedTheShortGap != wantFold {
			t.Errorf("a run of %d lines: folded = %v, want %v (got %v)", runLen, foldedTheShortGap, wantFold, got)
		}
		// The wide gap is what gave the hunk its licence, so it folds either way.
		if len(got) == 0 {
			t.Errorf("a run of %d lines: nothing folded at all, so the wide gap was missed", runLen)
		}
	}
}

func TestIsFoldable(t *testing.T) {
	if hunkFromShape(ctxLines(10) + "+" + ctxLines(10)).IsFoldable() {
		t.Error("a hunk with nothing long enough to fold reports itself foldable")
	}
	if !hunkFromShape(ctxLines(100) + "+" + ctxLines(100)).IsFoldable() {
		t.Error("a hunk with a long run of context reports itself unfoldable")
	}
}

// A mode-only change carries no hunk at all. Reaching for one would panic, and the fold has to
// stay quiet rather than crash the render of every other file in the diff.
func TestFoldOnFileWithoutHunks(t *testing.T) {
	files, err := ParseUnifiedDiff([]byte(`diff --git a/mode-only.sh b/mode-only.sh
old mode 100644
new mode 100755
`))
	if err != nil {
		t.Fatalf("ParseUnifiedDiff() error = %v", err)
	}
	f := files[0]
	if len(f.Hunks) != 0 {
		t.Fatalf("got %d hunks, want 0", len(f.Hunks))
	}
	if got := f.ChangeBlocks(); got != nil {
		t.Errorf("ChangeBlocks() = %v, want nil", got)
	}
	// The path that would actually panic is the renderer walking the file's hunks.
	if body := renderDiffBody([]File{f}); !strings.Contains(body, "No textual changes.") {
		t.Errorf("renderDiffBody() lost the no-change note for a file with no hunk:\n%s", body)
	}
}

// The file-level views count in Rendered Line Index, the same 1-based coordinate a comment
// anchor uses, and they keep counting across hunk boundaries.
func TestFileChangeBlocksAreRenderedLineIndices(t *testing.T) {
	f := File{Hunks: []Hunk{
		hunkFromShape(ctxLines(10) + "+" + ctxLines(10)),
		hunkFromShape(ctxLines(10) + "+" + ctxLines(10)),
	}}
	assertBlocks(t, f.ChangeBlocks(), []Block{{8, 14}, {29, 35}})
}

func TestHunkRecordsItsStartLines(t *testing.T) {
	files, err := ParseUnifiedDiff([]byte(`diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -53,7 +61,7 @@ func f() {
 ctx
-old
+new
`))
	if err != nil {
		t.Fatalf("ParseUnifiedDiff() error = %v", err)
	}
	h := files[0].Hunks[0]
	if h.OldStart != 53 || h.NewStart != 61 {
		t.Errorf("hunk starts = %d/%d, want 53/61", h.OldStart, h.NewStart)
	}
}

// A Change Block is meant to be exactly a `git diff -U3` hunk. That is the claim the narrow-diff
// search rule in ReAnchor rests on, and it is an arithmetic claim about git's own hunk-merging,
// so it is checked against git itself. Re-deriving the boundaries from a second copy of the same
// formula would prove nothing.
//
// reviewer never runs git. This test does, because git is the oracle the claim is about.
func TestChangeBlocksMatchNarrowDiffHunks(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH; this test needs it as the oracle")
	}

	tests := []struct {
		name    string
		changed []int // 1-based lines to rewrite
	}{
		{name: "a lone change", changed: []int{50}},
		{name: "changes five lines apart merge", changed: []int{50, 56}},
		{name: "changes six lines apart merge", changed: []int{50, 57}},
		{name: "changes seven lines apart split", changed: []int{50, 58}},
		{name: "a change at the first line", changed: []int{1}},
		{name: "a change at the last line", changed: []int{100}},
		{name: "changes at both ends", changed: []int{1, 100}},
		{name: "a chain of changes that merges throughout", changed: []int{20, 26, 32, 38}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			old := make([]string, 100)
			for i := range old {
				old[i] = fmt.Sprintf("line %d", i+1)
			}
			updated := slices.Clone(old)
			for _, n := range tt.changed {
				updated[n-1] = fmt.Sprintf("CHANGED %d", n)
			}
			writeLines(t, filepath.Join(dir, "old.txt"), old)
			writeLines(t, filepath.Join(dir, "new.txt"), updated)

			narrow := gitDiff(t, dir, "-U3")
			whole := gitDiff(t, dir, "-U100000")

			got := blockRangesOnNewSide(whole.Hunks[0], whole.Hunks[0].ChangeBlocks())
			want := hunkRangesOnNewSide(narrow.Hunks)

			if len(got) != len(want) {
				t.Fatalf("got %d Change Blocks %v, want %d -U3 hunks %v", len(got), got, len(want), want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("Change Block %d covers new lines %v, -U3 hunk covers %v", i, got[i], want[i])
				}
			}
		})
	}
}

func writeLines(t *testing.T, path string, lines []string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(%s) = %v", path, err)
	}
}

// gitDiff runs `git diff --no-index` at one context width and parses the result. --no-index
// makes git compare two plain paths, so no repository is involved.
func gitDiff(t *testing.T, dir, width string) File {
	t.Helper()
	cmd := exec.Command("git", "diff", "--no-index", width, "--", "old.txt", "new.txt")
	cmd.Dir = dir
	out, err := cmd.Output()
	// git exits 1 when the files differ, which is the only case this test uses.
	var exit *exec.ExitError
	if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1) {
		t.Fatalf("git diff %s = %v", width, err)
	}
	files, err := ParseUnifiedDiff(out)
	if err != nil {
		t.Fatalf("ParseUnifiedDiff(%s) = %v", width, err)
	}
	if len(files) != 1 {
		t.Fatalf("git diff %s produced %d files, want 1", width, len(files))
	}
	return files[0]
}

// The two diffs number their rendered lines differently, so they are compared on the one
// coordinate they share: the line numbers of the new file.
type newSideRange struct{ First, Last int }

func hunkRangesOnNewSide(hunks []Hunk) []newSideRange {
	var out []newSideRange
	for _, h := range hunks {
		n := 0
		for _, l := range h.Lines {
			if l.Kind != LineDelete && l.Kind != LineMeta {
				n++
			}
		}
		out = append(out, newSideRange{First: h.NewStart, Last: h.NewStart + n - 1})
	}
	return out
}

func blockRangesOnNewSide(h Hunk, blocks []Block) []newSideRange {
	var out []newSideRange
	for _, b := range blocks {
		r := newSideRange{}
		for _, l := range h.Lines[b.Start : b.End+1] {
			if l.NewNo == 0 {
				continue
			}
			if r.First == 0 || l.NewNo < r.First {
				r.First = l.NewNo
			}
			if l.NewNo > r.Last {
				r.Last = l.NewNo
			}
		}
		out = append(out, r)
	}
	return out
}

// wholeFileDiff renders a file of n lines with the given 1-based lines rewritten, as a diff
// that carries the whole file.
func wholeFileDiff(t *testing.T, n int, changed ...int) File {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH; this test needs it to build a whole-file diff")
	}
	dir := t.TempDir()
	old := make([]string, n)
	for i := range old {
		old[i] = fmt.Sprintf("line %d", i+1)
	}
	updated := slices.Clone(old)
	for _, c := range changed {
		updated[c-1] = fmt.Sprintf("CHANGED %d", c)
	}
	writeLines(t, filepath.Join(dir, "old.txt"), old)
	writeLines(t, filepath.Join(dir, "new.txt"), updated)
	return gitDiff(t, dir, "-U100000")
}

func TestRenderDiffBodyFoldsAWholeFileDiff(t *testing.T) {
	f := wholeFileDiff(t, 400, 200)
	body := renderDiffBody([]File{f})

	if !strings.Contains(body, "data-collapsed") {
		t.Error("renderDiffBody() folded nothing away in a whole-file diff")
	}
	if !strings.Contains(body, `<div class="diff-hunk" data-foldable>`) {
		t.Error("renderDiffBody() did not mark the hunk foldable")
	}
	// One bar per run, carrying both directions: the two ends of a run are always adjacent on
	// screen, since everything between them is folded, so a control at each end would just be
	// two bars stacked together.
	if n := strings.Count(body, `class="diff-expander"`); n != 2 {
		t.Errorf("got %d expanders, want 2 (one per run: above and below the change)", n)
	}
	for _, dir := range []string{`data-expand="down"`, `data-expand="up"`, `data-expand="fold"`} {
		if n := strings.Count(body, dir); n != 2 {
			t.Errorf("got %d %s buttons, want one per run (2)", n, dir)
		}
	}
	// One hunk covering the file has no gap to mark, so its "@@ -1,400 +1,400 @@" says nothing.
	if strings.Contains(body, "diff-hunk-header") {
		t.Error("renderDiffBody() kept the @@ header on a single foldable hunk")
	}
	// Every line still ships; only its visibility changed.
	if n := strings.Count(body, `class="diff-line`); n != len(f.Lines()) {
		t.Errorf("rendered %d lines, want all %d", n, len(f.Lines()))
	}
}

// Several hunks mean the diff really does skip lines between them. That gap must stay marked,
// or it reads as something an expander could open.
func TestRenderDiffBodyKeepsHeadersWhenAFileHasSeveralHunks(t *testing.T) {
	f := wholeFileDiff(t, 400, 200)
	// Split the single hunk in two, keeping both foldable.
	mid := len(f.Hunks[0].Lines) / 2
	f.Hunks = []Hunk{
		{Header: "@@ -1,200 +1,200 @@", OldStart: 1, NewStart: 1, Lines: f.Hunks[0].Lines[:mid]},
		{Header: "@@ -201,200 +201,200 @@", OldStart: 201, NewStart: 201, Lines: f.Hunks[0].Lines[mid:]},
	}
	body := renderDiffBody([]File{f})
	if n := strings.Count(body, "diff-hunk-header"); n != 2 {
		t.Errorf("got %d hunk headers, want 2 — a skipped gap must stay marked", n)
	}
}

// The Rendered Line Index is the coordinate every comment anchor counts in, so it has to run
// 1..N over the lines as rendered whether or not they are folded away.
func TestRenderedLineIndexIsContinuousAcrossTheFold(t *testing.T) {
	f := wholeFileDiff(t, 400, 100, 300)
	body := renderDiffBody([]File{f})
	for i := 1; i <= len(f.Lines()); i++ {
		if !strings.Contains(body, fmt.Sprintf(`data-line-index="%d"`, i)) {
			t.Fatalf("data-line-index=%d is missing from the rendered body", i)
		}
	}
	if strings.Contains(body, fmt.Sprintf(`data-line-index="%d"`, len(f.Lines())+1)) {
		t.Errorf("rendered a data-line-index past the file's %d lines", len(f.Lines()))
	}
}

// examples/sample.diff is an ordinary diff and is not reached by TestRenderDiffBody's glob over
// testdata, so its freedom from the fold is checked here.
func TestSampleDiffDoesNotFold(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("examples", "sample.diff"))
	if err != nil {
		t.Fatalf("ReadFile(examples/sample.diff) = %v", err)
	}
	files, err := ParseUnifiedDiff(raw)
	if err != nil {
		t.Fatalf("ParseUnifiedDiff() = %v", err)
	}
	body := renderDiffBody(files)
	for _, mark := range []string{"data-collapsed", "diff-expander", "data-foldable"} {
		if strings.Contains(body, mark) {
			t.Errorf("examples/sample.diff rendered %s; an ordinary diff must fold nothing", mark)
		}
	}
}

// Nothing `git diff -U<n>` produces may fold, for any n a person would type. The bound is
// arithmetic — a run of context inside one hunk is at most 2U long, so what the Change Blocks
// leave uncovered is at most 2U - 2*blockContext, which reaches minCollapsedRun only at U=23 —
// but arithmetic is worth only as much as the widths it is checked against, so these are real
// git output.
//
// The bound is about -U alone; see TestFunctionContextCanFold for what it does not cover.
//
// F1 is the case that matters most: git merges hunks whose gaps are at most 2U, so a chain of
// edits near the top of a file becomes one long hunk starting at line 1 on both sides. Every
// rule that keyed off the shape of that header rather than off the length of its context runs
// let this diff through.
func TestUnifiedDiffWidthsNeverFold(t *testing.T) {
	sourceFile := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("line %d", i+1)
		}
		return out
	}
	goSource := func(funcs, body int) []string {
		var out []string
		for f := range funcs {
			out = append(out, fmt.Sprintf("func f%d() {", f))
			for i := range body {
				out = append(out, fmt.Sprintf("\tstep%d := %d", i, i))
			}
			out = append(out, "}", "")
		}
		return out
	}

	tests := []struct {
		name    string
		width   string
		lines   []string
		changed []int
	}{
		{
			name:    "F1: -U20 with edits chained into one hunk from line 1",
			width:   "-U20",
			lines:   sourceFile(400),
			changed: []int{20, 55, 90, 125, 160, 195, 230, 265, 300},
		},
		{
			name:    "F2: -U20 with the change on the first line",
			width:   "-U20",
			lines:   sourceFile(400),
			changed: []int{1},
		},
		{
			name:    "F3: -U10 with changes at the widest gap it still merges",
			width:   "-U10",
			lines:   sourceFile(400),
			changed: []int{100, 121},
		},
		{
			// -W is in this table only for a function short enough that the bound happens to
			// hold anyway. TestFunctionContextCanFold covers the case where it does not.
			name:    "F4: -W around a short function at the top of the file",
			width:   "-W",
			lines:   goSource(12, 30),
			changed: []int{3},
		},
		{
			name:    "F5a: -U3",
			width:   "-U3",
			lines:   sourceFile(400),
			changed: []int{200},
		},
		{
			name:    "F5b: -U5",
			width:   "-U5",
			lines:   sourceFile(400),
			changed: []int{200},
		},
		{
			name:    "F6: a file of two lines",
			width:   "-U3",
			lines:   []string{"alpha", "beta"},
			changed: []int{1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := exec.LookPath("git"); err != nil {
				t.Skip("git is not on PATH; this test needs it to produce real diffs")
			}
			dir := t.TempDir()
			updated := slices.Clone(tt.lines)
			for _, c := range tt.changed {
				updated[c-1] = "CHANGED " + tt.lines[c-1]
			}
			writeLines(t, filepath.Join(dir, "old.txt"), tt.lines)
			writeLines(t, filepath.Join(dir, "new.txt"), updated)

			f := gitDiff(t, dir, tt.width)
			body := renderDiffBody([]File{f})

			for _, mark := range []string{"data-collapsed", "diff-expander", "data-foldable"} {
				if strings.Contains(body, mark) {
					t.Errorf("%s folded: found %s\nheaders: %v", tt.width, mark, headersOf(f))
				}
			}
			if got, want := strings.Count(body, "diff-hunk-header"), len(f.Hunks); got != want {
				t.Errorf("rendered %d hunk headers, want one per hunk (%d)", got, want)
			}
		})
	}
}

func headersOf(f File) []string {
	var out []string
	for _, h := range f.Hunks {
		out = append(out, h.Header)
	}
	return out
}

// The threshold is what holds TestOrdinaryDiffWidthsNeverFold up, so the widest uncovered run an
// ordinary width can produce is measured directly. Anything at or below minCollapsedRun is safe;
// F1 sits at 28, which is why lowering the threshold past it would start folding real diffs.
func TestWidestUncoveredRunAtOrdinaryWidths(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	dir := t.TempDir()
	old := make([]string, 400)
	for i := range old {
		old[i] = fmt.Sprintf("line %d", i+1)
	}
	updated := slices.Clone(old)
	for _, c := range []int{20, 55, 90, 125, 160, 195, 230, 265, 300} {
		updated[c-1] = "CHANGED"
	}
	writeLines(t, filepath.Join(dir, "old.txt"), old)
	writeLines(t, filepath.Join(dir, "new.txt"), updated)

	f := gitDiff(t, dir, "-U20")
	if len(f.Hunks) != 1 {
		t.Fatalf("got %d hunks, want the chain to merge into 1", len(f.Hunks))
	}
	h := f.Hunks[0]
	if h.OldStart != 1 || h.NewStart != 1 {
		t.Fatalf("hunk starts at %d/%d, want 1/1 — the point of this case is that it looks whole-file", h.OldStart, h.NewStart)
	}

	widest := 0
	blocks := h.ChangeBlocks()
	prev := -1
	for _, b := range blocks {
		widest = max(widest, b.Start-prev-1)
		prev = b.End
	}
	widest = max(widest, len(h.Lines)-prev-1)

	if widest > wideGap {
		t.Errorf("a -U20 diff leaves a run of %d uncovered lines, past wideGap=%d — it would be folded", widest, wideGap)
	}
	t.Logf("widest uncovered run at -U20: %d (wideGap=%d)", widest, wideGap)
}

// On a -U3 diff a hunk IS one Change Block, so the Change Block search and the whole-file search
// cover exactly the same ground and the first tier can never answer something the second could
// not. That is what makes re-anchoring on an ordinary diff bit-for-bit what it was.
//
// It stops being true at wider widths: -U10 leaves context outside the blocks, so the first tier
// searches strictly less. Nothing that anchors is lost either way — a narrower search can only
// find a subset — but the widths above -U3 can have an ambiguity settled where they used to go
// outdated. Measured here rather than asserted.
func TestChangeBlocksCoverWholeHunksAtNarrowWidth(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	dir := t.TempDir()
	old := make([]string, 400)
	for i := range old {
		old[i] = fmt.Sprintf("line %d", i+1)
	}
	updated := slices.Clone(old)
	for _, c := range []int{20, 100, 107, 250, 399} {
		updated[c-1] = "CHANGED"
	}
	writeLines(t, filepath.Join(dir, "old.txt"), old)
	writeLines(t, filepath.Join(dir, "new.txt"), updated)

	t.Run("-U3 hunks are exactly their Change Block", func(t *testing.T) {
		for i, h := range gitDiff(t, dir, "-U3").Hunks {
			blocks := h.ChangeBlocks()
			if len(blocks) != 1 || blocks[0].Start != 0 || blocks[0].End != len(h.Lines)-1 {
				t.Errorf("hunk %d (%s) has blocks %v over %d lines, want one covering all of them",
					i, h.Header, blocks, len(h.Lines))
			}
		}
	})

	t.Run("-U10 leaves context outside the blocks", func(t *testing.T) {
		uncovered := 0
		for _, h := range gitDiff(t, dir, "-U10").Hunks {
			covered := 0
			for _, b := range h.ChangeBlocks() {
				covered += b.Len()
			}
			uncovered += len(h.Lines) - covered
		}
		if uncovered == 0 {
			t.Error("expected -U10 to leave context outside the Change Blocks; the two searches would be identical")
		}
		t.Logf("-U10 leaves %d lines outside the Change Blocks", uncovered)
	})
}

// `-W` sizes a hunk by the enclosing function rather than by a context width, so a long enough
// function hands over a run past minCollapsedRun and it folds. Nothing is lost when that happens
// — the rows are all still rendered and the Rendered Line Index does not move — but it is not
// what a reader of a -W diff would expect, so the behaviour is pinned here rather than described
// in a comment that could drift away from it.
func TestFunctionContextCanFold(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	var lines []string
	for f := range 2 {
		lines = append(lines, fmt.Sprintf("func big%d() {", f))
		for i := range 80 {
			lines = append(lines, fmt.Sprintf("\tstep%d := %d", i, i))
		}
		lines = append(lines, "}", "")
	}
	updated := slices.Clone(lines)
	updated[2] = "\tstep0 := 999 // edited"

	dir := t.TempDir()
	writeLines(t, filepath.Join(dir, "old.txt"), lines)
	writeLines(t, filepath.Join(dir, "new.txt"), updated)

	f := gitDiff(t, dir, "-W")
	if !f.Hunks[0].IsFoldable() {
		t.Fatalf("expected a -W hunk of %d lines around one edit to fold; it did not", len(f.Hunks[0].Lines))
	}

	// Folding it changes what is shown, never what is there.
	body := renderDiffBody([]File{f})
	if n := strings.Count(body, `class="diff-line`); n != len(f.Lines()) {
		t.Errorf("rendered %d rows, want all %d", n, len(f.Lines()))
	}
	for i := 1; i <= len(f.Lines()); i++ {
		if !strings.Contains(body, fmt.Sprintf(`data-line-index="%d"`, i)) {
			t.Fatalf("data-line-index=%d went missing", i)
		}
	}
}

// The diff path builds its own title — a path for one file, "Diff review" for several — and does
// not go through postProcessHTML, so it composes the Page Title from its own SpecMetadata.
func TestRenderDiffPageTitle(t *testing.T) {
	const oneFile = `diff --git a/diff.go b/diff.go
--- a/diff.go
+++ b/diff.go
@@ -1 +1 @@
-old
+new
`
	const twoFiles = oneFile + `diff --git a/render.go b/render.go
--- a/render.go
+++ b/render.go
@@ -1 +1 @@
-old
+new
`

	tests := []struct {
		name             string
		diff             string
		agentSessionName string
		wantTitle        string
		wantRailHeader   string
	}{
		{"one file, no name", oneFile, "", "<title>diff.go</title>", "<h2>diff.go</h2>"},
		{"one file, a name", oneFile, "demo", "<title>demo — diff.go</title>", "<h2>diff.go</h2>"},
		{"several files, no name", twoFiles, "", "<title>Diff review</title>", "<h2>Diff review</h2>"},
		{"several files, a name", twoFiles, "demo", "<title>demo — Diff review</title>", "<h2>Diff review</h2>"},
		{"whitespace only", oneFile, "   ", "<title>diff.go</title>", "<h2>diff.go</h2>"},
		{"a name is escaped", oneFile, `a<b&c"d`, "<title>a&lt;b&amp;c&#34;d — diff.go</title>", "<h2>diff.go</h2>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files, err := ParseUnifiedDiff([]byte(tt.diff))
			if err != nil {
				t.Fatalf("ParseUnifiedDiff() error = %v", err)
			}
			output, err := RenderDiff(files, tt.agentSessionName)
			if err != nil {
				t.Fatalf("RenderDiff() error = %v", err)
			}
			got := string(output)
			if !strings.Contains(got, tt.wantTitle) {
				t.Errorf("Page Title missing %q", tt.wantTitle)
			}
			if !strings.Contains(got, tt.wantRailHeader) {
				t.Errorf("Contents Rail header missing %q", tt.wantRailHeader)
			}
		})
	}
}
