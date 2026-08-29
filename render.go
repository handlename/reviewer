package reviewer

import (
	"bytes"
	_ "embed"
	"fmt"
	"html"
	"regexp"
	"strings"
	"text/template"

	meta "github.com/yuin/goldmark-meta"
	"github.com/yuin/goldmark/parser"
)

//go:embed references/template.html
var defaultTemplate string

type SpecMetadata struct {
	Title   string
	Version string
	Date    string
	Body    string

	// Mode tells the page which review it is running: "markdown" or "diff". The template
	// branches on it because the two have different comment targets — a document block for
	// Markdown, a line range for a diff — and the wrong initializer silently comments nothing.
	Mode string
	// Stats replaces Version/Date in the diff contents rail. A diff has no front matter, so those two
	// would otherwise show a made-up version and an unknown date.
	Stats string
}

// Pre-compiled global regular expressions to avoid runtime compilation overhead.
var (
	mermaidRegex   = regexp.MustCompile(`(?s)<pre><code class="language-mermaid">(.*?)</code></pre>`)
	calloutRegex   = regexp.MustCompile(`(?s)<blockquote>\s*<p>\s*\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*(.*?)\s*</p>(.*?)</blockquote>`)
	codeBlockRegex = regexp.MustCompile(`(?s)<code>.*?</code>|<pre>.*?</pre>`)
)

type badgeRegex struct {
	reBracket *regexp.Regexp
	reStrong  *regexp.Regexp
	repl      string
}

// Static pre-compiled regex badges
var precompiledBadges = []badgeRegex{
	{regexp.MustCompile(`\[Must\]`), regexp.MustCompile(`<strong>Must</strong>`), `<span class="badge badge-must">Must</span>`},
	{regexp.MustCompile(`\[Should\]`), regexp.MustCompile(`<strong>Should</strong>`), `<span class="badge badge-should">Should</span>`},
	{regexp.MustCompile(`\[Could\]`), regexp.MustCompile(`<strong>Could</strong>`), `<span class="badge badge-could">Could</span>`},
	{regexp.MustCompile(`\[Wont\]`), regexp.MustCompile(`<strong>Wont</strong>`), `<span class="badge badge-wont">Wont</span>`},
	{regexp.MustCompile(`\[Confirmed\]`), regexp.MustCompile(`<strong>Confirmed</strong>`), `<span class="badge badge-confirmed">Confirmed</span>`},
	{regexp.MustCompile(`\[Inferred\]`), regexp.MustCompile(`<strong>Inferred</strong>`), `<span class="badge badge-inferred">Inferred</span>`},
	{regexp.MustCompile(`\[Assumption\]`), regexp.MustCompile(`<strong>Assumption</strong>`), `<span class="badge badge-assumption">Assumption</span>`},
}

// Render compiles a review target — a Markdown document or a unified diff — into the review
// page. The kind is decided from the content, so every entry point (serve, build, GET /) feeds
// the same bytes in and gets the right renderer without knowing which it asked for.
func Render(content []byte) ([]byte, error) {
	if DetectKind(content) == KindDiff {
		files, err := ParseUnifiedDiff(content)
		if err != nil {
			return nil, fmt.Errorf("failed to parse diff: %w", err)
		}
		return RenderDiff(files)
	}
	return RenderSpec(content)
}

// RenderSpec compiles markdown to fully designed interactive HTML
func RenderSpec(mdContent []byte) ([]byte, error) {
	markdown := newMarkdown()

	var buf bytes.Buffer
	context := parser.NewContext()
	if err := markdown.Convert(mdContent, &buf, parser.WithContext(context)); err != nil {
		return nil, fmt.Errorf("failed to convert markdown: %w", err)
	}

	metaData := meta.Get(context)
	title, _ := metaData["title"].(string)
	if title == "" {
		title = "Specification"
	}
	version, _ := metaData["version"].(string)
	if version == "" {
		version = "0.1.0"
	}
	date, _ := metaData["date"].(string)
	if date == "" {
		date = "Unknown"
	}

	htmlBody := postProcessHTML(buf.String())

	// Escape strings to prevent potential XSS injection through text/template
	specMeta := SpecMetadata{
		Title:   html.EscapeString(title),
		Version: html.EscapeString(version),
		Date:    html.EscapeString(date),
		Body:    htmlBody,
		Mode:    string(KindMarkdown),
	}

	return executeTemplate(specMeta)
}

