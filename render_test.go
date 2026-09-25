package reviewer

import (
	"strings"
	"testing"
)

func TestRenderSpec(t *testing.T) {
	mdContent := `---
title: "Test Spec"
version: "1.2.3"
date: "2026-06-01"
---

# Heading 1

This is a [Must] priority. And **Should** is also a priority.

> [!WARNING]
> This is a caution/warning message block.

| Feature | Status |
|---|---|
| A | [Confirmed] |

Here is an inline code block: ` + "`[Must]`" + `.
And a standard fenced code block:
` + "```go" + `
package payment
// [Confirmed] and **Should** should remain raw
` + "```" + `

` + "```" + `mermaid
graph TD
  A -> B
` + "```"

	output, err := RenderSpec([]byte(mdContent), "")
	if err != nil {
		t.Fatalf("failed to render: %v", err)
	}

	html := string(output)

	if !strings.Contains(html, "<title>Test Spec</title>") {
		t.Errorf("expected title 'Test Spec' in HTML head, got missing")
	}
	if !strings.Contains(html, "<h2>Test Spec</h2>") {
		t.Errorf("expected header 'Test Spec' in HTML body, got missing")
	}
	if !strings.Contains(html, "Version: 1.2.3") {
		t.Errorf("expected version '1.2.3', got missing")
	}
	if !strings.Contains(html, "Date: 2026-06-01") {
		t.Errorf("expected date '2026-06-01', got missing")
	}
	if !strings.Contains(html, "<h1>Heading 1</h1>") {
		t.Errorf("expected heading 1, got missing")
	}
	if !strings.Contains(html, `<span class="badge badge-must">Must</span>`) {
		t.Errorf("missing Must badge")
	}
	if !strings.Contains(html, `<span class="badge badge-should">Should</span>`) {
		t.Errorf("missing Should badge")
	}
	if !strings.Contains(html, `<code>[Must]</code>`) {
		t.Errorf("expected raw '[Must]' inside inline code block to be preserved, got replaced or missing")
	}
	if !strings.Contains(html, "[Confirmed] and **Should** should remain raw") {
		t.Errorf("expected raw code block content to remain unmodified, got replaced or missing")
	}
	if !strings.Contains(html, `<div class="callout callout-warning"><div class="callout-title">WARNING</div>`) {
		t.Errorf("missing callout block")
	}
	if !strings.Contains(html, `<table class="spec-table">`) {
		t.Errorf("missing table styling")
	}
	if !strings.Contains(html, `<div class="mermaid">graph TD`) {
		t.Errorf("missing mermaid div wrapper")
	}
	if !strings.Contains(html, `<code class="language-go">package payment`) {
		t.Errorf("expected syntax highlighted go class class=\"language-go\", got missing or mismatched formatting")
	}
}

// The Page Title is the only place an Agent Session Name appears. The Contents Rail header shows
// the document's own title, so a test that only looked for the name somewhere in the page would
// pass even if the two had been swapped.
func TestRenderSpecPageTitle(t *testing.T) {
	const mdContent = "---\ntitle: Spec\n---\n\n# Spec\n\nBody.\n"

	tests := []struct {
		name             string
		agentSessionName string
		wantTitle        string
	}{
		{"no name renders today's title", "", "<title>Spec</title>"},
		{"a name prefixes it", "demo", "<title>demo — Spec</title>"},
		{"whitespace only counts as no name", "   ", "<title>Spec</title>"},
		{"a name is escaped", `a<b&c"d`, "<title>a&lt;b&amp;c&#34;d — Spec</title>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, err := RenderSpec([]byte(mdContent), tt.agentSessionName)
			if err != nil {
				t.Fatalf("RenderSpec failed: %v", err)
			}
			got := string(output)
			if !strings.Contains(got, tt.wantTitle) {
				t.Errorf("Page Title missing %q", tt.wantTitle)
			}
			if !strings.Contains(got, "<h2>Spec</h2>") {
				t.Error("the Contents Rail header must keep showing the document title")
			}
		})
	}
}

// A name arrives from an agent, and the page shell is text/template: nothing downstream escapes.
func TestNormalizeAgentSessionName(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"   ", ""},
		{"\t\n ", ""},
		{"demo", "demo"},
		{"  demo  ", "demo"},
		{`a<b&c"d`, "a&lt;b&amp;c&#34;d"},
	}
	for _, tt := range tests {
		if got := normalizeAgentSessionName(tt.in); got != tt.want {
			t.Errorf("normalizeAgentSessionName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
