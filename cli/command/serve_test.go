package command

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/handlename/reviewer"
)

func TestServeRun_CancelledContext(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "reviewer-serve-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	inputPath := filepath.Join(tmpDir, "spec.md")
	mdContent := `---
title: "Serve Test"
---
# Welcome
[Should] check this out.
`
	if err := os.WriteFile(inputPath, []byte(mdContent), 0644); err != nil {
		t.Fatalf("failed to write temp input: %v", err)
	}

	// We pass a pre-cancelled context so that the server immediately shuts down and does not block the test.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	s := &Serve{
		Input:  inputPath,
		Port:   0, // let net.Listen find an ephemeral port
		NoOpen: true,
	}

	c := &Context{
		Ctx: ctx,
		App: reviewer.New(),
	}

	if err := s.Run(c); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	expectedOutputPath := filepath.Join(tmpDir, "spec.html")
	if _, err := os.Stat(expectedOutputPath); os.IsNotExist(err) {
		t.Fatalf("expected output HTML file %s to be created, but it does not exist", expectedOutputPath)
	}

	htmlBytes, err := os.ReadFile(expectedOutputPath)
	if err != nil {
		t.Fatalf("failed to read generated HTML: %v", err)
	}

	html := string(htmlBytes)
	if !strings.Contains(html, "<title>Serve Test</title>") {
		t.Errorf("expected title 'Serve Test' in HTML, but got missing")
	}
	if !strings.Contains(html, `<span class="badge badge-should">Should</span>`) {
		t.Errorf("expected Should badge in HTML, but got missing")
	}
}
