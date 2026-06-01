package command

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/handlename/reviewer"
)

func TestBuildRun_DefaultOutput(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "reviewer-build-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	inputPath := filepath.Join(tmpDir, "spec.md")
	mdContent := `---
title: "Build Test"
---
# Welcome
[Must] check this out.
`
	if err := os.WriteFile(inputPath, []byte(mdContent), 0644); err != nil {
		t.Fatalf("failed to write temp input: %v", err)
	}

	b := &Build{
		Input: inputPath,
	}

	ctx := &Context{
		Ctx: context.Background(),
		App: reviewer.New(),
	}

	if err := b.Run(ctx); err != nil {
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
	if !strings.Contains(html, "<title>Build Test</title>") {
		t.Errorf("expected title 'Build Test' in HTML, but got missing")
	}
	if !strings.Contains(html, `<span class="badge badge-must">Must</span>`) {
		t.Errorf("expected Must badge in HTML, but got missing")
	}
}

func TestBuildRun_ExplicitOutput(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "reviewer-build-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	inputPath := filepath.Join(tmpDir, "spec.md")
	outputPath := filepath.Join(tmpDir, "custom-output.html")

	mdContent := `---
title: "Explicit Build Test"
---
# Welcome
`
	if err := os.WriteFile(inputPath, []byte(mdContent), 0644); err != nil {
		t.Fatalf("failed to write temp input: %v", err)
	}

	b := &Build{
		Input:  inputPath,
		Output: outputPath,
	}

	ctx := &Context{
		Ctx: context.Background(),
		App: reviewer.New(),
	}

	if err := b.Run(ctx); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		t.Fatalf("expected output HTML file %s to be created, but it does not exist", outputPath)
	}

	htmlBytes, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read generated HTML: %v", err)
	}

	html := string(htmlBytes)
	if !strings.Contains(html, "<title>Explicit Build Test</title>") {
		t.Errorf("expected title 'Explicit Build Test' in HTML, but got missing")
	}
}
