package reviewer

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
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
type Hunk struct {
	Header string
	Lines  []Line
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

	for _, line := range strings.Split(string(content), "\n") {
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
			cur.Hunks = append(cur.Hunks, Hunk{Header: line})
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
// Each row is emitted on a single output line because .diff-line is white-space: pre — a
// newline between its spans would render as a line break inside the row.
func renderDiffBody(files []File) string {
	var b strings.Builder
	b.WriteString(`<div class="diff-view">` + "\n")
	for _, f := range files {
		path := f.DisplayPath()
		b.WriteString(`<section class="diff-file">` + "\n")
		// data-file makes the header a comment target in its own right: a comment about the
		// file as a whole anchors here rather than to a line that happens to be in it.
		// data-status carries what happened to the file, so the sidebar can show it as a mark
		// beside the name instead of repeating the words after it.
		b.WriteString(`<h2 class="diff-file-header" data-file="` + html.EscapeString(path) +
			`" data-status="` + string(f.Status()) + `">` +
			html.EscapeString(path) + renderRenameNote(f) + "</h2>\n")
		if len(f.Hunks) == 0 {
			b.WriteString(`<p class="diff-empty">No textual changes.</p>` + "\n")
		}
		index := 0
		for _, h := range f.Hunks {
			b.WriteString(`<div class="diff-hunk">` + "\n")
			b.WriteString(`<div class="diff-hunk-header">` + html.EscapeString(h.Header) + "</div>\n")
			for _, l := range h.Lines {
				index++
				b.WriteString(renderDiffLine(path, index, l) + "\n")
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
func renderDiffLine(path string, index int, l Line) string {
	no := ""
	noClass := "diff-no"
	switch {
	case l.Kind == LineDelete && l.OldNo > 0:
		no = strconv.Itoa(l.OldNo)
		noClass = "diff-no diff-no-old"
	case l.NewNo > 0:
		no = strconv.Itoa(l.NewNo)
	}

	return fmt.Sprintf(
		`<div class="diff-line %s" data-file="%s" data-line-index="%d"><span class="%s">%s</span><span class="diff-marker">%s</span><span class="diff-code">%s</span></div>`,
		lineKindClass[l.Kind],
		html.EscapeString(path),
		index,
		noClass,
		no,
		lineKindMarker[l.Kind],
		html.EscapeString(l.Content),
	)
}
