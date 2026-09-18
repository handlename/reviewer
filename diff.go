package reviewer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Kind is what a review target turns out to be once its content is read.
//
// The kind is decided from the content, never from the file name or a flag: the agent writes
// its diff to a temp file whose name it chooses, and `reviewer serve <file>` is the only entry
// point either way.
type Kind string

const (
	KindMarkdown Kind = "markdown"
	KindDiff     Kind = "diff"
)

// LineKind is a diff line's role. Rendering and re-anchoring both branch on it, but matching
// across rounds deliberately does not (see reanchor.go).
type LineKind string

const (
	LineContext LineKind = "context"
	LineAdd     LineKind = "add"
	LineDelete  LineKind = "delete"
	// LineMeta is diff bookkeeping shown verbatim, e.g. "\ No newline at end of file".
	LineMeta LineKind = "meta"
)

// Line is one rendered row of a diff. Content has the leading +/-/space marker stripped, which
// is also the form comments match against, so a line that turns from added to context between
// rounds still matches itself.
//
// OldNo and NewNo are 0 when the line does not exist on that side.
type Line struct {
	Kind    LineKind
	Content string
	OldNo   int
	NewNo   int
}

// Hunk is one @@ section. Header is the raw @@ line, kept whole because its trailing section
// heading (a function signature, usually) is the most useful context a diff carries.
//
// OldStart and NewStart are the first line the hunk covers on each side, read from the header.
// They are kept because the header is the only place they appear, and a hunk that starts at
// line 1 on both sides is the shape a whole-file diff takes.
type Hunk struct {
	Header   string
	OldStart int
	NewStart int
	Lines    []Line
}

// File is one file's worth of diff. Paths have their a/ and b/ prefixes stripped; a side the
// file does not exist on is "/dev/null", exactly as the diff spells it.
type File struct {
	OldPath string
	NewPath string
	Hunks   []Hunk
}

// DisplayPath is the single path a file is known by on the page and inside comment anchors.
//
// It is NewPath except for a deleted file, where NewPath is /dev/null and OldPath is the only
// name the file has. Using /dev/null as-is would make every deleted file share one anchor
// namespace, so two deletions in one diff would collide.
func (f File) DisplayPath() string {
	if f.NewPath == "" || f.NewPath == devNull {
		return f.OldPath
	}
	return f.NewPath
}

// FileStatus is what happened to a file in this diff.
type FileStatus string

const (
	FileAdded    FileStatus = "added"
	FileDeleted  FileStatus = "deleted"
	FileRenamed  FileStatus = "renamed"
	FileModified FileStatus = "modified"
)

// Status reports the file's fate, which the page shows as a mark beside its name.
func (f File) Status() FileStatus {
	switch {
	case f.OldPath == devNull:
		return FileAdded
	case f.NewPath == devNull:
		return FileDeleted
	case f.OldPath != "" && f.NewPath != "" && f.OldPath != f.NewPath:
		return FileRenamed
	default:
		return FileModified
	}
}

// Lines returns the file's lines in rendering order, which is also the order the 1-based
// indices in a comment anchor count.
func (f File) Lines() []Line {
	var out []Line
	for _, h := range f.Hunks {
		out = append(out, h.Lines...)
	}
	return out
}

