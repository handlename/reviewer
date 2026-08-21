package reviewer

import (
	"flag"
	"os"
	"path/filepath"
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
`))
	if err != nil {
		t.Fatalf("Render(diff) error = %v", err)
	}
	if !strings.Contains(string(diffHTML), `class="diff-line`) {
		t.Error("a diff did not render as a diff")
	}
	if strings.Contains(string(diffHTML), "initializeCommentableElements();") {
		t.Error("diff mode must not run the Markdown block-comment initializer")
	}

	mdHTML, err := Render([]byte("# Heading\n\nProse.\n"))
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
