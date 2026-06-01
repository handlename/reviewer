package reviewer

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStartReviewServer_Feedback(t *testing.T) {
	tempDir := t.TempDir()
	inputPath := filepath.Join(tempDir, "spec.md")
	htmlContent := []byte("<html><body>Hello Spec</body></html>")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	readyChan := make(chan string, 1)
	errChan := make(chan error, 1)

	go func() {
		errChan <- StartReviewServer(ctx, htmlContent, inputPath, 0, true, readyChan)
	}()

	var url string
	select {
	case url = <-readyChan:
		// Ready! No flaky sleeps required.
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for server to signal readiness")
	}

	// Test GET /
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("failed to GET root: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	if !bytes.Equal(body, htmlContent) {
		t.Errorf("expected HTML content %q, got %q", string(htmlContent), string(body))
	}

	// Test POST /api/feedback
	comments := []Comment{
		{
			Text:      "Nice spec",
			Timestamp: time.Now().Format(time.RFC3339),
		},
	}
	commentBytes, err := json.Marshal(comments)
	if err != nil {
		t.Fatalf("failed to marshal comments: %v", err)
	}

	postResp, err := http.Post(url+"/api/feedback", "application/json", bytes.NewReader(commentBytes))
	if err != nil {
		t.Fatalf("failed to POST feedback: %v", err)
	}
	defer postResp.Body.Close()

	if postResp.StatusCode != http.StatusOK {
		t.Errorf("expected POST feedback to return 200 OK, got %d", postResp.StatusCode)
	}

	// Wait for server to shutdown gracefully (since POST /api/feedback triggers shutdown)
	select {
	case err := <-errChan:
		if err != nil {
			t.Errorf("server shutdown returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("timed out waiting for server to shut down after feedback")
	}

	// Verify feedback file was written
	feedbackPath := filepath.Join(tempDir, "spec-feedback.json")
	if _, err := os.Stat(feedbackPath); os.IsNotExist(err) {
		t.Fatalf("expected feedback file to exist at %s", feedbackPath)
	}

	feedbackBytes, err := os.ReadFile(feedbackPath)
	if err != nil {
		t.Fatalf("failed to read feedback file: %v", err)
	}

	var loadedComments []Comment
	if err := json.Unmarshal(feedbackBytes, &loadedComments); err != nil {
		t.Fatalf("failed to unmarshal written feedback: %v", err)
	}

	if len(loadedComments) != 1 || loadedComments[0].Text != "Nice spec" {
		t.Errorf("unexpected written feedback comments: %+v", loadedComments)
	}
}