// Folding a diff down to its changes, and the constants that decide how much it hides.
const (
	// blockContext is how many lines of context a Change Block keeps on each side of a change.
	//
	// It is 3 so that a Change Block is exactly a `git diff -U3` hunk. git splits two changes
	// into separate hunks when more than 2*blockContext unchanged lines lie between them, and
	// two blocks are left with a line between them under precisely the same condition — which
	// is what lets re-anchoring inside a block behave as it would on the narrow diff.
	blockContext = 3

	// wideGap is the length of context run that tells the fold it may act at all.
	//
	// git merges two hunks only when at most 2U unchanged lines separate them, so a run of
	// context inside one hunk is never longer than 2U, and the part of it the Change Blocks
	// leave uncovered never longer than 2U - 2*blockContext. At 40 that arithmetic reaches
	// U=23, so a hunk holding a run this long cannot have come from `git diff -U<n>` for any
	// n a person would type: it carries more of the file than a diff normally shows. Finding
	// one is the licence to fold; without it the hunk renders exactly as it always has.
	//
	// The reasoning is about -U alone. `git diff -W` sizes a hunk by the enclosing function
	// and `--inter-hunk-context` by a figure of its own, so either can produce a run this long
	// and be folded — a -W diff of a function over about 46 lines is. Nothing is lost when that
	// happens: every row is still in the document, the Rendered Line Index does not move, and
	// the run opens in one click. It is only not what the reader of a -W diff would expect.
	wideGap = 40

	// minCollapsedRun is the longest run the fold leaves alone once it has licence to act.
	//
	// It is small because the point of folding is to leave the reader what a `git diff -U3`
	// would have shown, and -U3 elides every gap over 2*blockContext. Keeping it at the size of
	// wideGap instead would leave up to 40 lines of untouched context around every change —
	// the very burying the fold exists to undo. It is not 0 only because hiding a handful of
	// lines behind a control that is itself a line saves nobody anything.
	minCollapsedRun = 8
)

// Block is a run of one hunk's lines, as 0-based inclusive indices into Hunk.Lines.
type Block struct {
	Start int
	End   int
}

// Len is how many lines the block covers.
func (b Block) Len() int { return b.End - b.Start + 1 }

// ChangeBlocks returns the runs of lines the hunk shows by default: every changed line, plus
// blockContext lines of context on each side, with runs that overlap or touch merged into one.
//
// Blocks separated by even a single uncovered line stay separate, because that is where git
// -U3 splits a hunk, and reproducing its geometry exactly is the whole point (see blockContext).
//
// A meta line ("\ No newline at end of file") is not a change and cannot anchor a block, but it
// is meaningless on its own, so it inherits the visibility of the line it annotates: a block
// ending on that line is stretched to cover it.
func (h Hunk) ChangeBlocks() []Block {
	var blocks []Block
	for i, l := range h.Lines {
		if l.Kind != LineAdd && l.Kind != LineDelete {
			continue
		}
		b := Block{
			Start: max(0, i-blockContext),
			End:   min(len(h.Lines)-1, i+blockContext),
		}
		blocks = appendMerged(blocks, b)
	}
	for i := range blocks {
		for blocks[i].End+1 < len(h.Lines) && h.Lines[blocks[i].End+1].Kind == LineMeta {
			blocks[i].End++
		}
	}
	// Stretching for a meta line can make two blocks touch, so merge once more. Doing it here
	// rather than inside the loop keeps the -U3 geometry the loop produces intact.
	var merged []Block
	for _, b := range blocks {
		merged = appendMerged(merged, b)
	}
	return merged
}

// appendMerged adds b to blocks, folding it into the last one when they overlap or touch.
// Blocks arrive in ascending order of Start, which is what makes looking only at the last
// one enough.
func appendMerged(blocks []Block, b Block) []Block {
	if n := len(blocks); n > 0 && b.Start <= blocks[n-1].End+1 {
		if b.End > blocks[n-1].End {
			blocks[n-1].End = b.End
		}
		return blocks
	}
	return append(blocks, b)
}