// executeTemplate renders the page shell around an already-prepared body.
//
// The template is text/template, not html/template, so nothing here escapes anything: every
// field must arrive escaped. The Markdown path relies on goldmark having done it; the diff path
// escapes each string as it builds the body.
func executeTemplate(meta SpecMetadata) ([]byte, error) {
	tmpl, err := template.New("spec").Parse(defaultTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to parse template: %w", err)
	}

	var output bytes.Buffer
	if err := tmpl.Execute(&output, meta); err != nil {
		return nil, fmt.Errorf("failed to execute template: %w", err)
	}

	return output.Bytes(), nil
}

func postProcessHTML(htmlStr string) string {
	// 1. Process Mermaid Blocks first
	// (Converts mermaid pre/code blocks to figcaption/div wrapper, avoiding standard code masking)
	htmlStr = mermaidRegex.ReplaceAllStringFunc(htmlStr, func(match string) string {
		submatches := mermaidRegex.FindStringSubmatch(match)
		if len(submatches) < 2 {
			return match
		}
		content := submatches[1]
		// Unescape standard HTML entities inside mermaid
		content = strings.ReplaceAll(content, "&amp;", "&")
		content = strings.ReplaceAll(content, "&lt;", "<")
		content = strings.ReplaceAll(content, "&gt;", ">")
		content = strings.ReplaceAll(content, "&#34;", "\"")
		content = strings.ReplaceAll(content, "&#x27;", "'")
		return fmt.Sprintf(`<figure class="diagram-container"><div class="mermaid">%s</div><figcaption>Architecture & Flow Diagram</figcaption></figure>`, strings.TrimSpace(content))
	})

	// 2. Mask remaining standard <code> and <pre> blocks to protect them from regex pollution
	var maskedBlocks []string
	htmlStr = codeBlockRegex.ReplaceAllStringFunc(htmlStr, func(match string) string {
		maskedBlocks = append(maskedBlocks, match)
		return fmt.Sprintf("<!--CODE_BLOCK_PLACEHOLDER_%d-->", len(maskedBlocks)-1)
	})

	// 3. Spec Tables
	htmlStr = strings.ReplaceAll(htmlStr, "<table>", `<table class="spec-table">`)

	// 4. Priority & Status Badges
	for _, badge := range precompiledBadges {
		htmlStr = badge.reBracket.ReplaceAllString(htmlStr, badge.repl)
		htmlStr = badge.reStrong.ReplaceAllString(htmlStr, badge.repl)
	}

	// 5. Callout Cards
	htmlStr = calloutRegex.ReplaceAllStringFunc(htmlStr, func(match string) string {
		submatches := calloutRegex.FindStringSubmatch(match)
		if len(submatches) < 4 {
			return match
		}
		cType := strings.ToUpper(submatches[1])
		body := submatches[2]
		extra := submatches[3]

		class := "callout-info"
		switch cType {
		case "WARNING":
			class = "callout-warning"
		case "CAUTION":
			class = "callout-danger"
		}

		return fmt.Sprintf(`<div class="callout %s"><div class="callout-title">%s</div><p>%s</p>%s</div>`, class, cType, body, extra)
	})

	// 6. Restore the masked code blocks
	for i, block := range maskedBlocks {
		placeholder := fmt.Sprintf("<!--CODE_BLOCK_PLACEHOLDER_%d-->", i)
		htmlStr = strings.Replace(htmlStr, placeholder, block, 1)
	}

	return htmlStr
}
