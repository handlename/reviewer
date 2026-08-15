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
