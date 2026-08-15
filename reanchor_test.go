package reviewer

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// round1 is the diff a comment was written against.
const round1Diff = `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -10,6 +10,6 @@ func main() {
 	setup()
 	foo()
-	old()
+	replacement()
 	}
 	bar()
`

func parseOne(t *testing.T, content string) File {
	t.Helper()
	files, err := ParseUnifiedDiff([]byte(content))
	if err != nil {
		t.Fatalf("ParseUnifiedDiff() error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1", len(files))
	}
	return files[0]
}

func TestReAnchorFollowsShiftedLines(t *testing.T) {
	// An unrelated hunk added upstream pushes everything down by two rendered lines.
	shifted := parseOne(t, `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,2 +1,3 @@
 package main
+import "fmt"
@@ -10,6 +10,6 @@ func main() {
 	setup()
 	foo()
-	old()
+	replacement()
 	}
 	bar()
`)

	// The comment was on "	foo()" at index 2 of the round-1 diff.
	start, end, ok := ReAnchor(2, 2, []string{"\tfoo()"}, shifted)
	if !ok {
		t.Fatal("comment went outdated even though its line is still there")
	}
	// The new hunk contributes two rendered lines ahead of it, so index 2 becomes index 4.
	if start != 4 || end != 4 {
		t.Errorf("re-anchored to %d-%d, want 4-4", start, end)
	}
}

// The short circuit is what stops a comment on "}" — or any other line that occurs more than
// once — from going outdated the instant the page reloads with the diff unchanged.
func TestReAnchorKeepsDuplicatedLineWhenNothingMoved(t *testing.T) {
	file := parseOne(t, `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,6 +1,6 @@
 	}
 	foo()
-	old()
+	new()
 	}
 	}
`)

	start, end, ok := ReAnchor(1, 1, []string{"\t}"}, file)
	if !ok {
		t.Fatal(`a comment on a duplicated "}" went outdated with the diff unchanged`)
	}
	if start != 1 || end != 1 {
		t.Errorf("moved to %d-%d, want it to stay at 1-1", start, end)
	}
}

func TestReAnchorOutdatedCases(t *testing.T) {
	tests := []struct {
		name        string
		diff        string
		prevStart   int
		prevEnd     int
		anchorLines []string
	}{
		{
			name: "the line was deleted",
			diff: `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -10,3 +10,2 @@
 	setup()
 	bar()
`,
			prevStart: 2, prevEnd: 2, anchorLines: []string{"\tfoo()"},
		},
		{
			// Moved AND duplicated: there is no way to tell which occurrence was meant.
			name: "position shifted and the content occurs twice",
			diff: `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,4 +1,4 @@
 	setup()
 	}
 	bar()
 	}
`,
			prevStart: 1, prevEnd: 1, anchorLines: []string{"\t}"},
		},
		{
			// A match must live inside one hunk: lines either side of a @@ header are far
			// apart in the real file, so a span across one is an accident of rendering.
			name: "the range would have to span two hunks",
			diff: `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,1 +1,1 @@
 	first()
@@ -20,1 +20,1 @@
 	second()
`,
			prevStart: 1, prevEnd: 2, anchorLines: []string{"\tfirst()", "\tsecond()"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, ok := ReAnchor(tt.prevStart, tt.prevEnd, tt.anchorLines, parseOne(t, tt.diff)); ok {
				t.Error("want the comment to go outdated, but it followed")
			}
		})
	}
}

func TestReAnchorSpansRemovalAndAddition(t *testing.T) {
	file := parseOne(t, round1Diff)
	// The removal and its replacement, selected together — the primary use of a suggestion.
	start, end, ok := ReAnchor(3, 4, []string{"\told()", "\treplacement()"}, file)
	if !ok {
		t.Fatal("a range spanning - and + did not anchor")
	}
	if start != 3 || end != 4 {
		t.Errorf("anchored to %d-%d, want 3-4", start, end)
	}
}

func TestReAnchorMatchesRegardlessOfLineKind(t *testing.T) {
	// Round 1 the line was added; round 2 the agent's change landed upstream and it is context.
	round2 := parseOne(t, `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -10,3 +10,3 @@
 	replacement()
-	other()
+	changed()
`)

	if _, _, ok := ReAnchor(4, 4, []string{"\treplacement()"}, round2); !ok {
		t.Error("a line that turned from added into context lost its comment")
	}
}

