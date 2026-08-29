package reviewer

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	meta "github.com/yuin/goldmark-meta"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
)

var (
	htmlTagRegex = regexp.MustCompile(`<[^>]*>`)
	loneTableRow = regexp.MustCompile(`^\|.*\|$`)
)

// NormalizeAnchorQuote reduces an Anchor Quote to the text the browser will see, so a quote
// copied from the Markdown source can be matched against the rendered block it came from.
//
// The agent quotes the source — `**bold**`, a backtick code span, a `##` or `- ` marker — while
// the page matches against an element's textContent, which carries none of that. Every quote
// with inline syntax therefore missed the very block it was copied from, and the thread lost its
// target. The quote is put through the same renderer as the document body rather than having its
// syntax stripped by hand: a second, approximate Markdown parser here is exactly what would drift
// from what the page displays.
func NormalizeAnchorQuote(quote string) string {
	var buf bytes.Buffer
	if err := newMarkdown().Convert([]byte(completeLoneTableRow(quote)), &buf, parser.WithContext(parser.NewContext())); err != nil {
		// An unparseable quote is not an error: it falls back to the raw text and simply fails to
		// find a target, which is where it already was.
		return collapseWhitespace(quote)
	}
	return collapseWhitespace(stripHTMLTags(postProcessHTML(buf.String())))
}

// newMarkdown builds the converter both the document body and NormalizeAnchorQuote go through.
// One constructor because the two must agree: a quote normalized by a different parser than the
// one that rendered the page is a quote that matches text the page never displayed.
func newMarkdown() goldmark.Markdown {
	return goldmark.New(
		goldmark.WithExtensions(
			meta.Meta,
			extension.GFM,
		),
	)
}

// completeLoneTableRow gives a table row quoted on its own the delimiter row GFM needs to read it
// as a table. A `tr` carries an Anchor of its own, so a quoted row has to resolve; without the
// delimiter goldmark reads the line as a paragraph and its pipes survive into the text, which no
// rendered row contains.
//
// A pipe inside a code span throws the column count off and the table then does not parse — which
// leaves the quote exactly where it was, so the miscount costs nothing it did not already cost.
func completeLoneTableRow(quote string) string {
	row := strings.TrimSpace(quote)
	if strings.Contains(row, "\n") || !loneTableRow.MatchString(row) {
		return quote
	}
	columns := strings.Count(row, "|") - 1
	if columns < 1 {
		return quote
	}
	return row + "\n|" + strings.Repeat(" --- |", columns)
}

// stripHTMLTags reduces rendered HTML to its text, the way textContent does in the browser.
// It runs on goldmark's own output, where a `<` that is not a tag has already been escaped.
//
// A tag becomes nothing, not a space: textContent draws no whitespace from an element, so a space
// here would put one where the browser has none — enough to lose the match on a code span that
// ends a sentence. Block elements keep their separation from the newlines goldmark writes between
// them, which the browser reads as text nodes too.
func stripHTMLTags(htmlStr string) string {
	return htmlUnescape(htmlTagRegex.ReplaceAllString(htmlStr, ""))
}

// htmlUnescape resolves the entities goldmark writes. html.UnescapeString is not used because it
// also resolves entities the source wrote literally as text (`&amp;amp;`), which textContent
// leaves alone.
func htmlUnescape(s string) string {
	return strings.NewReplacer(
		"&amp;", "&",
		"&lt;", "<",
		"&gt;", ">",
		"&#34;", `"`,
		"&quot;", `"`,
		"&#39;", "'",
		"&#x27;", "'",
	).Replace(s)
}

// collapseWhitespace mirrors normalizeQuote in references/template.html. Both sides of the
// comparison have to be folded the same way, so the rule is stated identically in both places.
func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