// CollapsedRuns returns the runs of lines the fold hides by default.
//
// Two things have to hold. The hunk must carry a run longer than wideGap, which is what says it
// holds more of the file than an ordinary diff would show — the licence to fold at all. Then
// each run longer than minCollapsedRun is hidden, which leaves the reader roughly what a
// `git diff -U3` would have shown.
func (h Hunk) CollapsedRuns() []Block {
	gaps := h.uncoveredRuns()

	// Nothing folds unless one gap is long enough to prove the hunk carries more of the file
	// than any ordinary `git diff -U<n>` would have shown. Without that proof the hunk renders
	// as it always has, however many lines it happens to hold.
	widest := 0
	for _, g := range gaps {
		widest = max(widest, g.Len())
	}
	if widest <= wideGap {
		return nil
	}

	var runs []Block
	for _, g := range gaps {
		if g.Len() > minCollapsedRun {
			runs = append(runs, g)
		}
	}
	return runs
}

// uncoveredRuns returns what the Change Blocks leave out, in order. A hunk with no change at
// all yields nothing: there is no block to fold away from, and hiding the whole hunk would take
// its @@ header with it and leave the gap unmarked.
func (h Hunk) uncoveredRuns() []Block {
	blocks := h.ChangeBlocks()
	if len(blocks) == 0 {
		return nil
	}
	var gaps []Block
	prev := -1
	for _, b := range blocks {
		if b.Start > prev+1 {
			gaps = append(gaps, Block{Start: prev + 1, End: b.Start - 1})
		}
		prev = b.End
	}
	if prev+1 < len(h.Lines) {
		gaps = append(gaps, Block{Start: prev + 1, End: len(h.Lines) - 1})
	}
	return gaps
}

// IsFoldable reports whether the hunk has anything to fold. Everything that depends on the
// fold — the expanders, the suppressed @@ header, the cap on how much one comment may select —
// keys off this rather than off any guess about how the diff was generated.
func (h Hunk) IsFoldable() bool { return len(h.CollapsedRuns()) > 0 }

// ChangeBlocks returns the file's Change Blocks in the coordinates a comment anchor uses: the
// 1-based Rendered Line Index, counting every rendered line of every hunk and no headers.
//
// Re-anchoring needs them in this form to tell whether a comment sits in a block at all, which
// is what decides if the narrow-diff search rule may speak for it.
func (f File) ChangeBlocks() []Block {
	var out []Block
	base := 0
	for _, h := range f.Hunks {
		for _, b := range h.ChangeBlocks() {
			out = append(out, Block{Start: base + b.Start + 1, End: base + b.End + 1})
		}
		base += len(h.Lines)
	}
	return out
}

const devNull = "/dev/null"

// Pre-compiled at package scope per AGENTS.md section 2.
var (
	hunkHeaderRegex     = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(.*)$`)
	combinedHunkRegex   = regexp.MustCompile(`^@{3,} `)
	fencedCodeRegex     = regexp.MustCompile("^\\s{0,3}(```|~~~)")
	diffGitHeaderRegex  = regexp.MustCompile(`^diff --git (.+)$`)
	diffGitPathsRegex   = regexp.MustCompile(`^a/(.*) b/(.*)$`)
	diffGitQuotedRegexp = regexp.MustCompile(`^"a/(.*)" "b/(.*)"$`)
)

