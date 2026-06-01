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
}