func TestReAnchorIsIdempotent(t *testing.T) {
	file := parseOne(t, round1Diff)
	lines := []string{"\tfoo()"}

	start, end, ok := ReAnchor(2, 2, lines, file)
	if !ok {
		t.Fatal("first pass did not anchor")
	}
	start2, end2, ok := ReAnchor(start, end, lines, file)
	if !ok || start2 != start || end2 != end {
		t.Errorf("second pass moved the anchor: %d-%d then %d-%d", start, end, start2, end2)
	}
}

func TestFindFile(t *testing.T) {
	files, err := ParseUnifiedDiff([]byte(`diff --git a/old/name.go b/new/name.go
rename from old/name.go
rename to new/name.go
--- a/old/name.go
+++ b/new/name.go
@@ -1 +1 @@
-package old
+package new
diff --git a/gone.go b/gone.go
--- a/gone.go
+++ /dev/null
@@ -1 +0,0 @@
-package main
`))
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := FindFile(files, "new/name.go"); !ok {
		t.Error("a renamed file was not found under its new path")
	}
	if _, ok := FindFile(files, "old/name.go"); ok {
		t.Error("a renamed file is addressed by its new path only")
	}
	// A deleted file is known by its old path, since /dev/null names nothing.
	if _, ok := FindFile(files, "gone.go"); !ok {
		t.Error("a deleted file was not found under its old path")
	}
}

func TestReAnchorCommentsResetsOutdatedWhenTheLinesComeBack(t *testing.T) {
	files, err := ParseUnifiedDiff([]byte(round1Diff))
	if err != nil {
		t.Fatal(err)
	}

	got := reAnchorComments([]Comment{{
		Text:        "was outdated last round",
		Anchor:      FormatDiffAnchor("main.go", 2, 2),
		AnchorLines: []string{"\tfoo()"},
		Outdated:    true,
	}}, files)

	if got[0].Outdated {
		t.Error("Outdated stayed true after the comment matched again")
	}
}

// The whole file leaving the diff is the common case after the agent finishes a file.
func TestReAnchorCommentsMarksMissingFileOutdated(t *testing.T) {
	files, err := ParseUnifiedDiff([]byte(round1Diff))
	if err != nil {
		t.Fatal(err)
	}

	got := reAnchorComments([]Comment{{
		Anchor:      FormatDiffAnchor("other.go", 1, 1),
		AnchorLines: []string{"\tfoo()"},
	}}, files)

	if !got[0].Outdated {
		t.Error("a comment on a file no longer in the diff should be outdated")
	}
}

// Markdown anchors share every code path here, so they must come out exactly as they went in.
func TestReAnchorCommentsPassesNonDiffAnchorsThrough(t *testing.T) {
	files, err := ParseUnifiedDiff([]byte(round1Diff))
	if err != nil {
		t.Fatal(err)
	}

	got := reAnchorComments([]Comment{
		{Anchor: "spec-element-3", Context: "The system MUST…"},
		{Anchor: ""},
	}, files)

	for i, c := range got {
		if c.Outdated {
			t.Errorf("comment %d with a non-diff anchor was marked outdated", i)
		}
	}
	if got[0].Anchor != "spec-element-3" {
		t.Errorf("anchor was rewritten to %q", got[0].Anchor)
	}
}

