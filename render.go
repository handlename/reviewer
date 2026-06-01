package reviewer

import (
	"bytes"
	_ "embed"
	"fmt"
	"regexp"
	"strings"
	"text/template"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
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
}

// RenderSpec compiles markdown to fully designed interactive HTML
func RenderSpec(mdContent []byte) ([]byte, error) {
	markdown := goldmark.New(
		goldmark.WithExtensions(
			meta.Meta,
			extension.GFM,
		),
	)

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

	specMeta := SpecMetadata{
		Title:   title,
		Version: version,
		Date:    date,
		Body:    htmlBody,
	}

	tmpl, err := template.New("spec").Parse(defaultTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to parse template: %w", err)
	}

	var output bytes.Buffer
	if err := tmpl.Execute(&output, specMeta); err != nil {
		return nil, fmt.Errorf("failed to execute template: %w", err)
	}

	return output.Bytes(), nil
}

func postProcessHTML(html string) string {
	// 1. Spec Tables
	html = strings.ReplaceAll(html, "<table>", `<table class="spec-table">`)

	// 2. Mermaid Blocks
	// Goldmark outputs mermaid blocks as: <pre><code class="language-mermaid">...</code></pre>
	mermaidRegex := regexp.MustCompile(`(?s)<pre><code class="language-mermaid">(.*?)</code></pre>`)
	html = mermaidRegex.ReplaceAllStringFunc(html, func(match string) string {
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

	// 3. Priority & Status Badges
	badges := map[string]string{
		"Must":       `<span class="badge badge-must">Must</span>`,
		"Should":     `<span class="badge badge-should">Should</span>`,
		"Could":      `<span class="badge badge-could">Could</span>`,
		"Wont":       `<span class="badge badge-wont">Wont</span>`,
		"Confirmed":  `<span class="badge badge-confirmed">Confirmed</span>`,
		"Inferred":   `<span class="badge badge-inferred">Inferred</span>`,
		"Assumption": `<span class="badge badge-assumption">Assumption</span>`,
	}

	for key, val := range badges {
		// Replace [Must], [Should]
		reBracket := regexp.MustCompile(fmt.Sprintf(`\[%s\]`, key))
		html = reBracket.ReplaceAllString(html, val)

		// Replace <strong>Must</strong>, <strong>Should</strong>
		reStrong := regexp.MustCompile(fmt.Sprintf(`<strong>%s</strong>`, key))
		html = reStrong.ReplaceAllString(html, val)
	}

	// 4. Callout Cards
	// Matches blockquotes starting with [!NOTE], etc.
	calloutRegex := regexp.MustCompile(`(?s)<blockquote>\s*<p>\s*\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*(.*?)\s*</p>(.*?)</blockquote>`)
	html = calloutRegex.ReplaceAllStringFunc(html, func(match string) string {
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

	return html
}