// DetectKind decides whether content is a unified diff or a Markdown document.
//
// Fenced code blocks are skipped, because a Markdown spec that quotes a diff inside a fence is
// still a Markdown spec — misreading one as a diff would strip its formatting and lose every
// existing comment anchor.
func DetectKind(content []byte) Kind {
	lines := strings.Split(string(content), "\n")
	inFence := false
	for i, line := range lines {
		if fencedCodeRegex.MatchString(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		switch {
		case strings.HasPrefix(line, "diff --git "):
			return KindDiff
		case hunkHeaderRegex.MatchString(line), combinedHunkRegex.MatchString(line):
			return KindDiff
		case strings.HasPrefix(line, "--- ") && i+1 < len(lines) && strings.HasPrefix(lines[i+1], "+++ "):
			return KindDiff
		}
	}
	return KindMarkdown
}

// ParseUnifiedDiff turns a unified diff into per-file hunks and lines.
//
// Unrecognised header lines (index, mode, similarity, "Binary files differ") are skipped: they
// carry nothing the review page shows, and failing on them would reject perfectly ordinary
// `git diff` output.
func ParseUnifiedDiff(content []byte) ([]File, error) {
	var (
		files []File
		cur   *File
		hunk  *Hunk
		oldNo int
		newNo int
	)

	// startFile appends and re-points cur, so later paths land on the file being parsed.
	startFile := func(f File) {
		files = append(files, f)
		cur = &files[len(files)-1]
		hunk = nil
	}

	for line := range strings.SplitSeq(string(content), "\n") {
		switch {
		case combinedHunkRegex.MatchString(line):
			// A combined diff (git diff --cc, merge commits) has one column per parent, so a
			// line has no single before/after. Rejecting it is honest; guessing is not.
			return nil, fmt.Errorf("combined diffs are not supported: %s", line)

		case strings.HasPrefix(line, "diff --git "):
			oldPath, newPath := parseDiffGitPaths(line)
			startFile(File{OldPath: oldPath, NewPath: newPath})

		case strings.HasPrefix(line, "--- "):
			path := trimDiffPath(strings.TrimPrefix(line, "--- "))
			// A plain `diff -u` has no "diff --git" line, so the ---/+++ pair opens the file.
			if cur == nil || len(cur.Hunks) > 0 {
				startFile(File{})
			}
			cur.OldPath = path

		case strings.HasPrefix(line, "+++ "):
			if cur == nil {
				startFile(File{})
			}
			cur.NewPath = trimDiffPath(strings.TrimPrefix(line, "+++ "))

		case hunkHeaderRegex.MatchString(line):
			if cur == nil {
				startFile(File{})
			}
			m := hunkHeaderRegex.FindStringSubmatch(line)
			oldNo, _ = strconv.Atoi(m[1])
			newNo, _ = strconv.Atoi(m[3])
			cur.Hunks = append(cur.Hunks, Hunk{Header: line, OldStart: oldNo, NewStart: newNo})
			hunk = &cur.Hunks[len(cur.Hunks)-1]

		case hunk == nil:
			// File header noise (index, old/new mode, similarity, binary notices) — skipped.

		case strings.HasPrefix(line, "+"):
			hunk.Lines = append(hunk.Lines, Line{Kind: LineAdd, Content: line[1:], NewNo: newNo})
			newNo++

		case strings.HasPrefix(line, "-"):
			hunk.Lines = append(hunk.Lines, Line{Kind: LineDelete, Content: line[1:], OldNo: oldNo})
			oldNo++

		case strings.HasPrefix(line, `\`):
			hunk.Lines = append(hunk.Lines, Line{Kind: LineMeta, Content: strings.TrimSpace(line[1:])})

		case strings.HasPrefix(line, " "):
			hunk.Lines = append(hunk.Lines, Line{Kind: LineContext, Content: line[1:], OldNo: oldNo, NewNo: newNo})
			oldNo++
			newNo++

		case line == "":
			// A context line whose single leading space was stripped in transit, or the trailing
			// newline of the file. Either way it ends the hunk's run of lines only if nothing
			// follows; treating it as an empty context line keeps line numbering aligned.
			hunk.Lines = append(hunk.Lines, Line{Kind: LineContext, OldNo: oldNo, NewNo: newNo})
			oldNo++
			newNo++

		default:
			// Anything else ends the hunk: git's trailing "-- \n<version>" signature, or prose
			// wrapped around a pasted diff.
			hunk = nil
		}
	}

	// A file's trailing empty context line is an artefact of splitting on "\n", not a line of
	// the diff. Only the very last line of the input can be one.
	trimTrailingArtifact(files)

	return files, nil
}

// trimTrailingArtifact drops the phantom context line produced by a diff that ends with a
// newline, which is every well-formed diff.
func trimTrailingArtifact(files []File) {
	if len(files) == 0 {
		return
	}
	f := &files[len(files)-1]
	if len(f.Hunks) == 0 {
		return
	}
	h := &f.Hunks[len(f.Hunks)-1]
	if n := len(h.Lines); n > 0 && h.Lines[n-1].Kind == LineContext && h.Lines[n-1].Content == "" {
		h.Lines = h.Lines[:n-1]
	}
}

// parseDiffGitPaths reads the a/… b/… pair from a "diff --git" line. Paths containing spaces
// make this ambiguous in general; the ---/+++ lines that follow overwrite whatever is guessed
// here, so this only has to serve the header-only cases (a pure mode or rename change).
func parseDiffGitPaths(line string) (string, string) {
	rest := strings.TrimPrefix(line, "diff --git ")
	if m := diffGitQuotedRegexp.FindStringSubmatch(rest); m != nil {
		return m[1], m[2]
	}
	if m := diffGitPathsRegex.FindStringSubmatch(rest); m != nil {
		return m[1], m[2]
	}
	return rest, rest
}

// trimDiffPath strips the a/ or b/ prefix and the tab-separated timestamp that `diff -u`
// appends, leaving the path the file is known by.
func trimDiffPath(s string) string {
	if i := strings.IndexByte(s, '\t'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if s == devNull {
		return s
	}
	if strings.HasPrefix(s, "a/") || strings.HasPrefix(s, "b/") {
		return s[2:]
	}
	return s
}

// diffAnchorRangeRegex matches the "<start>-<end>" tail of a diff anchor.
var diffAnchorRangeRegex = regexp.MustCompile(`^(\d+)-(\d+)$`)

// diffFileAnchorTail marks an anchor that means the whole file rather than a range of its
// lines. It is a word rather than a range like "0-0" so the two forms cannot be confused by a
// reader — or by an agent — and so a file comment survives every edit inside the file.
const diffFileAnchorTail = "file"

// FormatDiffAnchor builds the anchor for a commented line range: "<display path>#<start>-<end>".
//
// start and end are 1-based indices into the file's rendered diff lines — added, removed and
// context lines all counted, @@ headers not — and NOT source line numbers. A removed line has no
// number on the new side, so source numbering could not express "do not delete this line", nor a
// selection spanning a removal and its replacement, which is exactly what a suggestion is for.
func FormatDiffAnchor(path string, start, end int) string {
	return fmt.Sprintf("%s#%d-%d", path, start, end)
}

// ParseDiffAnchor reads an anchor back. ok is false for anything that is not a diff anchor —
// a Markdown "spec-element-7", say — so callers can pass those through untouched.
//
// The split is on the LAST '#', because a path may contain one: FormatDiffAnchor always ends in
// "#<digits>-<digits>", so an anchor into a file literally named "a#1-2" reads back correctly
// from "a#1-2#3-4". Splitting on the first '#' would not.
func ParseDiffAnchor(anchor string) (path string, start, end int, ok bool) {
	i := strings.LastIndexByte(anchor, '#')
	if i < 0 {
		return "", 0, 0, false
	}
	m := diffAnchorRangeRegex.FindStringSubmatch(anchor[i+1:])
	if m == nil {
		return "", 0, 0, false
	}
	start, _ = strconv.Atoi(m[1])
	end, _ = strconv.Atoi(m[2])
	if start < 1 || end < start {
		return "", 0, 0, false
	}
	return anchor[:i], start, end, true
}

// FormatDiffFileAnchor builds the anchor for a comment on a file as a whole: "<path>#file".
//
// Some review comments are about the change to a file rather than about any line in it — "this
// belongs in the other package", "where are the tests" — and pinning those to an arbitrary line
// both misplaces them and sends them outdated the moment that line is edited.
func FormatDiffFileAnchor(path string) string {
	return path + "#" + diffFileAnchorTail
}

// ParseDiffFileAnchor reads a whole-file anchor back. Like ParseDiffAnchor it splits on the last
// '#', so a path containing one still round-trips.
func ParseDiffFileAnchor(anchor string) (path string, ok bool) {
	i := strings.LastIndexByte(anchor, '#')
	if i < 0 || anchor[i+1:] != diffFileAnchorTail {
		return "", false
	}
	return anchor[:i], true
}

// RenderDiff compiles parsed diff files into the same interactive review page RenderSpec
// produces for Markdown.
//
// postProcessHTML is deliberately not run here: its badge and callout rewriting would corrupt
// code, and there is no Markdown to enhance. Every diff-derived string is escaped instead —
// nothing on this path has been through a HTML-producing renderer.
func RenderDiff(files []File) ([]byte, error) {
	meta := SpecMetadata{
		Mode:  string(KindDiff),
		Title: diffTitle(files),
		Stats: diffStats(files),
		Body:  renderDiffBody(files),
	}
	return executeTemplate(meta)
}

func diffTitle(files []File) string {
	if len(files) == 1 {
		return html.EscapeString(files[0].DisplayPath())
	}
	return "Diff review"
}

func diffStats(files []File) string {
	added, deleted := 0, 0
	for _, f := range files {
		for _, l := range f.Lines() {
			switch l.Kind {
			case LineAdd:
				added++
			case LineDelete:
				deleted++
			}
		}
	}
	return fmt.Sprintf("%s · +%d −%d", pluralFiles(len(files)), added, deleted)
}

func pluralFiles(n int) string {
	if n == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", n)
}

// renderDiffBody builds the document body: one section per file, one row per diff line.
//
// Each row is emitted on a single output line because .diff-line is white-space: pre-wrap — a
// newline between its spans would render as a line break inside the row.
func renderDiffBody(files []File) string {
	var b strings.Builder
	b.WriteString(`<div class="diff-view">` + "\n")
	for _, f := range files {
		path := f.DisplayPath()
		b.WriteString(`<section class="diff-file">` + "\n")
		// data-file makes the header a comment target in its own right: a comment about the
		// file as a whole anchors here rather than to a line that happens to be in it.
		// data-status carries what happened to the file, so the contents rail can show it as a mark
		// beside the name instead of repeating the words after it.
		b.WriteString(`<h2 class="diff-file-header" data-file="` + html.EscapeString(path) +
			`" data-status="` + string(f.Status()) + `">` +
			html.EscapeString(path) + renderRenameNote(f) + "</h2>\n")
		if len(f.Hunks) == 0 {
			b.WriteString(`<p class="diff-empty">No textual changes.</p>` + "\n")
		}
		index := 0
		for _, h := range f.Hunks {
			base := index
			runs := h.CollapsedRuns()
			hunkAttrs := ""
			if len(runs) > 0 {
				hunkAttrs = " data-foldable"
			}
			b.WriteString(`<div class="diff-hunk"` + hunkAttrs + ">\n")
			// The @@ header is dropped only for a file that is one foldable hunk, where it reads
			// "@@ -1,500 +1,502 @@" and says nothing. Several hunks mean the diff really does skip
			// lines between them, and that gap has to stay marked — unmarked, it would read as
			// something an expander could open.
			if len(f.Hunks) > 1 || len(runs) == 0 {
				b.WriteString(`<div class="diff-hunk-header">` + html.EscapeString(h.Header) + "</div>\n")
			}
			wsOnly := whitespaceOnlyMask(h.Lines)
			collapsed := collapsedMask(len(h.Lines), runs)
			for i, l := range h.Lines {
				if r, ok := runBeginningAt(runs, i); ok {
					b.WriteString(renderExpander(path, h, r, base) + "\n")
				}
				index++
				b.WriteString(renderDiffLine(path, index, l, wsOnly[i], collapsed[i]) + "\n")
			}
			b.WriteString("</div>\n")
		}
		b.WriteString("</section>\n")
	}
	b.WriteString("</div>\n")
	return b.String()
}

// renderRenameNote spells out a rename, which the display path alone cannot show.
func renderRenameNote(f File) string {
	switch f.Status() {
	case FileAdded:
		return ` <span class="diff-file-note">added</span>`
	case FileDeleted:
		return ` <span class="diff-file-note">deleted</span>`
	case FileRenamed:
		// The one case the display path cannot show on its own: where the file came from.
		return ` <span class="diff-file-note">renamed from ` + html.EscapeString(f.OldPath) + `</span>`
	default:
		return ""
	}
}

var lineKindClass = map[LineKind]string{
	LineAdd:     "diff-add",
	LineDelete:  "diff-del",
	LineContext: "diff-ctx",
	LineMeta:    "diff-meta",
}

var lineKindMarker = map[LineKind]string{
	LineAdd:     "+",
	LineDelete:  "-",
	LineContext: " ",
	LineMeta:    `\`,
}

// renderDiffLine emits one row.
//
// data-file and data-line-index are the coordinates a comment anchor is built from: the index
// is 1-based within the file and counts every rendered line, hunk headers excluded.
//
// The single line-number column shows each line's number on its own side — the old file's for a
// deletion, the new file's otherwise — because a deletion has no number on the new side and a
// blank there would hide which line the comment is about.
func renderDiffLine(path string, index int, l Line, wsOnly, collapsed bool) string {
	no := ""
	noClass := "diff-no"
	switch {
	case l.Kind == LineDelete && l.OldNo > 0:
		no = strconv.Itoa(l.OldNo)
		noClass = "diff-no diff-no-old"
	case l.NewNo > 0:
		no = strconv.Itoa(l.NewNo)
	}

	// data-ws-only is emitted on both halves of a whitespace-only pair. The toggle then hides
	// the deletion and restyles the addition purely in CSS, so every line keeps its
	// data-line-index and comments anchored to it survive the switch.
	ws := ""
	if wsOnly {
		ws = " data-ws-only"
	}

	// data-collapsed follows the same rule as data-ws-only: the row stays in the DOM and CSS
	// decides whether it shows, so every data-line-index holds still and the comments anchored
	// to them survive being folded away and opened again.
	fold := ""
	if collapsed {
		fold = " data-collapsed"
	}

	return fmt.Sprintf(
		`<div class="diff-line %s" data-file="%s" data-line-index="%d"%s%s><span class="%s">%s</span><span class="diff-marker">%s</span><span class="diff-code">%s</span></div>`,
		lineKindClass[l.Kind],
		html.EscapeString(path),
		index,
		ws,
		fold,
		noClass,
		no,
		lineKindMarker[l.Kind],
		html.EscapeString(l.Content),
	)
}

// collapsedMask marks, for each line of one hunk, whether it starts out folded away.
func collapsedMask(n int, runs []Block) []bool {
	mask := make([]bool, n)
	for _, r := range runs {
		for i := r.Start; i <= r.End; i++ {
			mask[i] = true
		}
	}
	return mask
}

func runBeginningAt(runs []Block, i int) (Block, bool) {
	for _, r := range runs {
		if r.Start == i {
			return r, true
		}
	}
	return Block{}, false
}

// renderExpander emits the control that opens a Collapsed Run.
//
// One per run, carrying both directions. A run can be opened from its top or from its bottom,
// but a control at each end would be two bars stacked on top of each other: everything between
// them is folded away by definition, so the two ends are always adjacent on screen no matter how
// much of the run is open.
//
// The page keeps the bar against the first line still folded. It moves the bar; it never moves a
// .diff-line, which is what keeps the Rendered Line Index still.
func renderExpander(path string, h Hunk, r Block, base int) string {
	attrs := fmt.Sprintf(
		`class="diff-expander" data-file="%s" data-run-start="%d" data-run-end="%d" data-run-key="%s"`,
		html.EscapeString(path),
		base+r.Start+1,
		base+r.End+1,
		runKey(path, h, r),
	)
	return `<div ` + attrs + `><button type="button" class="diff-expander-button" data-expand="down" aria-label="Show the lines below">&#8595;</button>` +
		`<button type="button" class="diff-expander-button" data-expand="up" aria-label="Show the lines above">&#8593;</button>` +
		fmt.Sprintf(`<span class="diff-expander-count">%d hidden lines</span>`, r.Len()) +
		`<button type="button" class="diff-expander-fold" data-expand="fold" aria-label="Hide these lines again" hidden>` + foldIcon + `</button></div>`
}

// foldIcon is two chevrons closing on each other, which is what folding the run does. An arrow
// pointing both ways says "this moves" rather than "this shuts", and read next to the ↓ and ↑
// that open the run it looked like a third way to open it.
const foldIcon = `<svg class="diff-expander-icon" viewBox="0 0 14 12" aria-hidden="true">` +
	`<path d="M2.5 2 L7 4 L11.5 2"/><path d="M2.5 10 L7 8 L11.5 10"/></svg>`

// runKey identifies a Collapsed Run by the text around it rather than by where it falls.
//
// Rendered Line Index moves every time the agent regenerates the diff — which is exactly when
// a reader's expansion has to be restored — so the index cannot name the run. The lines on
// either side of it can: they are the edges of the Change Blocks the run separates, and they
// only move if someone edits them. A run at the start or end of a file simply has fewer
// neighbours, and the missing side counts as empty.
func runKey(path string, h Hunk, r Block) string {
	parts := []string{path, strconv.Itoa(r.Len())}
	for i := r.Start - blockContext; i < r.Start; i++ {
		parts = append(parts, lineContentAt(h, i))
	}
	for i := r.End + 1; i <= r.End+blockContext; i++ {
		parts = append(parts, lineContentAt(h, i))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:8])
}

func lineContentAt(h Hunk, i int) string {
	if i < 0 || i >= len(h.Lines) {
		return ""
	}
	return h.Lines[i].Content
}

// whitespaceOnlyMask marks, for each line of one hunk, whether its change is whitespace-only —
// the pairs the "Hide whitespace changes" toggle folds away.
//
// reviewer never runs git, so `git diff -w` cannot be re-run against the sources: the judgement
// has to come out of the already-parsed hunk. The approximation is deliberately conservative —
// a run of deletions is paired 1:1 with the run of additions that follows it, and the pair only
// counts when both runs are the same length and every row matches once whitespace is stripped.
// A block whose lengths disagree folds nothing, because the 1:1 pairing that a longer or
// shorter counterpart implies would be guesswork, and a wrong fold hides a real edit.
func whitespaceOnlyMask(lines []Line) []bool {
	if len(lines) == 0 {
		return nil
	}
	mask := make([]bool, len(lines))
	for i := 0; i < len(lines); {
		if lines[i].Kind != LineDelete {
			i++
			continue
		}
		delStart := i
		for i < len(lines) && lines[i].Kind == LineDelete {
			i++
		}
		addStart := i
		for i < len(lines) && lines[i].Kind == LineAdd {
			i++
		}
		if addStart-delStart != i-addStart {
			continue
		}
		if pairsDifferOnlyInWhitespace(lines[delStart:addStart], lines[addStart:i]) {
			for j := delStart; j < i; j++ {
				mask[j] = true
			}
		}
	}
	return mask
}

// pairsDifferOnlyInWhitespace reports whether two equal-length runs match row for row once all
// whitespace is removed, which is `git diff -w` (ignore-all-space) applied to a fixed pairing.
func pairsDifferOnlyInWhitespace(deleted, added []Line) bool {
	for i := range deleted {
		if stripWhitespace(deleted[i].Content) != stripWhitespace(added[i].Content) {
			return false
		}
	}
	return true
}

func stripWhitespace(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}