func TestGetFeedbackReAnchorsWithoutTouchingTheSidecar(t *testing.T) {
	dir := t.TempDir()
	diffPath := filepath.Join(dir, "review.diff")
	if err := os.WriteFile(diffPath, []byte(round1Diff), 0644); err != nil {
		t.Fatal(err)
	}

	url, stop := startTestServer(t, diffPath)
	defer stop()

	// A comment recorded against a position that has since moved: only the content matches.
	postFeedback(t, url, Feedback{Comments: []Comment{{
		Text:        "why the rename?",
		Anchor:      FormatDiffAnchor("main.go", 1, 1),
		AnchorLines: []string{"\tfoo()"},
		Author:      AuthorHuman,
		Status:      StatusOpen,
	}}})

	sidecar := FeedbackPath(diffPath)
	before, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	beforeStat, err := os.Stat(sidecar)
	if err != nil {
		t.Fatal(err)
	}

	// Let a same-millisecond write still be visible as a change in content.
	time.Sleep(10 * time.Millisecond)

	fb := getFeedback(t, url)
	if len(fb.Comments) != 1 {
		t.Fatalf("got %d comments, want 1", len(fb.Comments))
	}
	if got, want := fb.Comments[0].Anchor, FormatDiffAnchor("main.go", 2, 2); got != want {
		t.Errorf("anchor served as %q, want it re-anchored to %q", got, want)
	}
	if fb.Comments[0].Outdated {
		t.Error("a comment that followed was served as outdated")
	}

	after, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	afterStat, err := os.Stat(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("GET /api/feedback rewrote the sidecar; re-anchoring is for display only")
	}
	if !beforeStat.ModTime().Equal(afterStat.ModTime()) {
		t.Error("GET /api/feedback touched the sidecar's mtime")
	}
	// The stored anchor is still the one the human submitted: the browser posts the
	// re-anchored version back on the next submit.
	var stored Feedback
	readJSONFile(t, sidecar, &stored)
	if got, want := stored.Comments[0].Anchor, FormatDiffAnchor("main.go", 1, 1); got != want {
		t.Errorf("stored anchor = %q, want %q", got, want)
	}
}

func TestGetFeedbackLeavesMarkdownCommentsAlone(t *testing.T) {
	dir := t.TempDir()
	docPath := filepath.Join(dir, "spec.md")
	if err := os.WriteFile(docPath, []byte("# Spec\n\nThe system MUST do the thing.\n"), 0644); err != nil {
		t.Fatal(err)
	}

	url, stop := startTestServer(t, docPath)
	defer stop()

	postFeedback(t, url, Feedback{Comments: []Comment{{
		Text:   "clarify this",
		Anchor: "spec-element-1",
		Author: AuthorHuman,
		Status: StatusOpen,
	}}})

	fb := getFeedback(t, url)
	if len(fb.Comments) != 1 {
		t.Fatalf("got %d comments, want 1", len(fb.Comments))
	}
	if fb.Comments[0].Outdated {
		t.Error("a Markdown comment was marked outdated; the diff guard is not holding")
	}
	if fb.Comments[0].Anchor != "spec-element-1" {
		t.Errorf("anchor = %q, want it untouched", fb.Comments[0].Anchor)
	}
}

// An outdated comment is still a comment: the agent addresses it by ID, and Reply looks IDs up.
func TestReplyReachesAnOutdatedComment(t *testing.T) {
	dir := t.TempDir()
	diffPath := filepath.Join(dir, "review.diff")
	if err := os.WriteFile(diffPath, []byte(round1Diff), 0644); err != nil {
		t.Fatal(err)
	}

	s, err := StartSession(t.Context(), diffPath, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	postFeedback(t, s.URL(), Feedback{Comments: []Comment{{
		Text:        "gone next round",
		Anchor:      FormatDiffAnchor("main.go", 1, 1),
		AnchorLines: []string{"\tvanished()"},
		Outdated:    true,
		Author:      AuthorHuman,
		Status:      StatusOpen,
	}}})

	var stored Feedback
	readJSONFile(t, FeedbackPath(diffPath), &stored)
	id := stored.Comments[0].ID

	if err := s.Reply([]ReplyInput{{CommentID: id, Reply: "removed that code entirely"}}, "cleaned up"); err != nil {
		t.Fatalf("Reply to an outdated comment failed: %v", err)
	}

	readJSONFile(t, FeedbackPath(diffPath), &stored)
	if stored.Comments[0].Reply != "removed that code entirely" {
		t.Errorf("reply = %q", stored.Comments[0].Reply)
	}
	if !stored.Comments[0].Outdated {
		t.Error("Reply cleared the outdated flag")
	}
}

func getFeedback(t *testing.T, url string) Feedback {
	t.Helper()
	resp, err := http.Get(url + "/api/feedback")
	if err != nil {
		t.Fatalf("GET feedback: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var fb Feedback
	if err := json.Unmarshal(body, &fb); err != nil {
		t.Fatalf("unmarshal feedback %s: %v", body, err)
	}
	return fb
}
