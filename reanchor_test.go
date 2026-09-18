package reviewer

import (
	"encoding/json"
	"fmt"
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

// A whole-file comment has no lines to match, so it follows the file: it survives every edit
// inside it, and only goes outdated when the file itself leaves the diff.
func TestReAnchorCommentsFollowsWholeFileComments(t *testing.T) {
	files, err := ParseUnifiedDiff([]byte(round1Diff))
	if err != nil {
		t.Fatal(err)
	}

	got := reAnchorComments([]Comment{
		{Text: "split this file", Anchor: FormatDiffFileAnchor("main.go"), Outdated: true},
		{Text: "where are the tests", Anchor: FormatDiffFileAnchor("other.go")},
	}, files)

	if got[0].Outdated {
		t.Error("a comment on a file still in the diff was marked outdated")
	}
	if got[0].Anchor != FormatDiffFileAnchor("main.go") {
		t.Errorf("anchor was rewritten to %q", got[0].Anchor)
	}
	if !got[1].Outdated {
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

	if err := s.Reply([]ReplyInput{{CommentID: id, Reply: "removed that code entirely"}}, nil, "cleaned up"); err != nil {
		t.Fatalf("Reply to an outdated comment failed: %v", err)
	}

	readJSONFile(t, FeedbackPath(diffPath), &stored)
	if msgs := stored.Comments[0].Messages; len(msgs) != 1 || msgs[0].Text != "removed that code entirely" {
		t.Errorf("thread = %#v", stored.Comments[0].Messages)
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

// A thread the agent opened carries its target as a quotation. Resolving it is the same content
// search a diff comment already uses, so the thread finds its lines each round.
func TestResolveQuoteOnADiff(t *testing.T) {
	files, err := ParseUnifiedDiff([]byte(round1Diff))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("a quote matching one line anchors to it", func(t *testing.T) {
		anchor, lines, ok := ResolveQuote("\treplacement()", files)
		if !ok {
			t.Fatal("a quote present in the diff did not resolve")
		}
		if anchor != FormatDiffAnchor("main.go", 4, 4) {
			t.Errorf("anchor = %q, want %q", anchor, FormatDiffAnchor("main.go", 4, 4))
		}
		if len(lines) != 1 || lines[0] != "\treplacement()" {
			t.Errorf("anchorLines = %#v", lines)
		}
	})

	t.Run("a multi-line quote spans the range", func(t *testing.T) {
		anchor, _, ok := ResolveQuote("\told()\n\treplacement()", files)
		if !ok {
			t.Fatal("a two-line quote did not resolve")
		}
		if anchor != FormatDiffAnchor("main.go", 3, 4) {
			t.Errorf("anchor = %q, want %q", anchor, FormatDiffAnchor("main.go", 3, 4))
		}
	})

	t.Run("blank lines around a quote are ignored", func(t *testing.T) {
		if _, _, ok := ResolveQuote("\n\treplacement()\n", files); !ok {
			t.Error("a quote wrapped in blank lines did not resolve")
		}
	})

	t.Run("a quote matching nothing does not resolve", func(t *testing.T) {
		if _, _, ok := ResolveQuote("\tnowhere()", files); ok {
			t.Error("a quote absent from the diff resolved anyway")
		}
	})

	t.Run("an empty quote does not resolve", func(t *testing.T) {
		if _, _, ok := ResolveQuote("", files); ok {
			t.Error("an empty quote is a document-level thread, not an anchor")
		}
	})
}

// Several matches take the first: exactly one element may carry data-anchor, and pointing at the
// first occurrence is more useful than pointing at nothing.
func TestResolveQuoteTakesTheFirstOfSeveralMatches(t *testing.T) {
	files, err := ParseUnifiedDiff([]byte(`diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,4 +1,4 @@
 	}
 	foo()
 	}
 	bar()
`))
	if err != nil {
		t.Fatal(err)
	}

	anchor, _, ok := ResolveQuote("\t}", files)
	if !ok {
		t.Fatal("a duplicated quote did not resolve")
	}
	if anchor != FormatDiffAnchor("main.go", 1, 1) {
		t.Errorf("anchor = %q, want the first occurrence %q", anchor, FormatDiffAnchor("main.go", 1, 1))
	}
}

// The quote, not the anchor derived from it last round, is what an agent thread is placed by.
func TestReAnchorCommentsResolvesAgentThreadQuotes(t *testing.T) {
	files, err := ParseUnifiedDiff([]byte(round1Diff))
	if err != nil {
		t.Fatal(err)
	}

	got := reAnchorComments([]Comment{
		{Text: "why?", Author: AuthorAgent, NeedsAnswer: true, AnchorQuote: "\treplacement()", Outdated: true},
		{Text: "gone", Author: AuthorAgent, AnchorQuote: "\tvanished()", Anchor: FormatDiffAnchor("main.go", 2, 2)},
		{Text: "about the change as a whole", Author: AuthorAgent},
	}, files)

	if got[0].Outdated || got[0].Anchor != FormatDiffAnchor("main.go", 4, 4) {
		t.Errorf("a resolvable quote = %#v", got[0])
	}
	if len(got[0].AnchorLines) != 1 || got[0].AnchorLines[0] != "\treplacement()" {
		t.Errorf("anchorLines = %#v", got[0].AnchorLines)
	}
	if !got[1].Outdated {
		t.Error("a quote that matches nothing should be outdated")
	}
	if got[1].AnchorQuote != "\tvanished()" || got[1].Anchor != FormatDiffAnchor("main.go", 2, 2) {
		t.Errorf("an outdated thread lost what it needs to come back: %#v", got[1])
	}
	if got[2].Outdated || got[2].Anchor != "" {
		t.Errorf("a document-level thread = %#v, want no target and not outdated", got[2])
	}
}

// reAnchorLegacy is ReAnchor as it stood before the Change Blocks were searched first: rule 1,
// then one search of the whole file. It is kept here, as a copy rather than as a call, so the
// promise that no comment which anchors today stops anchoring is checked against the old code
// itself instead of against a description of it.
func reAnchorLegacy(prevStart, prevEnd int, anchorLines []string, file File) (start, end int, ok bool) {
	if len(anchorLines) == 0 {
		return 0, 0, false
	}
	if prevEnd-prevStart+1 == len(anchorLines) {
		if hunk, offset, ok := locateInHunk(file, prevStart, prevEnd); ok && matchesAt(hunk.Lines, offset, anchorLines) {
			return prevStart, prevEnd, true
		}
	}
	var matches []int
	base := 0
	for _, h := range file.Hunks {
		for i := 0; i+len(anchorLines) <= len(h.Lines); i++ {
			if matchesAt(h.Lines, i, anchorLines) {
				matches = append(matches, base+i+1)
			}
		}
		base += len(h.Lines)
	}
	if len(matches) != 1 {
		return 0, 0, false
	}
	return matches[0], matches[0] + len(anchorLines) - 1, true
}

func ctxLine(content string) Line { return Line{Kind: LineContext, Content: content} }
func addLine(content string) Line { return Line{Kind: LineAdd, Content: content} }

func padLines(prefix string, n int) []Line {
	out := make([]Line, n)
	for i := range out {
		out[i] = ctxLine(fmt.Sprintf("%s %d", prefix, i))
	}
	return out
}

// foldedFixture lays out a file that carries far more context than a narrow diff would, so the
// fold has something to hide and the two-tier search has both grounds to search.
//
// Layout, 0-based: [0,99] padding, 100 "before", 101 the change, 102 "}", [103,...] padding.
// The change makes a Change Block of [98,104]; everything outside it is folded away.
func foldedFixture(t *testing.T, headSwap map[int]string) File {
	t.Helper()
	lines := padLines("head", 100)
	for i, content := range headSwap {
		lines[i] = ctxLine(content)
	}
	lines = append(lines, ctxLine("before"), addLine("changed"), ctxLine("}"))
	lines = append(lines, padLines("tail", 150)...)

	f := File{OldPath: "a.go", NewPath: "a.go", Hunks: []Hunk{
		{Header: "@@ -1,251 +1,251 @@", OldStart: 1, NewStart: 1, Lines: lines},
	}}
	if !f.Hunks[0].IsFoldable() {
		t.Fatal("fixture does not fold; the two-tier search would have nothing to distinguish")
	}
	return f
}

// ruleTwoAWouldDecide reports whether the Change Block search is what answers this lookup —
// that is, whether it finds exactly one match where the whole-file search does not.
func ruleTwoAWouldDecide(file File, prevStart, prevEnd int, want []string) bool {
	return inChangeBlock(file, prevStart, prevEnd) &&
		len(searchChangeBlocks(file, want)) == 1 &&
		len(searchHunks(file, want)) != 1
}

// Every comment the old code could place, the new code places in the same spot. The property is
// meant to hold by construction — a Change Block is part of a hunk, so the narrow search can
// only ever find a subset of what the whole-file search finds — and this pins it down.
//
// The corpus has to reach the branch it is testing, so it carries all three shapes: one the
// Change Block search answers, one that falls through to the whole-file search, and one rule 1
// settles without searching at all. The count at the end is what stops it passing vacuously.
func TestReAnchorNeverLosesWhatTheOldSearchFound(t *testing.T) {
	withTwin := foldedFixture(t, map[int]string{50: "}"})
	plain := foldedFixture(t, nil)

	cases := []struct {
		name                string
		file                File
		prevStart, prevEnd  int
		anchor              []string
		wantRuleTwoADecides bool
	}{
		{
			// "}" sits both in the folded padding and in the Change Block. The whole-file search
			// finds two and gives up; the Change Block search finds the one the narrow diff
			// would have shown.
			// prev points at a line that no longer holds the anchor, which is what happens once
			// the agent's edit shifts everything below it — the only time the search runs at all.
			name: "the Change Block search settles an ambiguity",
			file: withTwin, prevStart: 99, prevEnd: 99,
			anchor:              []string{"}"},
			wantRuleTwoADecides: true,
		},
		{
			// The anchor runs out of the Change Block into the folded lines below it, so no
			// block contains it and the whole-file search answers.
			//
			// prev is off by the five lines an edit above would have inserted, which is what
			// makes rule 1 miss and the search run — pointing it at the lines it already holds
			// would settle it before either search was reached.
			name: "an anchor spanning the block and the fold falls through",
			file: plain, prevStart: 108, prevEnd: 111,
			anchor: []string{"}", "tail 0", "tail 1", "tail 2"},
		},
		{
			name: "rule 1 settles an unmoved anchor without searching",
			file: plain, prevStart: 101, prevEnd: 101,
			anchor: []string{"before"},
		},
		{
			name: "a unique anchor deep in the folded region",
			file: plain, prevStart: 30, prevEnd: 30,
			anchor: []string{"head 29"},
		},
		{
			name: "an anchor that occurs nowhere",
			file: plain, prevStart: 103, prevEnd: 103,
			anchor: []string{"not in this file"},
		},
		{
			name: "a multi-line anchor inside the Change Block",
			file: plain, prevStart: 101, prevEnd: 103,
			anchor: []string{"before", "changed", "}"},
		},
	}

	decided := 0
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantStart, wantEnd, wantOK := reAnchorLegacy(tc.prevStart, tc.prevEnd, tc.anchor, tc.file)
			gotStart, gotEnd, gotOK := ReAnchor(tc.prevStart, tc.prevEnd, tc.anchor, tc.file)

			if wantOK && (!gotOK || gotStart != wantStart || gotEnd != wantEnd) {
				t.Errorf("the old search placed this at %d-%d; the new one returned %d-%d (ok=%v)",
					wantStart, wantEnd, gotStart, gotEnd, gotOK)
			}
			if got := ruleTwoAWouldDecide(tc.file, tc.prevStart, tc.prevEnd, tc.anchor); got != tc.wantRuleTwoADecides {
				t.Errorf("the Change Block search deciding = %v, want %v", got, tc.wantRuleTwoADecides)
			}
		})
		if ruleTwoAWouldDecide(tc.file, tc.prevStart, tc.prevEnd, tc.anchor) {
			decided++
		}
	}

	if decided == 0 {
		t.Error("no case in the corpus reached the Change Block search, so the property was never exercised")
	}
}

// P1: a whole-file diff shows a twin of the anchor that the narrow diff never did. Without the
// Change Block search the comment would go outdated the moment the agent regenerated the diff.
func TestReAnchorPrefersTheChangeBlockWhenTheFoldHidesATwin(t *testing.T) {
	f := foldedFixture(t, map[int]string{50: "}"})
	// prev no longer holds the anchor, so rule 1 misses and the search decides. That is the
	// ordinary case: the agent edits something above and every index below it shifts.
	if _, _, ok := reAnchorLegacy(99, 99, []string{"}"}, f); ok {
		t.Fatal("the old search was expected to give up here; the fixture proves nothing otherwise")
	}
	start, end, ok := ReAnchor(99, 99, []string{"}"}, f)
	if !ok || start != 103 || end != 103 {
		t.Errorf("ReAnchor() = %d-%d, ok=%v, want 103-103 and ok", start, end, ok)
	}
}

// P2: the twin is in a Change Block too, so neither search can choose. Outdated is the honest
// answer, and it is what both the old and the new code give.
func TestReAnchorStaysOutdatedWhenBothBlocksHoldTheAnchor(t *testing.T) {
	f := foldedFixture(t, nil)
	h := &f.Hunks[0]
	// A second change far below, with a "}" of its own beside it.
	h.Lines[200] = addLine("changed twice")
	h.Lines[201] = ctxLine("}")

	if len(searchChangeBlocks(f, []string{"}"})) != 2 {
		t.Fatalf("fixture should put the anchor in two Change Blocks, got %v", searchChangeBlocks(f, []string{"}"}))
	}
	if _, _, ok := ReAnchor(99, 99, []string{"}"}, f); ok {
		t.Error("ReAnchor() chose between two equally good Change Block matches; it should go outdated")
	}
}

// P3: the comment sits on a folded line, which a narrow diff never showed, so there is no
// narrow-diff answer to borrow. Preferring the changed region here would walk the comment a
// hundred lines to code it was never about — quietly, and for keeps once the page posts back.
func TestReAnchorDoesNotPullAFoldedCommentIntoTheChange(t *testing.T) {
	f := foldedFixture(t, map[int]string{20: "}"})
	// The comment was written on the "}" at index 20, folded away; its twin at index 102 sits in
	// the Change Block. prev has drifted off it, so rule 1 misses and the search decides.
	if inChangeBlock(f, 25, 25) {
		t.Fatal("fixture puts the anchor inside a Change Block; it must be in the fold")
	}
	if len(searchChangeBlocks(f, []string{"}"})) != 1 {
		t.Fatalf("fixture should leave exactly one twin in a Change Block, got %v", searchChangeBlocks(f, []string{"}"}))
	}
	// Ungated, the Change Block search would find that single twin and take it.
	if start, end, ok := ReAnchor(25, 25, []string{"}"}, f); ok {
		t.Errorf("ReAnchor() moved a folded comment to %d-%d; it should go outdated", start, end)
	}
}

// Inside one file the changed code answers first, so a passage quoted from it is not claimed by
// an identical line buried in the context above.
func TestResolveQuoteReadsTheChangeBlockFirst(t *testing.T) {
	f := foldedFixture(t, map[int]string{50: "}"})
	anchor, lines, ok := ResolveQuote("}", []File{f})
	if !ok {
		t.Fatal("ResolveQuote() found nothing")
	}
	if want := FormatDiffAnchor("a.go", 103, 103); anchor != want {
		t.Errorf("ResolveQuote() = %q, want %q (the occurrence beside the change)", anchor, want)
	}
	if len(lines) != 1 || lines[0] != "}" {
		t.Errorf("ResolveQuote() lines = %v, want [}]", lines)
	}
}

// Files keep their order. Staging the search across all of them would let a later file's change
// outrank an earlier file's context, which is not what "the first occurrence" has ever meant.
func TestResolveQuoteKeepsFileOrder(t *testing.T) {
	first := File{OldPath: "first.go", NewPath: "first.go", Hunks: []Hunk{{
		Header: "@@ -1,3 +1,3 @@", OldStart: 1, NewStart: 1,
		Lines: []Line{ctxLine("alpha"), ctxLine("shared"), ctxLine("omega")},
	}}}
	second := foldedFixture(t, map[int]string{50: "shared"})
	second.OldPath, second.NewPath = "second.go", "second.go"
	second.Hunks[0].Lines[102] = ctxLine("shared")

	anchor, _, ok := ResolveQuote("shared", []File{first, second})
	if !ok {
		t.Fatal("ResolveQuote() found nothing")
	}
	if want := FormatDiffAnchor("first.go", 2, 2); anchor != want {
		t.Errorf("ResolveQuote() = %q, want %q — the earlier file wins even though the later one has it in a change", anchor, want)
	}
}

// The case the two-tier search exists to leave alone: an anchor that reaches out of a Change
// Block into the fold cannot be answered by the block search, and the whole-file search has to
// place it. Checked with rule 1 out of the way, so the search is what is being measured.
func TestReAnchorPlacesASpanningAnchorByTheFileSearch(t *testing.T) {
	f := foldedFixture(t, nil)
	anchor := []string{"}", "tail 0", "tail 1", "tail 2"}

	if got := searchChangeBlocks(f, anchor); len(got) != 0 {
		t.Fatalf("the Change Block search found %v; the anchor is meant to reach past the block", got)
	}
	if got := len(searchHunks(f, anchor)); got != 1 {
		t.Fatalf("the file search found %d matches, want exactly 1", got)
	}

	// prev has drifted, as it does whenever the agent edits something above.
	start, end, ok := ReAnchor(108, 111, anchor, f)
	if !ok || start != 103 || end != 106 {
		t.Errorf("ReAnchor() = %d-%d, ok=%v, want 103-106 and ok", start, end, ok)
	}
}
