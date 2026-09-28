package reviewer

import (
	"fmt"
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

	output, err := RenderSpec([]byte(mdContent))
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

// The Reply Notification is experimental: the page raises nothing unless the process asked for
// it, and both renderers must carry the choice through, since a review target is either kind.
func TestRender_ReplyNotificationGate(t *testing.T) {
	inputs := map[string][]byte{
		"markdown": []byte("# Heading\n\nProse.\n"),
		"diff":     []byte("diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old\n+new\n"),
	}
	for name, content := range inputs {
		for _, enabled := range []bool{false, true} {
			out, err := Render(content, enabled, "")
			if err != nil {
				t.Fatalf("%s: Render error = %v", name, err)
			}
			want := fmt.Sprintf("const replyNotificationEnabled = %t;", enabled)
			if !strings.Contains(string(out), want) {
				t.Errorf("%s, enabled=%t: page does not contain %q", name, enabled, want)
			}
		}
	}
}

// The Page Title is the only place an Agent Session Name appears. The Contents Rail header shows
// the document's own title, so a test that only looked for the name somewhere in the page would
// pass even if the two had been swapped.
//
// Both kinds go through Render, which is where the name is set: RenderSpec and RenderDiff never
// see it.
func TestRenderPageTitle(t *testing.T) {
	const md = "---\ntitle: Spec\n---\n\n# Spec\n\nBody.\n"
	const oneFile = "diff --git a/diff.go b/diff.go\n--- a/diff.go\n+++ b/diff.go\n@@ -1 +1 @@\n-old\n+new\n"
	const twoFiles = oneFile + "diff --git a/render.go b/render.go\n--- a/render.go\n+++ b/render.go\n@@ -1 +1 @@\n-old\n+new\n"

	tests := []struct {
		name             string
		content          string
		agentSessionName string
		wantTitle        string
		wantRailHeader   string
	}{
		{"markdown, no name", md, "", "<title>Spec</title>", "<h2>Spec</h2>"},
		{"markdown, a name", md, "demo", "<title>demo — Spec</title>", "<h2>Spec</h2>"},
		{"markdown, whitespace only", md, "   ", "<title>Spec</title>", "<h2>Spec</h2>"},
		{"markdown, a name is escaped", md, `a<b&c"d`, "<title>a&lt;b&amp;c&#34;d — Spec</title>", "<h2>Spec</h2>"},
		{"one file, no name", oneFile, "", "<title>diff.go</title>", "<h2>diff.go</h2>"},
		{"one file, a name", oneFile, "demo", "<title>demo — diff.go</title>", "<h2>diff.go</h2>"},
		{"several files, no name", twoFiles, "", "<title>Diff review</title>", "<h2>Diff review</h2>"},
		{"several files, a name", twoFiles, "demo", "<title>demo — Diff review</title>", "<h2>Diff review</h2>"},
		{"diff, a name is escaped", oneFile, `a<b&c"d`, "<title>a&lt;b&amp;c&#34;d — diff.go</title>", "<h2>diff.go</h2>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, err := Render([]byte(tt.content), false, tt.agentSessionName)
			if err != nil {
				t.Fatalf("Render failed: %v", err)
			}
			got := string(output)
			if !strings.Contains(got, tt.wantTitle) {
				t.Errorf("Page Title missing %q", tt.wantTitle)
			}
			if !strings.Contains(got, tt.wantRailHeader) {
				t.Errorf("Contents Rail header missing %q", tt.wantRailHeader)
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
