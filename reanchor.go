package reviewer

// Re-anchoring is how a comment survives the agent regenerating the diff.
//
// It is computed for DISPLAY ONLY, when the page asks for the comments, and is never written
// back to the sidecar. Persistence rides the existing path: the browser holds the re-anchored
// comments and posts them back on the next submit. A GET that writes would race the submit
// handler on a read-modify-write and could drop comments.
//
// Every function here is pure, so the rules can be tested without a session or a filesystem.

// FindFile looks a file up by the path a comment anchor names — the display path, which is the
// new path except for a deletion. A renamed file is therefore found under its new name, which is
// the one its anchors were written with.
func FindFile(files []File, path string) (File, bool) {
	for _, f := range files {
		if f.DisplayPath() == path {
			return f, true
		}
	}
	return File{}, false
}

// ReAnchor locates anchorLines in file and reports where the comment now belongs.
//
// Rule 1 (short circuit): if the recorded range still holds exactly that content, keep it.
// Rule 2: otherwise search the file. Exactly one match moves the comment; zero or several
// leave it outdated.
//
// Rule 1 is not an optimisation. Searching by content alone would send most single-line
// comments outdated on the first reload with the diff byte-for-byte unchanged: lines like "}",
// "return nil" or a blank line occur several times in one file, so the match count reaches two
// and the search gives up. The comment would vanish from the diff moments after being written.
//
// Matching compares Line.Content only, never Line.Kind. A line that was "+foo" in one round and
// " foo" in the next — which happens constantly as a side effect of the agent fixing something
// upstream — is still the same line to the reader, and to the comment.
func ReAnchor(prevStart, prevEnd int, anchorLines []string, file File) (start, end int, ok bool) {
	if len(anchorLines) == 0 {
		return 0, 0, false
	}

	if prevEnd-prevStart+1 == len(anchorLines) {
		// Checked inside its hunk, not against the flattened file, so rule 1 obeys the same
		// "never across a hunk boundary" constraint the search below does.
		if hunk, offset, ok := locateInHunk(file, prevStart, prevEnd); ok && matchesAt(hunk.Lines, offset, anchorLines) {
			return prevStart, prevEnd, true
		}
	}

	// The search never crosses a hunk: lines on either side of a @@ header are far apart in the
	// real file, so a "match" spanning one would be an accident of rendering, not the same code.
	var matches []int
	base := 0
	for _, h := range file.Hunks {
		for i := 0; i+len(anchorLines) <= len(h.Lines); i++ {
			if matchesAt(h.Lines, i, anchorLines) {
				matches = append(matches, base+i+1) // file-wide, 1-based
			}
		}
		base += len(h.Lines)
	}
	if len(matches) != 1 {
		return 0, 0, false
	}
	return matches[0], matches[0] + len(anchorLines) - 1, true
}

// locateInHunk maps a file-wide 1-based range onto the hunk that wholly contains it, returning
// the 0-based offset of its first line within that hunk.
func locateInHunk(file File, start, end int) (Hunk, int, bool) {
	if start < 1 || end < start {
		return Hunk{}, 0, false
	}
	base := 0
	for _, h := range file.Hunks {
		if start > base && end <= base+len(h.Lines) {
			return h, start - base - 1, true
		}
		base += len(h.Lines)
	}
	return Hunk{}, 0, false
}

func matchesAt(lines []Line, i int, want []string) bool {
	if i < 0 || i+len(want) > len(lines) {
		return false
	}
	for j, w := range want {
		if lines[i+j].Content != w {
			return false
		}
	}
	return true
}

// reAnchorComments returns the comments as they should be displayed against the current diff.
//
// Comments whose anchor is not a diff anchor — every Markdown comment — are passed through
// untouched, Outdated included: this same handler serves the Markdown review, and marking those
// outdated would strip its indicators and connector lines.
func reAnchorComments(comments []Comment, files []File) []Comment {
	out := make([]Comment, len(comments))
	copy(out, comments)

	for i := range out {
		path, start, end, ok := ParseDiffAnchor(out[i].Anchor)
		if !ok {
			continue
		}
		file, found := FindFile(files, path)
		if !found {
			out[i].Outdated = true
			continue
		}
		newStart, newEnd, ok := ReAnchor(start, end, out[i].AnchorLines, file)
		if !ok {
			out[i].Outdated = true
			continue
		}
		out[i].Anchor = FormatDiffAnchor(path, newStart, newEnd)
		// Set explicitly rather than left alone: Outdated is persisted, so a comment that went
		// outdated once and now matches again would otherwise stay flagged forever.
		out[i].Outdated = false
	}

	return out
}
